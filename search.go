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
	files []string
	pats  []*regexp.Regexp
}

func loadSearchConfig(params []string) (*searchConfig, error) {
	if len(params) == 0 {
		return nil, nil
	}
	var cfg searchConfig
	var arg string
	var i int
	for i, arg = range params {
		switch {
		case arg == "--":
			i++
			break
		case arg[0] == '@':
			arg = arg[1:]
			finfo, err := os.Stat(arg)
			if err != nil || finfo.IsDir() {
				cfg.globs = append(cfg.globs, arg)
			} else {
				cfg.files = append(cfg.files, arg)
			}
		default:
			if err := cfg.pushPattern(arg); err != nil {
				return nil, err
			}
		}
	}
	for ; i < len(params); i++ {
		if err := cfg.pushPattern(params[i]); err != nil {
			return nil, err
		}
	}

	extglobs, err := parseExtensions(os.Getenv("GREDX"))
	if err != nil {
		return nil, err
	}
	// extglobs may be nil
	cfg.globs = append(cfg.globs, extglobs...)
	if cfg.files == nil && cfg.globs == nil {
		return nil, nil
	}
	return &cfg, nil
}

func (cfg *searchConfig) pushPattern(pat string) error {
	re, err := regexp.Compile(pat)
	// may append nil but that's ok
	cfg.pats = append(cfg.pats, re)
	return err
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
	// s.files may be empty
	for _, path := range s.files {
		if err = grep(path, s); err != nil {
			warn("%s", err)
		}
	}
	if len(s.files) > 0 {
		return nil
	}
	if s.globs != nil {
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

type match struct {
	fail bool
	idx [2]int
}

func (m *match) store(idx []int) {
	if idx == nil {
		m.fail = true
		return
	}
	m.idx[0] = idx[0]
	m.idx[1] = idx[1]
}

func (m *match) seek(offset int) bool {
	if m.fail {
		return false
	}
	m.idx[0] -= offset
	m.idx[1] -= offset
	if m.idx[0] < 0 || m.idx[1] < 0 {
		return true
	}
	return false
}

func grep(path string, s *searchConfig) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	buf, err := io.ReadAll(f)
	lineno, first := 1, true

	ms := make([]match, len(s.pats))
	// prime the matches
	for i, pat := range s.pats {
		idx := pat.FindIndex(buf)
		ms[i].store(idx)
	}

	for buf != nil {
		var i, j, min, max int
		j = -1
		// TODO: does not handle multiple matches perfectly
		for i = 0; i < len(ms); i++ {
			m := &ms[i]
			switch {
			case m.fail:
				continue
			case j < 0 || m.idx[0] < min:
				j = i
				min = m.idx[0]
				max = m.idx[1]
			case m.idx[0] == min && m.idx[1] > max:
				max = m.idx[1]
				j = i
			}
		}
		if j < 0 {
			// nothing matched
			break
		}
		j, k := lineExpand(ms[j].idx[0], ms[j].idx[1], buf)
		//fmt.Printf("DBG: j:%d k:%d len:%d buf:%s\n", j, k, len(buf), buf[j:k])
		n, lines := countLines(lineno, buf[:j])
		lineno += lines
		n, lines = printLines(first, path, lineno, buf[n:k])
		if first {
			first = false
		}
		lineno += lines
		buf = buf[k:]
		for i = 0; i < len(ms); i++ {
			if i == j || ms[i].seek(k) {
				idx := s.pats[i].FindIndex(buf)
				ms[i].store(idx)
			}
		}
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
