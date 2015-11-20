// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aclements/go-misc/internal/loganal"
)

// TODO: If searching dashboard logs, optionally print to builder URLs
// instead of local file names.

// TODO: Optionally extract failures and show only those.

// TODO: Optionally classify matched logs by failure (and show either
// file name or extracted failure).

// TODO: Option to print Markdown-friendly output for GitHub.

// TODO: Option to print failure summary versus full failure message.

// TODO: Option to only show failures matching regexp? Currently we
// show all failures in files matching regexp, but sometimes you want
// to search the failures themselves. We could pre-filter the files by
// regexp, extract failures, and then match the failure messages. The
// current behavior is particularly confusing since we only show the
// failures, which may not contain the matched regexps.

var (
	flagRegexpList stringList
	regexpList     []*regexp.Regexp

	flagDashboard = flag.Bool("dashboard", false, "search dashboard logs from fetchlogs")
)

func main() {
	// XXX What I want right now is just to point it at a bunch of
	// logs and have it extract the failures.
	flag.Var(&flagRegexpList, "e", "show files matching `regexp`; if provided multiple times, files must match all regexps")
	flag.Parse()

	// Validate flags.
	for _, flagRegexp := range flagRegexpList {
		re, err := regexp.Compile("(?m)" + flagRegexp)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad regexp %v: %v\n", flagRegexp, err)
			os.Exit(2)
		}
		regexpList = append(regexpList, re)
	}
	if *flagDashboard && flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "-dashboard and paths are incompatible\n")
		os.Exit(2)
	}

	// Gather paths.
	var paths []string
	var stripDir string
	if *flagDashboard {
		revDir := filepath.Join(xdgCacheDir(), "fetchlogs", "rev")
		paths = []string{revDir}
		stripDir = revDir + "/"
	} else {
		paths = flag.Args()
	}

	// Process files
	status := 1
	for _, path := range paths {
		filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				status = 2
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
				return nil
			}
			if info.IsDir() || strings.HasPrefix(filepath.Base(path), ".") {
				return nil
			}

			nicePath := path
			if stripDir != "" && strings.HasPrefix(path, stripDir) {
				nicePath = path[len(stripDir):]
			}

			found, err := process(path, nicePath)
			if err != nil {
				status = 2
				fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			} else if found && status == 1 {
				status = 0
			}
			return nil
		})
	}
	os.Exit(status)
}

func process(path, nicePath string) (found bool, err error) {
	// TODO: Use streaming if possible.
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return false, err
	}

	// Check regexp match.
	for _, re := range regexpList {
		if !re.Match(data) {
			return false, nil
		}
	}

	// Extract failures.
	failures, err := loganal.Extract(string(data), "", "")
	if err != nil {
		return false, err
	}

	// Print failures.
	for _, failure := range failures {
		msg := failure.FullMessage
		if msg == "" {
			msg = failure.Message
		}
		fmt.Printf("%s:\n%s\n\n", nicePath, msg)
	}
	return true, nil
}
