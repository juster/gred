package main

import (
	"bytes"
	"encoding/ascii85"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	readBufSize  = 1024
	firstSepLeft = '╓'
	crcSepLeft   = '║'
	crcSepRight  = crcSepLeft
)

var (
	BadArgs = errors.New("bad arguments")
)

// Patterns can be positive or negative file globs
type searchConfig struct {
	globs []string
	paths []string
	pat   *regexp.Regexp
}

type walker struct {
	dirs, files []string
	cfg         *searchConfig
}

func NewWalker(root string, cfg *searchConfig) *walker {
	return &walker{dirs: []string{root}, cfg: cfg}
}

func searchInput(args []string) (s *searchConfig, err error) {
	if len(args) == 0 {
		return nil, NoInput
	}
	for _, expr := range args {
		_, err = regexp.Compile(expr)
		if err != nil {
			return
		}
	}
	var pat *regexp.Regexp
	pat, err = regexp.Compile(strings.Join(args, "|"))
	if err != nil {
		return nil, err
	}

	s = &searchConfig{pat: pat}
	s.paths, s.globs = parseSearchTarget(os.Getenv("GRED"))
	extglobs, err := parseExtensions(os.Getenv("GREDX"))
	if err != nil {
		return nil, err
	} else {
		// extglobs may be nil
		s.globs = append(s.globs, extglobs...)
	}
	if s.paths == nil && s.globs == nil {
		return nil, NoInput
	}
	return
}

func parseSearchTarget(target string) (paths, globs []string) {
	for _, trg := range strings.Fields(target) {
		if _, err := os.Lstat(trg); err == nil {
			paths = append(paths, trg)
		} else {
			globs = append(globs, trg)
		}
	}
	return
}

func parseExtensions(dotted string) ([]string, error) {
	str := strings.TrimSpace(dotted)
	switch {
	case str == "":
		return nil, nil
	case str == ".":
		return []string{"*"}, nil
	case str[0] != '.':
		return nil, errors.New("invalid GREDX")
	}
	var globs []string
	for _, ext := range strings.Split(str[1:], ".") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		globs = append(globs, "*."+ext)
	}
	return globs, nil
}

func search(s *searchConfig) error {
	var err error
	if s.paths != nil {
		for _, path := range s.paths {
			if err = grep(path, s); err != nil {
				break
			}
		}
	}
	if s.globs != nil && err == nil {
		w := NewWalker(".", s)
		err = w.Walk()
	}
	return err
}

func (w *walker) Walk() error {
	for len(w.dirs) > 0 {
		var next string
		next, w.dirs = w.dirs[0], w.dirs[1:]
		//fmt.Printf("*DBG* %s\n", next)
		if err := filepath.WalkDir(next, w.filterFunc); err != nil {
			return err
		}
		for _, path := range w.files {
			grep(path, w.cfg)
		}
	}
	return nil
}

func (w *walker) filterFunc(path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	name := d.Name()
	switch {
	case path == ".":
		return nil
	case d.IsDir():
		if name[0] == '.' {
			return fs.SkipDir
		} else {
			w.dirs = append(w.dirs, path)
		}
	}
	for _, g := range w.cfg.globs {
		ok, globErr := filepath.Match(g, name)
		switch {
		case ok:
			w.files = append(w.files, path)
			break
		case globErr != nil:
			return globErr
		}
	}
	return nil
}

// TODO: make more better
func grep(path string, s *searchConfig) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	buf, err := io.ReadAll(f)
	pat, lineno, first := s.pat, 1, true
	for buf != nil {
		m := pat.FindIndex(buf)
		if m == nil {
			break
		}
		j, k := lineExpand(m[0], m[1], buf)
		//fmt.Printf("DBG: j:%d k:%d len:%d buf:%s\n", j, k, len(buf), buf[j:k])
		n, lines := countLines(lineno, buf[:j])
		lineno += lines
		n, lines = printLines(first, path, lineno, buf[n:k])
		if first {
			first = false
		}
		lineno += lines
		buf = buf[k:]
	}
	return nil
}

func countLines(lineno int, buf []byte) (n, lines int) {
	for n < len(buf) {
		i := bytes.IndexByte(buf[n:], '\n')
		if i < 0 {
			break
		}
		lines++
		n += i + 1
	}
	return
}

func printLines(first bool, path string, lineno int, buf []byte) (n, lines int) {
	var line []byte
	for n < len(buf) {
		if i := bytes.IndexByte(buf[n:], '\n'); i < 0 {
			line = buf[n:]
			n += len(buf)
		} else {
			line = buf[n:i]
			n += i + 1
			lines++
		}

		sepLeft := crcSepLeft
		if first {
			sepLeft = firstSepLeft
		}
		fmt.Printf("%c%s\t%s:%d\t%s\n", sepLeft, crcBytes(line), path, lineno+lines, line)
	}
	return
}

func crcBytes(b []byte) []byte {
	buf := &bytes.Buffer{}
	crc := crc32.ChecksumIEEE(b)
	binary.Write(buf, binary.BigEndian, crc)

	dst := make([]byte, ascii85.MaxEncodedLen(4))
	ascii85.Encode(dst, buf.Bytes())
	return dst
}

func lineExpand(i, j int, buf []byte) (int, int) {
	x := 1 + bytes.LastIndexByte(buf[:i], '\n')
	y := j + bytes.IndexByte(buf[j:], '\n')
	if y < j {
		y = len(buf)
	}
	return x, y
}

/******************************************************

type LineSpy struct {
	rdr io.Rdr
	offset, lineNo, prevCount int
}

func NewLineSpy(rdr io.Reader) *LineSpy {
	return &LineSpy{rdr, 1, 0}
}

func (spy *LineSpy) Read(buf []byte) (n int, err error) {
	spy.lineNo += spy.prevCount
	spy.prevCount = 0
	n, err = rdr.Read(buf)
	if n == 0 || err != nil {
		return
	}
	spy.offset += n
	for i := 0; i < len(buf); i++ {
		j := bytes.IndexByte(buf[i:], '\n')
		if j < 0 {
			return
		}
		spy.prevCount++
	}
}

func (spy LineSpy) LineNo() int {
	retun spy.lineNo + spy.prevCount
}

func (spy LineSpy) PrevCount() int {
	return spy.prevCount
}

******************************************************/
