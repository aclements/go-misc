// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"go/scanner"
	"go/token"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"plugin"
	"strings"
)

func main() {
	f := os.Stdin
	for {
		src, err := readLine(f)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "error reading %s: %s\n", f, err)
			os.Exit(1)
		}
		_ = src

		src = transform(src)

		so := compile(src)
		if so == "" {
			continue
		}

		run(so)

		// TODO: Declare global exported functions to access
		// all unexported variables and fields. How do I get
		// at types?
	}

	if tempDir != "" {
		// TODO
	}
}

var index int

func readLine(r io.Reader) (string, error) {
	// TODO: Continuation lines.
	fmt.Printf("> ")
	r2 := bufio.NewReader(r)
	return r2.ReadString('\n')
}

var imports []string

func transform(src string) string {
	// TODO: Detect top-level var/type/func/import versus
	// statement versus expression.

	fs := token.NewFileSet()
	var s scanner.Scanner
	// XXX error handler argument?
	s.Init(fs.AddFile("<stdin>", 1, len(src)), []byte(src), nil, 0)

	// Split into top-level statements.
	type stmt struct {
		toks []token.Token
	}

	_, tok, _ := s.Scan()
	if tok == token.IMPORT {
		// XXX Check that it's only imports. Or split statements?
		//src = "package main; " + src

		// XXX Import _ the package to make sure we can import
		// it (and to get inits)
		imports = append(imports, src)
		return "package main; func Main() { }"
	}

	// TODO: Figure out the right subset of current imports for
	// this src.

	// TODO: For expressions, print the result and make it
	// available in a convenience variable.

	// XXX Docs don't say anything about importing "C". Is that
	// necessary?
	src = fmt.Sprintf(`package main
%s
func Main() {
	%s
}`, strings.Join(imports, "\n"), src)
	//import \"fmt\"; func Main() {" + src + "}"
	return src
}

var tempDir string

func compile(src string) string {
	if tempDir == "" {
		var err error
		tempDir, err = ioutil.TempDir("", "goi-")
		if err != nil {
			log.Fatalf("failed to create temporary directory: %s", err)
		}
	}

	// XXX Clean up after loading so.

	pkg := fmt.Sprintf("x%d", index)
	index++

	gopath := os.Getenv("GOPATH")
	if gopath != "" {
		gopath = tempDir + string(filepath.ListSeparator) + gopath
	} else {
		gopath = tempDir
	}

	base := filepath.Join(tempDir, "src", pkg)
	if err := os.MkdirAll(base, 0700); err != nil {
		log.Fatalf("failed to create temporary directory: %s", err)
	}
	path := filepath.Join(base, "x.go")
	if err := ioutil.WriteFile(path, []byte(src), 0600); err != nil {
		log.Fatalf("error writing temporary source: %s", err)
	}
	so := filepath.Join(base, "x.so")
	// TODO: Make sure the runtime is available in plugin mode or
	// else this takes a long time.
	//
	// Assuming dependent packages are installed, we spend most of
	// the time in the linker (and most of that time in the host
	// linker). -w disables DWARF and -s disables the symbol table
	// (XXX is that safe?).
	cmd := exec.Command("go", "build", "-buildmode", "plugin", "-i", "-o", so, "-ldflags=-w -s", pkg)
	cmd.Env = append([]string{"GOPATH=" + gopath}, os.Environ()...)
	// TODO: Translate errors.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err != nil {
		// TODO: Distinguish compile errors from exec error.
		so = ""
	}
	return so
}

func run(so string) {
	p, err := plugin.Open(so)
	if err != nil {
		log.Fatalf("error loading compiled code: %s", err)
	}
	sym, err := p.Lookup("Main")
	if err != nil {
		log.Fatalf("no Main in compiled code: %s", err)
	}
	main, ok := sym.(func())
	if !ok {
		log.Fatal("Main has wrong type")
	}
	main()
}
