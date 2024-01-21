package main

import (
	"bufio"
	"bytes"
	"encoding/ascii85"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
)

var (
	BadPatchPrefix, BadCRC, UnexpectedEOF, DupPathGroup error
	patchPrefixRe                                       *regexp.Regexp
)

func init() {
	BadPatchPrefix = errors.New("CRC32 path:no\\t should prefix each content line")
	BadCRC = errors.New("file modified at edit line, aborting")
	UnexpectedEOF = errors.New("premature end of target file, aborting")
	DupPathGroup = errors.New("file lines must be grouped by file")
	patchPrefixRe = regexp.MustCompile("^.(.....)\t([^:]+):([0-9]+)\t")
	seenPath = make(map[string]bool)
}

type patchLine struct {
	n, srcN int
	b       []byte
	crc     uint32
}

type patch struct {
	path  string
	lines []*patchLine
}

func newPatchLine(crc, lineno, line []byte, srcLineNo int) (*patchLine, error) {
	var crcMem [4]byte
	var oldCrc uint32
	var err error

	_, _, err = ascii85.Decode(crcMem[:], crc, true)
	if err != nil {
		return nil, err
	}
	crcBuf := bytes.NewBuffer(crcMem[:])
	err = binary.Read(crcBuf, binary.BigEndian, &oldCrc)
	if err != nil {
		return nil, err
	}

	// patch lines without changes are ignored
	newCrc := crc32.ChecksumIEEE(line)
	if oldCrc == newCrc {
		return nil, nil
	}

	j, err := strconv.ParseUint(string(lineno), 10, 32)
	if err != nil {
		return nil, err
	}
	if j < 0 {
		return nil, errors.New("negative line no")
	}

	return &patchLine{n: int(j), b: line, crc: oldCrc, srcN: srcLineNo}, nil
}

// patchInput reads the patch provided as input on standard input.
// Returns nil, NoInput when that input is empty.
func patchInput(args []string) ([]*patch, error) {
	if len(args) != 0 {
		warn("patch mode does not accept arguments")
		usage()
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
		n, p, err = parseNextPatch(lineno, scan)
		if err != nil && err != io.EOF {
			return nil, err
		}
		//fmt.Printf("*DBG* lineno:%d n:%d\n", lineno, n)
		lineno += n
		if p != nil {
			patches = append(patches, p)
		}
	}
	return patches, nil
}

var seenPath map[string]bool

// nextPatch reads the next lines where each line belongs to the same file.
func parseNextPatch(lineno int, scan *bufio.Scanner) (n int, p *patch, err error) {
	line := scan.Bytes()
	m := patchPrefixRe.FindSubmatch(line)
	if m == nil {
		err = newPatchInputError(lineno, line, BadPatchPrefix)
		return
	}
	rest := line[len(m[0]):]
	ln, err := newPatchLine(m[1], m[3], rest, lineno)
	if err != nil {
		err = newPatchInputError(lineno, m[0], err)
		return
	}

	path := string(m[2])
	if seenPath[path] {
		err = newPatchInputError(lineno, m[0], DupPathGroup)
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
		//fmt.Printf("*DBG* %d:%s\n", n, line)
		m = patchPrefixRe.FindSubmatch(line)
		if m == nil {
			err = newPatchInputError(lineno+n, line, BadPatchPrefix)
			return
		}
		if p.path != string(m[2]) {
			// End of grep lines for the original path.
			// nextPatch must be stopped and called again.
			break
		}
		rest = line[len(m[0]):]
		ln, err = newPatchLine(m[1], m[3], rest, lineno+n)
		switch {
		case err != nil:
			err = newPatchInputError(lineno+n, m[0], err)
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

func (p patch) Apply() error {
	var rdr, wtr *os.File
	var err error

	rdr, err = os.Open(p.path)
	if err != nil {
		return err
	}
	defer rdr.Close()

	dir, file := filepath.Split(p.path)
	wtr, err = os.CreateTemp(dir, file)
	if err != nil {
		return err
	}

	err = p.pipe(wtr, rdr)
	if err != nil {
		wtr.Close()
		os.Remove(wtr.Name())
	} else {
		err = os.Rename(wtr.Name(), rdr.Name())
	}
	return err
}

var newline = []byte{'\n'}

func (p patch) pipe(wtr io.Writer, rdr io.Reader) error {
	buf := bufio.NewReader(rdr)
	lineno := 1
	for _, ln := range p.lines {
		for lineno < ln.n {
			line, err := buf.ReadBytes('\n')
			if err == io.EOF {
				err = UnexpectedEOF
			}
			if err != nil {
				return newPatchingError(p.path, lineno, ln.srcN, err)
			}
			wtr.Write(line)
			lineno++
		}
		if err := ln.Check(buf); err != nil {
			return newPatchingError(p.path, lineno, ln.srcN, err)
		}
		wtr.Write(ln.b)
		wtr.Write(newline)
		lineno++
	}

	_, err := buf.WriteTo(wtr)
	return err
}

var line_buffer [1024]byte

func (ln patchLine) Check(rdr *bufio.Reader) error {
	line, err := rdr.ReadBytes('\n')
	switch err {
	case nil:
		i := len(line) - 1
		if line[i] == '\n' {
			line = line[:i]
		}
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
