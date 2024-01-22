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

func searchInput(args []string) (s *searchConfig, err error) {
	if len(args) == 0 {
		return nil, nil
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
	}
	// extglobs may be nil
	s.globs = append(s.globs, extglobs...)
	if s.paths == nil && s.globs == nil {
		return nil, nil
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
		err = walk(".", s)
	}
	return err
}

func walk(root string, cfg *searchConfig) error {
	return filepath.WalkDir(root, cfg.walkFunc)
}

func (cfg *searchConfig) walkFunc(path string, d fs.DirEntry, err error) error {
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
		}
		return nil
	}
	for _, g := range cfg.globs {
		ok, globErr := filepath.Match(g, name)
		switch {
		case ok:
			grep(path, cfg)
			return nil
		case globErr != nil:
			return globErr
		}
	}
	return nil
}

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
