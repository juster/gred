package main

import (
	"bufio"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
)

const (
	patchHashSize = crc32.Size
)

var (
	BadPatchPrefix, BadCRC, UnexpectedEOF, DupPathGroup error
	patchPrefixRe                                       *regexp.Regexp
)

func init() {
	BadPatchPrefix = errors.New("CRC32 path:no\\t should prefix each content line")
	BadCRC = errors.New("current file modified at patch line")
	UnexpectedEOF = errors.New("unexpected end of file")
	DupPathGroup = errors.New("file lines must be grouped by file")
	patchPrefixRe = regexp.MustCompile("^.([A-F0-9]{8}). ([^:]+):([0-9]+)\t")
	seenPath = make(map[string]bool)
}

type patchLine struct {
	n   int
	b   []byte
	crc uint32
}

type patch struct {
	path  string
	lines []*patchLine
}

func newPatchLine(crc, lineno, line []byte) (*patchLine, error) {
	i, err := strconv.ParseUint(string(crc), 16, 32)
	if err != nil {
		return nil, err
	}

	oldCrc, newCrc := uint32(i), crc32.ChecksumIEEE(line)
	if oldCrc == newCrc {
		// patch lines without changes are ignored
		return nil, nil
	}

	j, err := strconv.ParseUint(string(lineno), 10, 32)
	if err != nil {
		return nil, err
	}
	if j < 0 {
		return nil, errors.New("negative line no")
	}

	return &patchLine{n: int(j), b: line, crc: oldCrc}, nil
}

type patchError struct {
	LineNo  int
	Line    []byte
	Wrapped error
}

// lineidx is zero-indexed but LineNo is 1-indexed
func newPatchError(lineIdx int, line []byte, err error) error {
	return &patchError{lineIdx + 1, line, err}
}

func (e *patchError) Error() string {
	return fmt.Sprintf("%v: line %d: %s", e.Wrapped, e.LineNo, e.Line)
}

func (e *patchError) StartsFrom(start int) {
	e.LineNo += start - 1
}

func (e *patchError) Unwrap() error {
	return e.Wrapped
}

// Read the patch provided as input on standard input. Returns nil, NoPatchInput
// when that input is empty.
//
// TODO: Design a bufio.Scanner or something to avoid loading all input into
// memory?

func patchInput() ([]*patch, error) {
	if len(os.Args) != 1 {
		return nil, nil
	}

	scan := bufio.NewScanner(os.Stdin)
	if !scan.Scan() {
		return nil, scan.Err()
	}
	var patches []*patch
	var lineno = 1
	var err error
	for err != io.EOF {
		var p *patch
		var n int
		n, p, err = nextPatch(scan)
		if err != nil && err != io.EOF {
			if patchErr, ok := err.(*patchError); ok {
				patchErr.StartsFrom(lineno)
			}
			return nil, err
		}
		fmt.Printf("*DBG* lineno:%d n:%d\n", lineno, n)
		lineno += n
		if p != nil {
			patches = append(patches, p)
		}
	}
	return patches, nil
}

var seenPath map[string]bool

// nextPatch reads the next lines where each line belongs to the same file.
func nextPatch(scan *bufio.Scanner) (n int, p *patch, err error) {
	line := scan.Bytes()
	m := patchPrefixRe.FindSubmatch(line)
	if m == nil {
		err = newPatchError(0, line, BadPatchPrefix)
		return
	}
	rest := line[len(m[0]):]
	ln, err := newPatchLine(m[1], m[3], rest)
	if err != nil {
		err = newPatchError(0, m[0], err)
		return
	}

	path := string(m[2])
	if seenPath[path] {
		err = newPatchError(0, m[0], DupPathGroup)
		return
	}
	seenPath[path] = true

	p = &patch{}
	p.path = path
	if ln != nil {
		p.lines = []*patchLine{ln}
	}

	var eof bool
	for n = 1; ; n++ {
		if !scan.Scan() {
			if scan.Err() == nil {
				eof = true
			}
			break
		}
		line = scan.Bytes()
		fmt.Printf("*DBG* %d:%s\n", n, line)
		m = patchPrefixRe.FindSubmatch(line)
		if m == nil {
			err = newPatchError(n, line, BadPatchPrefix)
			return
		}
		if p.path != string(m[2]) {
			// End of grep lines for the original path.
			// nextPatch must be stopped and called again.
			break
		}
		rest = line[len(m[0]):]
		ln, err = newPatchLine(m[1], m[3], rest)
		switch {
		case err != nil:
			err = newPatchError(n, m[0], err)
			return
		case ln != nil:
			p.lines = append(p.lines, ln)
		}
	}
	if err = scan.Err(); err != nil {
		return
	}
	if eof {
		err = io.EOF
	}
	// all lines may have been skipped
	if p.lines == nil {
		p = nil
		return
	}
	// Ensure lines are in order.
	sort.Slice(p.lines, func(i, j int) bool {
		return p.lines[i].n < p.lines[j].n
	})
	return
}

var newline = []byte{'\n'}

func (p patch) Apply(rdr io.Reader, wtr io.Writer) error {
	buf := bufio.NewReader(rdr)
	lineno := 1
	for _, ln := range p.lines {
		for ln.n < lineno {
			line, err := buf.ReadBytes('\n')
			if err != nil {
				return err
			}
			if _, err = buf.Discard(1); err != nil {
				return err
			}
			wtr.Write(line)
			wtr.Write(newline)
			lineno++
		}
		if err := ln.Check(buf); err != nil {
			return err
		}
		wtr.Write(ln.b)
	}

	_, err := buf.WriteTo(wtr)
	return err
}

var line_buffer [1024]byte

func (ln patchLine) Check(rdr *bufio.Reader) error {
	line, err := rdr.ReadBytes('\n')
	switch err {
	case nil:
		// ok
	case io.EOF:
		return UnexpectedEOF
	default:
		return err
	}

	crc := crc32.ChecksumIEEE(line)
	if crc != ln.crc {
		return BadCRC
	}
	return nil
}
