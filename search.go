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
	readBufSize   = 1024
	firstSepLeft  = '╓'
	firstSepRight = '╖'
	crcSepLeft    = '║'
	crcSepRight   = crcSepLeft
)

var (
	BadArgs = errors.New("bad arguments")
)

// Patterns can be positive or negative file globs
type searchConfig struct {
	globs []string
	path  string
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
	targ := os.Getenv("GRED")
	ext := os.Getenv("GREDX")
	switch {
	case targ != "":
		if _, err = os.Lstat(targ); err == nil {
			s.path = targ
		} else {
			err = nil
			s.globs = []string{targ}
		}
	case ext != "":
		globs := dotGlobs(ext)
		if globs == nil {
			return nil, errors.New("invalid GREDX")
		} else {
			s.globs = globs
		}
	default:
		return nil, NoInput
	}
	return
}

func dotGlobs(dotted string) []string {
	str := strings.TrimSpace(dotted)
	switch {
	case str == ".":
		return []string{"*"}
	case str[0] != '.':
		return nil
	}
	var globs []string
	for _, ext := range strings.Split(str[1:], ".") {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		globs = append(globs, "*."+ext)
	}
	return globs
}

func search(s *searchConfig) error {
	if s.path != "" {
		return grep(s.path, s)
	}
	w := NewWalker(".", s)
	return w.Walk()
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
		fmt.Fprintln(os.Stderr, "error:", path, err)
		return nil
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
