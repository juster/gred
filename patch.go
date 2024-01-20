package main

import (
	"bytes"
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
	BadPatchIntro, BadContentLine, BadHash error
	UnexpectedEOF                          error
	patchIntroRe                           *regexp.Regexp
	chunkEnd                               []byte
)

func init() {
	BadPatchIntro = errors.New("invalid patch intro")
	BadContentLine = errors.New("path:no\\t should prefix each content line")
	BadHash = errors.New("chunk hash differs from current file")
	UnexpectedEOF = errors.New("unexpected end of file")
	patchIntroRe = regexp.MustCompile(
		"^([^:]+):([0-9]+),([0-9]+) ([0-9]+),([0-9]+) ([0-9a-f]+) {{{$")
	chunkEnd = []byte("}}} ")
}

type patch struct {
	path        string
	hash        []byte
	lines       [][]byte
	start, stop int64
	inputLine   int
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

func (e *patchError) Unwrap() error {
	return e.Wrapped
}

// Read the patch provided as input on standard input. Returns nil, NoPatchInput
// when that input is empty.
//
// TODO: Design a bufio.Scanner or something to avoid loading all input into
// memory?

func patchInput() (map[string][]*patch, error) {
	if len(os.Args) != 1 {
		return nil, nil
	}

	var buf []byte
	var err error
	if buf, err = io.ReadAll(os.Stdin); err != nil {
		return nil, err
	}
	if len(buf) == 0 {
		return nil, nil
	}
	lines := bytes.Split(buf, []byte("\n"))
	if len(lines) > 0 && len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	for i, ln := range lines {
		lines[i] = bytes.TrimSuffix(ln, []byte("\r"))
	}

	patches := make(map[string][]*patch)
	for i, n := 0, 0; i < len(lines); i += n {
		p := &patch{inputLine: i + 1}
		if n, err = nextPatch(p, lines[i:]); err != nil {
			return nil, newPatchError(i+n, lines[i+n], err)
		}
		if n == 0 {
			panic("nextPatch should return error if n == 0")
		}
		patches[p.path] = append(patches[p.path], p)
		fmt.Printf("DBG: i=%d n=%d len=%d\n", i, n, len(lines))
	}
	if err = finishPatchSet(patches); err != nil {
		return nil, err
	}
	return patches, nil
}

// nextPatch reads the next patch chunk where each patch chunk consists
// of separate lines:
// 1. 1-line patch prefix:
//    {{{
//    SPACE
//    (relative path to file) :
//    (line_start 1-indexed) , (line_stop 1-indexed)
//    SPACE
//    (start byte offset) , (end byte offset -- exclusive)
//    SPACE
//    (md5 hash of original bytes)
// 2. n-lines: any (or 0) lines to replace the original
// 3. 1-line: patch suffix:
//    }}}
//    (md5 hash of original bytes)
// 4. 1-line: an empty line
//
// line_start and line_stop specify an inclusive line range.
// start and stop specify an inclusive byte range.

func nextPatch(p *patch, lines [][]byte) (int, error) {
	var n int

	if len(lines) < 3 {
		return n, errors.New("incomplete patch segment")
	}
	m := patchIntroRe.FindSubmatch(lines[0])
	if m == nil {
		return n, BadPatchIntro
	}
	if err := loadPatchPrefix(p, m); err != nil {
		return n, err
	}

	for n = 1; ; n++ {
		if n >= len(lines) {
			return n - 1, errors.New("chunk end not found")
		}
		if isChunkSuffix(lines[n], p.hash) {
			n++
			break
		}
		p.lines = append(p.lines, lines[n])
	}
	return n + countEmptyLines(lines[n:]), nil
}

func loadPatchPrefix(p *patch, m [][]byte) (err error) {
	p.path = string(m[1])
	line_start, err := strconv.ParseInt(string(m[2]), 10, 64)
	if err != nil {
		return
	}
	line_stop, err := strconv.ParseInt(string(m[3]), 10, 64)
	if err != nil {
		return
	}
	p.start, err = strconv.ParseInt(string(m[4]), 10, 64)
	if err != nil {
		return
	}
	p.stop, err = strconv.ParseInt(string(m[5]), 10, 64)
	if err != nil {
		return
	}
	hash_len := len(m[6])
	if hash_len%2 == 1 || hash_len != 2*patchHashSize {
		return fmt.Errorf("hash should be %d chars", 2*patchHashSize)
	}
	p.hash = m[6]

	if line_start < 1 || line_stop < 1 {
		return errors.New("line nos cannot be less than 1")
	}
	if p.start < 0 || p.stop < 0 {
		return errors.New("byte offsets cannot be less than 0")
	}
	line_count := line_stop - line_start + 1
	if line_count <= 0 {
		return errors.New("invalid line range")
	}
	if p.stop-p.start <= 0 {
		return errors.New("invalid byte range")
	}
	return nil
}

func isChunkSuffix(line, hash1 []byte) bool {
	if !bytes.HasPrefix(line, chunkEnd) {
		return false
	}
	hash2 := line[len(chunkEnd):]
	if len(hash1) != len(hash2) {
		return false
	}
	for i, x := range hash1 {
		if hash2[i] != x {
			return false
		}
	}
	return true
}

func countEmptyLines(lines [][]byte) int {
	var i int
	for ; i < len(lines) && len(lines[i]) == 0; i++ {
	}
	return i
}

func finishPatchSet(m map[string][]*patch) error {
	for path, ps := range m {
		sort.Slice(ps, func(i, j int) bool {
			return ps[i].start < ps[j].start
		})
		// check for overlapping byte ranges
		for i := 1; i < len(ps); i++ {
			p, q := ps[i-1], ps[i]
			if p.start == q.start || p.stop > q.start {
				return fmt.Errorf(
					"overlapping patches for %s at input lines %d & %d",
					path, p.inputLine, q.inputLine)
			}
		}
	}
	return nil
}

const apply_buf_len = 1024

var apply_buf [apply_buf_len]byte

func applyPatches(rdr io.Reader, wtr io.Writer, todo []*patch) error {
	var offset int64
	for todo != nil {
		n := todo[0].start - offset
		if n == 0 {
			if err := todo[0].Check(rdr); err != nil {
				return err
			}
			for _, ln := range todo[0].lines {
				fmt.Fprintln(wtr, ln)
			}
			// pretend we resume at the original offset, not the actual (new) one
			offset = todo[0].stop
			todo = todo[1:]
			continue
		}
		if n > apply_buf_len {
			n = apply_buf_len
		}

		m, err := rdr.Read(apply_buf[:n])
		if err != nil {
			return err
		}
		if n > int64(m) {
			return UnexpectedEOF
		}
		offset += n

		if m, err = wtr.Write(apply_buf[:]); err != nil {
			return err
		}
		if m < apply_buf_len {
			return errors.New("failed to write all bytes")
		}
	}

	_, err := io.Copy(wtr, rdr)
	return err
}

func (p patch) Check(rdr io.Reader) error {
	old_len := int(p.stop - p.start)
	var n int
	var err error
	var buf = make([]byte, old_len)
	if n, err = rdr.Read(buf); err != nil {
		return err
	}
	if n != old_len {
		return UnexpectedEOF
	}
	w := strconv.Itoa(2 * patchHashSize)
	crchex := fmt.Sprintf("%0"+w+"x", crc32.ChecksumIEEE(buf))
	if string(p.hash) != crchex {
		return BadHash
	}
	return nil
}
