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
	"syscall"
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
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		before := time.Now()
		if err := cmd.Run(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		after := time.Now()
		fmt.Printf("Benchmark%s\t", benchname)
		fmt.Printf("%d\t%d ns/op", 1, after.Sub(before))
		fmt.Printf("\t%d user-ns/op\t%d sys-ns/op", cmd.ProcessState.UserTime(), cmd.ProcessState.SystemTime())
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			fmt.Printf("\t%d peak-RSS-bytes", ru.Maxrss*(1<<10))
		}
		fmt.Printf("\n")
	}
}
