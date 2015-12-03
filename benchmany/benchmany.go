// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Benchmany runs Go benchmarks across many git commits.
//
// Usage:
//
//	benchmany [-C git-dir] [-n iterations] <revision range>
//
// For each revision in <revision range>, benchmany runs the
// benchmarks in the current directory <iterations> times and writes
// the benchmark results to log.<commit hash>. Benchmarks may be Go
// testing framework benchmarks or benchmarks from
// golang.org/x/benchmarks. For the spelling of a revision range, see
// "SPECIFYING RANGES" in gitrevisions(7).
//
// Benchmany will check out each revision in git-dir. The current
// directory may or may not be in the same git repository as git-dir.
// If git-dir refers to a Go installation, benchmany will run
// make.bash at each revision; otherwise, it assumes go test can
// rebuild the necessary dependencies.
//
// Benchmany is safe to interrupt. If it is restarted, it will parse
// the benchmark log files to recover its state.
//
// Benchmany is designed to quickly get coverage for large sets of
// revisions. It randomizes the order to run iterations in, but biases
// this order toward covering an evenly distributed set of revisions
// early and finishing all of the iterations of the revisions it has
// started on before moving on to new revisions. This way, if
// benchmany is interrupted, the revisions benchmarked cover the space
// more-or-less evenly.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var gitDir string

// maxFails is the maximum number of benchmark run failures to
// tolerate for a commit before giving up on trying to benchmark that
// commit. Build failures always disqualify a commit.
const maxFails = 5

// TODO: Support printing list of log files names.

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s <subcommand> <args...>\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Subcommands:\n")
		for _, sub := range subcommands {
			fmt.Fprintf(os.Stderr, "  %s %s\n", sub.name, sub.desc)
		}
		fmt.Fprintf(os.Stderr, "See %s <subcommand> -h for details.\n", os.Args[0])
	}
	flag.Parse()
	if flag.NArg() < 1 || subcommands[flag.Arg(0)] == nil {
		flag.Usage()
		os.Exit(2)
	}

	sub := subcommands[flag.Arg(0)]
	sub.flags.Parse(flag.Args()[1:])
	sub.cmd()
}

type subcommand struct {
	name, desc string
	cmd        func()
	flags      *flag.FlagSet
}

var subcommands = make(map[string]*subcommand)

func registerSubcommand(name, desc string, cmd func(), flags *flag.FlagSet) {
	subcommands[name] = &subcommand{name, desc, cmd, flags}
}

// status prints a status message.
func status(commit *commitInfo, status string) {
	// TODO: Indicate progress across all runs.
	fmt.Printf("commit %s, iteration %d/%d: %s...\n", commit.hash[:7], commit.count+1, iterations, status)
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
		fmt.Fprintf(os.Stderr, "git %s failed: %s", args, err)
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
