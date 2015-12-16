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
	"strings"

	"github.com/aclements/go-misc/internal/loganal"
)

// TODO: If searching dashboard logs, optionally print to builder URLs
// instead of local file names.

// TODO: Optionally extract failures and show only those.

// TODO: Optionally classify matched logs by failure (and show either
// file name or extracted failure).

// TODO: Option to print failure summary versus full failure message.

var (
	fileRegexps regexpList
	failRegexps regexpList

	flagDashboard = flag.Bool("dashboard", false, "search dashboard logs from fetchlogs")
	flagMD        = flag.Bool("md", false, "output in Markdown")
	flagFilesOnly = flag.Bool("l", false, "print only names of matching files")
)

func main() {
	// XXX What I want right now is just to point it at a bunch of
	// logs and have it extract the failures.
	flag.Var(&fileRegexps, "e", "show files matching `regexp`; if provided multiple times, files must match all regexps")
	flag.Var(&failRegexps, "E", "show only errors matching `regexp`; if provided multiple times, an error must match all regexps")
	flag.Parse()

	// Validate flags.
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
	if !fileRegexps.AllMatch(data) || !failRegexps.AllMatch(data) {
		return false, nil
	}

	// If this is from the dashboard, get the builder URL.
	var logURL string
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), ".rev.json")); err == nil {
		// TODO: Get the URL from the rev.json metadata
		link, err := os.Readlink(path)
		if err == nil {
			hash := filepath.Base(link)
			logURL = "https://build.golang.org/log/" + hash
		}
	}

	printPath := nicePath
	if *flagMD && logURL != "" {
		printPath = fmt.Sprintf("[%s](%s)", nicePath, logURL)
	}

	if *flagFilesOnly {
		fmt.Printf("%s\n", printPath)
		return true, nil
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

		if len(failRegexps) > 0 && !failRegexps.AllMatch([]byte(msg)) {
			continue
		}

		fmt.Printf("%s:\n", printPath)
		if *flagMD {
			fmt.Printf("```\n%s\n```\n\n", msg)
		} else {
			fmt.Printf("%s\n\n", msg)
		}
	}
	return true, nil
}
