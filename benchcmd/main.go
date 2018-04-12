// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command benchcmd times a shell command using Go benchmark format.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [-n iters] benchname cmd...\n", os.Args[0])
		flag.PrintDefaults()
	}
	n := flag.Int("n", 5, "iterations")
	flag.Parse()
	if flag.NArg() < 2 {
		flag.Usage()
		os.Exit(2)
	}
	benchname := flag.Arg(0)
	args := flag.Args()[1:]

	for i := 0; i < *n; i++ {
		fmt.Printf("Benchmark%s\t", benchname)
		cmd := exec.Command(args[0], args[1:]...)
		before := time.Now()
		if err := cmd.Run(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		elapsed := time.Since(before)
		ps := cmd.ProcessState
		fmt.Printf("%d\t%d ns/op\t%d user-ns/op\t%d sys-ns/op\n", 1, elapsed, ps.UserTime(), ps.SystemTime())
	}
}
