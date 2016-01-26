// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
)

var cmdListFlags = flag.NewFlagSet(os.Args[0]+" list", flag.ExitOnError)

var list struct {
	order string
}

func init() {
	f := cmdListFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s list [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&gitDir, "C", "", "run git in `dir`")
	f.StringVar(&list.order, "d", "forward", "print revisions in \"forward\" or \"backward\" chronological `order`")
	f.StringVar(&outDir, "o", "", "logs are in `directory`")
	registerSubcommand("list", "[flags] <revision range> - print benchmark results", cmdList, f)
}

func cmdList() {
	if cmdListFlags.NArg() < 1 || !(list.order == "forward" || list.order == "backward") {
		cmdListFlags.Usage()
		os.Exit(2)
	}

	commits := getCommits(cmdListFlags.Args())

	// Commits are in backward chronological order by default.
	if list.order == "forward" {
		// Put commits in forward chronological order.
		for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
			commits[i], commits[j] = commits[j], commits[i]
		}
	}

	for _, c := range commits {
		fmt.Printf("%s\n", c.logPath)
	}
}
