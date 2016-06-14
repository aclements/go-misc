// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Benchmany runs Go benchmarks across many git commits.
//
// Usage:
//
//      benchmany [-C git-dir] [-n iterations] <commit or range>...
//
// benchmany runs the benchmarks in the current directory <iterations>
// times for each commit in <commit or range> and writes the benchmark
// results to bench.log. Benchmarks may be Go testing framework
// benchmarks or benchmarks from golang.org/x/benchmarks.
//
// <commit or range>... can be either a list of individual commits or
// a revision range. For the spelling of a revision range, see
// "SPECIFYING RANGES" in gitrevisions(7). For exact details, see the
// --no-walk option to git-rev-list(1).
//
// Benchmany will check out each revision in git-dir. The current
// directory may or may not be in the same git repository as git-dir.
// If git-dir refers to a Go installation, benchmany will run
// make.bash at each revision; otherwise, it assumes go test can
// rebuild the necessary dependencies. Benchmany also supports using
// gover (https://godoc.org/github.com/aclements/go-misc/gover) to
// save and reuse Go build trees. This is useful for saving time
// across multiple benchmark runs and for benchmarks that depend on
// the Go tree itself (such as compiler benchmarks).
//
// Benchmany supports multiple ways of prioritizing the order in which
// individual iterations are run. By default, it runs in "sequential"
// mode: it runs the first iteration of all benchmarks, then the
// second, and so forth. It also supports a "spread" mode designed to
// quickly get coverage for large sets of revisions. This mode
// randomizes the order to run iterations in, but biases this order
// toward covering an evenly distributed set of revisions early and
// finishing all of the iterations of the revisions it has started on
// before moving on to new revisions. This way, if benchmany is
// interrupted, the revisions benchmarked cover the space more-or-less
// evenly. Finally, it supports a "metric" mode, which zeroes in on
// changes in a benchmark metric by selecting the commit half way
// between the pair of commits with the biggest difference in the
// metric. This is like "git bisect", but for performance.
//
// Benchmany is safe to interrupt. If it is restarted, it will parse
// the benchmark log files to recover its state.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var gitDir string
var dryRun bool

// maxFails is the maximum number of benchmark run failures to
// tolerate for a commit before giving up on trying to benchmark that
// commit. Build failures always disqualify a commit.
const maxFails = 5

func main() {
	flag.Parse()
	doRun()
}

// git runs git subcommand subcmd and returns its stdout. If git
// fails, it prints the failure and exits.
func git(subcmd string, args ...string) string {
	gitargs := []string{}
	if gitDir != "" {
		gitargs = append(gitargs, "-C", gitDir)
	}
	gitargs = append(gitargs, subcmd)
	gitargs = append(gitargs, args...)
	cmd := exec.Command("git", gitargs...)
	cmd.Stderr = os.Stderr
	if dryRun {
		dryPrint(cmd)
		if !(subcmd == "rev-parse" || subcmd == "rev-list" || subcmd == "show") {
			return ""
		}
	}
	out, err := cmd.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "git %s failed: %s\n", shellEscapeList(gitargs), err)
		os.Exit(1)
	}
	return string(out)
}

func dryPrint(cmd *exec.Cmd) {
	out := shellEscape(cmd.Path)
	for _, a := range cmd.Args[1:] {
		out += " " + shellEscape(a)
	}
	if cmd.Dir != "" {
		out = fmt.Sprintf("(cd %s && %s)", shellEscape(cmd.Dir), out)
	}
	fmt.Fprintln(os.Stderr, out)
}

func shellEscape(x string) string {
	if len(x) == 0 {
		return "''"
	}
	for _, r := range x {
		if 'a' <= r && r <= 'z' || 'A' <= r && r <= 'Z' || '0' <= r && r <= '9' || strings.ContainsRune("@%_-+:,./", r) {
			continue
		}
		// Unsafe character.
		return "'" + strings.Replace(x, "'", "'\"'\"'", -1) + "'"
	}
	return x
}

func shellEscapeList(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = shellEscape(x)
	}
	return strings.Join(out, " ")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !os.IsNotExist(err)
}

func trimNL(s string) string {
	return strings.TrimRight(s, "\n")
}

// indent returns s with each line indented by four spaces. If s is
// non-empty, the returned string is guaranteed to end in a "\n".
func indent(s string) string {
	if len(s) == 0 {
		return s
	}
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	return "    " + strings.Replace(s, "\n", "\n    ", -1) + "\n"
}

// lines splits s in to lines. It omits a final blank line, if any.
func lines(s string) []string {
	l := strings.Split(s, "\n")
	if len(l) > 0 && l[len(l)-1] == "" {
		l = l[:len(l)-1]
	}
	return l
}
