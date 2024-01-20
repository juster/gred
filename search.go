package main

import (
	"bytes"
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
type searcher struct {
	buf                [readBufSize]byte
	files, dirs, globs []string
	pat                *regexp.Regexp
}

func searcherInput() (*searcher, error) {
	if len(os.Args) < 2 {
		return nil, BadArgs
	}
	for _, expr := range os.Args[1:] {
		_, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
	}
	pat, err := regexp.Compile(strings.Join(os.Args[1:], "|"))
	if err != nil {
		return nil, err
	}

	ext := os.Getenv("GREDEXT")
	if ext == "" {
		return nil, errors.New("missing GREDEXT")
	}
	globs, ok := dotGlobs(ext)
	if !ok {
		return nil, errors.New("invalid GREDEXT")
	}
	return &searcher{pat: pat, globs: globs}, nil
}

func dotGlobs(dotted string) ([]string, bool) {
	str := strings.TrimSpace(dotted)
	switch {
	case str == ".":
		return []string{"*"}, true
	case str[0] != '.':
		return nil, false
	}
	var globs []string
	for _, ext := range strings.Split(str[1:], ".") {
		if ext == "" {
			continue
		}
		globs = append(globs, "*."+ext)
	}
	return globs, true
}

func (s *searcher) Walk(dir string) error {
	if err := filepath.WalkDir(dir, s.filterFunc); err != nil {
		return err
	}
	for _, path := range s.files {
		s.grep(path)
	}
	// breadth-first search
	var next string
	for len(s.dirs) > 0 {
		next, s.dirs = s.dirs[0], s.dirs[1:]
		if err := s.Walk(next); err != nil {
			return err
		}
	}
	return nil
}

func (s *searcher) filterFunc(path string, d fs.DirEntry, err error) error {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", path, err)
		return nil
	}
	if d.IsDir() {
		if path[0] != '.' {
			s.dirs = append(s.dirs, path)
		}
		return nil
	}
	for _, g := range s.globs {
		ok, globErr := filepath.Match(g, d.Name())
		switch {
		case ok:
			s.files = append(s.files, path)
		case globErr != nil:
			return globErr
		}
	}
	return nil
}

// TODO: make more better
func (s *searcher) grep(path string) error {
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

		sepLeft, sepRight := crcSepLeft, crcSepRight
		if first {
			sepLeft, sepRight = firstSepLeft, firstSepRight
		}
		crc := crc32.ChecksumIEEE(line)
		fmt.Printf("%c%08X%c %s:%d\t%s\n", sepLeft, crc, sepRight, path, lineno+lines, line)
	}
	return
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
