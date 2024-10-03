// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// gc-S reads the output of compile -S to find a symbol and symbols it
// references.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: <compile -S output> | %s regexp\n", os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	regexp, err := regexp.Compile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "regexp error: %s\n", err)
		os.Exit(1)
	}

	symCh := parseSyms(os.Stdin)

	// Collect all symbols. For matching symbols, print them immediately and add
	// them as roots to the trace.
	syms := make(map[string]Sym)
	q := []string{}
	printed := make(map[string]bool) // false = added, not printed
	for sym := range symCh {
		if regexp.MatchString(sym.name) {
			sym.Print(os.Stdout)
			printed[sym.name] = true
			q = append(q, sym.name)
		}
		syms[sym.name] = sym
	}

	// Trace referenced symbols.
	for len(q) > 0 {
		if sym, ok := syms[q[0]]; ok {
			if !printed[q[0]] {
				printed[q[0]] = true
				sym.Print(os.Stdout)
			}
			for _, ref := range sym.Refs() {
				if _, ok := printed[ref]; ok {
					continue
				}
				printed[ref] = false
				q = append(q, ref)
			}
		}
		q = q[1:]
	}
}

type Sym struct {
	name string
	data string
}

func parseSyms(r io.Reader) <-chan Sym {
	ch := make(chan Sym)
	go func() {
		defer close(ch)

		scanner := bufio.NewScanner(r)
		var accum bytes.Buffer
		var name string
		flush := func() {
			if name != "" {
				ch <- Sym{name, accum.String()}
				name = ""
				accum.Reset()
			}
		}
		for scanner.Scan() {
			l := scanner.Text()
			switch {
			case strings.HasPrefix(l, "#"):
				// Ignore
			default:
				flush()
				name, _, _ = strings.Cut(l, " ")
				fallthrough
			case len(l) == 0 || l[0] == '\t':
				accum.WriteString(l)
				accum.WriteByte('\n')
			}
		}
		flush()
		if err := scanner.Err(); err != nil {
			fmt.Fprintln(os.Stderr, "reading standard input:", err)
		}
	}()
	return ch
}

var printPathRe = regexp.MustCompile(`(?m)^\t0x[0-9a-f]+ [0-9]+ \(([^)]+)\)`)

func (s Sym) Print(w io.Writer) {
	// Simplify paths and align tabs.
	tw := tabwriter.NewWriter(w, 1, 4, 1, ' ', tabwriter.TabIndent)
	prev := 0
	for _, idx := range printPathRe.FindAllStringSubmatchIndex(s.data, -1) {
		a, b := idx[2], idx[3]
		path := s.data[a:b]
		if path == "<unknown line number>" {
			path = "???"
		} else if filepath.IsAbs(path) {
			path = "…/" + filepath.Base(path)
		}
		fmt.Fprintf(tw, "%s%s", s.data[prev:a], path)
		prev = b
	}
	fmt.Fprintf(tw, "%s", s.data[prev:])
	tw.Flush()
}

var refRe = regexp.MustCompile(`\b[^\s]+\(SB\)`)

func (s Sym) Refs() []string {
	refs := refRe.FindAllString(s.data, -1)
	for i, ref := range refs {
		refs[i] = ref[:len(ref)-len("(SB)")]
	}
	return refs
}
