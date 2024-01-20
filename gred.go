package main

import (
	"errors"
	"fmt"
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
	var err error
	for _, p := range patches {
		if err = p.Apply(); err != nil {
			die("%v", err)
		}
		fmt.Printf("%s %d\n", p.path, len(p.lines))
	}
}

func warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func main() {
	if len(os.Args) == 1 {
		patches, err := patchInput()
		if err != nil {
			die("%v", err)
		}
		if patches == nil {
			warn("stdin patches included no changes and were ignored")
			return
		}
		runPatch(patches)
	} else {
		runSearch()
	}
}
