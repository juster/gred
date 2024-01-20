package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

func runSearch() {
	s, err := searcherInput()
	if err != nil {
		goto PrintError
	}
	if err = s.Walk("."); err == nil {
		return
	}

PrintError:
	if errors.Is(err, BadArgs) {
		fmt.Fprintln(os.Stderr, "usage: GREDEXT=.foo.bar gred [regexp1] [regexp2]")
	} else {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	os.Exit(2)
}

func runPatch(patches []*patch) {
	for _, p := range patches {
		rdr, err := os.Open(p.path)
		wtr := io.Discard
		if err == nil {
			//wtr, err = os.Open(path)
		}
		if err != nil {
			rdr.Close()
			//wtr.Close() // may be nil but that's ok
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		p.Apply(rdr, wtr)
		rdr.Close()
		//wtr.Close()
	}
}

func main() {
	if len(os.Args) == 1 {
		patches, err := patchInput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if patches == nil {
			fmt.Fprintln(os.Stderr, "warning: stdin patches included no changes and were ignored")
			return
		}
		runPatch(patches)
	} else {
		runSearch()
	}
}
