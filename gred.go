package main

import (
	"errors"
	"fmt"
	"os"
)

func runSearch() {
	fmt.Println("DBG Searching...")
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

func runPatch(patches map[string][]*patch) {
	fmt.Println("DBG Patching...")
	for path, ps := range patches {
		for _, p := range ps {
			fmt.Printf("DBG %s %d,%d %s\n", p.path, p.start, p.stop, p.hash)
			for _, ln := range p.lines {
				fmt.Printf("DBG %s\n", ln)
			}
		}
		rdr, err := os.Open(path)
		var wtr *os.File
		if err == nil {
			wtr, err = os.Open(path)
		}
		if err != nil {
			rdr.Close()
			wtr.Close() // may be nil but that's ok
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			continue
		}

		applyPatches(rdr, wtr, ps)
		rdr.Close()
		wtr.Close()
	}
}

func main() {
	patches, err := patchInput()
	switch {
	case err != nil:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	case patches != nil:
		runPatch(patches)
	default:
		// if there is no patch chunks supplied on stdin then we must search
		runSearch()
	}
}
