// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command greplogs searches Go builder logs.
//
//     greplogs [flags] (-e regexp|-E regexp) paths...
//     greplogs [flags] (-e regexp|-E regexp) -dashboard
//
// greplogs finds builder logs matching a given set of regular
// expressions in Go syntax (godoc.org/regexp/syntax) and extracts
// failures from them.
//
// greplogs can search an arbitrary set of files just like grep.
// Alternatively, the -dashboard flag causes it to search the logs
// saved locally by fetchlogs (golang.org/x/build/cmd/fetchlogs).
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
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
	flagColor     = flag.String("color", "auto", "highlight output in color: `mode` is never, always, or auto")

	color *colorizer
)

const (
	colorPath      = colorFgMagenta
	colorPathColon = colorFgCyan
	colorMatch     = colorBold | colorFgRed
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
	switch *flagColor {
	case "never":
		color = newColorizer(false)
	case "always":
		color = newColorizer(true)
	case "auto":
		color = newColorizer(canColor())
	default:
		fmt.Fprintf(os.Stderr, "-color must be one of never, always, or auto")
		os.Exit(2)
	}

	// Gather paths.
	var paths []string
	var stripDir string
	if *flagDashboard {
		revDir := filepath.Join(xdgCacheDir(), "fetchlogs", "rev")
		fis, err := ioutil.ReadDir(revDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s\n", revDir, err)
			os.Exit(1)
		}
		for _, fi := range fis {
			if !fi.IsDir() {
				continue
			}
			paths = append(paths, filepath.Join(revDir, fi.Name()))
		}
		sort.Sort(sort.Reverse(sort.StringSlice(paths)))
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
		fmt.Printf("%s\n", color.color(printPath, colorPath))
		return true, nil
	}

	// Extract failures.
	failures, err := loganal.Extract(string(data), "", "")
	if err != nil {
		return false, err
	}

	// Print failures.
	for _, failure := range failures {
		var msg []byte
		if failure.FullMessage != "" {
			msg = []byte(failure.FullMessage)
		} else {
			msg = []byte(failure.Message)
		}

		if len(failRegexps) > 0 && !failRegexps.AllMatch(msg) {
			continue
		}

		fmt.Printf("%s%s\n", color.color(printPath, colorPath), color.color(":", colorPathColon))
		if *flagMD {
			fmt.Printf("```\n")
		}
		if !color.enabled {
			fmt.Printf("%s", msg)
		} else {
			// Find specific matches and highlight them.
			matches := mergeMatches(append(fileRegexps.Matches(msg),
				failRegexps.Matches(msg)...))
			printed := 0
			for _, m := range matches {
				fmt.Printf("%s%s", msg[printed:m[0]], color.color(string(msg[m[0]:m[1]]), colorMatch))
				printed = m[1]
			}
			fmt.Printf("%s", msg[printed:])
		}
		if *flagMD {
			fmt.Printf("\n```")
		}
		fmt.Printf("\n\n")
	}
	return true, nil
}

func mergeMatches(matches [][]int) [][]int {
	sort.Slice(matches, func(i, j int) bool { return matches[i][0] < matches[j][0] })
	for i := 0; i < len(matches); {
		m := matches[i]

		// Combine with later matches.
		j := i + 1
		for ; j < len(matches); j++ {
			m2 := matches[j]
			if m[1] <= m2[0] {
				// Overlapping or exactly adjacent.
				if m2[1] > m[1] {
					m[1] = m2[1]
				}
				m2[0], m2[1] = 0, 0
			} else {
				break
			}
		}
		i = j
	}

	// Clear out combined matches.
	j := 0
	for _, m := range matches {
		if m[0] == 0 && m[1] == 0 {
			continue
		}
		matches[j] = m
		j++
	}
	return matches[:j]
}
