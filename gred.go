package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
)

var (
	NoInput   = errors.New("No input")
	patchFlag = flag.Bool("p", false, "patch mode: feed in edited gred match output")
)

func init() {
	flag.Usage = usage
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:

Search:
	(must set GRED or GREDX env var to specify files to search)
	GRED=*.glob gred '<[^>]+>'
	GRED=./path/to/file gred -- -p (-- flag let you search for "-p", yay!)
	GREDX=.foo.bar gred foo (search *.foo and *.bar files)
	GREDX=. gred [regexp1] (GREDX=. matches all files)

Patch:
	GRED=. gred foobar > gred.out
	vim gred.out
	cat gred.out | gred -p
`)
	flag.PrintDefaults()
	os.Exit(2)
}

func warn(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

func patchMode(patches []*patch) {
	for _, p := range patches {
		if patchErr := p.Apply(); patchErr != nil {
			warn("%v", patchErr)
			continue
		}
		fmt.Printf("%s %d\n", p.path, len(p.lines))
	}
}

func main() {
	var args []string
	if len(os.Args) < 2 || os.Args[1] != "--" {
		flag.Parse()
		args = flag.Args()
	} else {
		args = os.Args[2:]
	}

	if *patchFlag {
		patches, err := patchInput(args)
		switch {
		case err == NoInput:
			usage()
		case err != nil:
			die("%v", err)
		case patches == nil:
			warn("stdin patches included no changes and were ignored")
		default:
			patchMode(patches)
		}
		return
	}

	s, err := searchInput(args)
	switch {
	case err == NoInput:
		fallthrough
	case s == nil:
		usage()
	case err != nil:
		die("%v", err)
	default:
		search(s)
	}
}
