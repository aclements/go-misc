// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Benchmany runs Go benchmarks across many git commits.
//
// Usage:
//
//	benchmany [-C git-dir] [-n iterations] <revision range>
//
// For each revision in <revision range>, benchmany runs the Go
// testing package benchmarks in the current directory <iterations>
// times and writes the benchmark results to log.<commit hash>. For
// the spelling of a revision range, see "SPECIFYING RANGES" in
// gitrevisions(7).
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
	"bufio"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type commitInfo struct {
	hash         string
	binPath      string
	gover        bool
	logPath      string
	count, fails int
	buildFailed  bool
}

var (
	gitDir     string
	topLevel   string
	iterations int
	dryRun     bool
)

// maxFails is the maximum number of benchmark run failures to
// tolerate for a commit before giving up on trying to benchmark that
// commit. Build failures always disqualify a commit.
const maxFails = 5

func main() {
	// TODO: Support running x/benchmarks instead of/in addition
	// to regular benchmarks.

	// TODO: Check CPU performance governor before each benchmark.

	// TODO: Support both sequential and randomized mode.

	// TODO: Support printing list of log files names.

	// TODO: Support adding builds to gover cache.

	// TODO: Flag to specify output directory.

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <revision range>\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.StringVar(&gitDir, "C", "", "run git in `dir`")
	flag.IntVar(&iterations, "n", 5, "run each benchmark `N` times")
	flag.BoolVar(&dryRun, "dry-run", false, "print commands but do not run them")
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	// Get commit sequence.
	hashes := lines(git("rev-list", flag.Args()...))

	// Get gover-cached builds. It's okay if this fails.
	cachedHashes := make(map[string]bool)
	x, _ := exec.Command("gover", "list").CombinedOutput()
	for _, cached := range lines(string(x)) {
		fs := strings.SplitN(cached, " ", 2)
		cachedHashes[fs[0]] = true
	}

	// Get other git information.
	topLevel = trimNL(git("rev-parse", "--show-toplevel"))

	// Load current benchmark state.
	var commits []*commitInfo
	totalRuns := 0
	for _, hash := range hashes {
		logPath := fmt.Sprintf("log.%s", hash[:7])
		count, fails, buildFailed := countRuns(logPath)
		commit := &commitInfo{
			hash:        hash,
			binPath:     fmt.Sprintf("bench.%s", hash[:7]),
			gover:       cachedHashes[hash[:7]],
			logPath:     logPath,
			count:       count,
			fails:       fails,
			buildFailed: buildFailed,
		}
		if commit.runnable() {
			commits = append(commits, commit)
			totalRuns += iterations - commit.count
		}
	}

	fmt.Fprintf(os.Stderr, "Benchmarking %d total runs from %d commits...\n", totalRuns, len(commits))

	for {
		commit := pickCommit(commits)
		if commit == nil {
			break
		}
		runBenchmark(commit)
	}
}

// countRuns parses the log at path and returns the number of
// successful runs, the number of failed runs, and whether the build
// failed.
func countRuns(path string) (count, fails int, buildFailed bool) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return
	} else if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		t := scanner.Text()
		if t == "PASS" {
			count++
		} else if t == "FAILED:" {
			fails++
		} else if t == "BUILD FAILED:" {
			buildFailed = true
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading log %s: %v", path, err)
		os.Exit(1)
	}
	return
}

// runnable returns whether commit c needs to be benchmarked at least
// one more time.
func (c *commitInfo) runnable() bool {
	return !c.buildFailed && c.fails < maxFails && c.count < iterations
}

// partial returns true if this commit is both runnable and already
// has some runs.
func (c *commitInfo) partial() bool {
	return c.count > 0 && c.runnable()
}

// writeLog appends msg to c's log file.
func (c *commitInfo) writeLog(msg []byte) {
	logFile, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := logFile.Write(msg); err != nil {
		log.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		log.Fatal(err)
	}
}

// pickCommit picks the next commit to run from commits.
func pickCommit(commits []*commitInfo) *commitInfo {
	// Assign weights to each commit. This is thoroughly
	// heuristic, but it's geared toward either increasing the
	// iteration count of commits that we have, or picking a new
	// commit so as to spread out the commits we have.
	weights := make([]int, len(commits))
	totalWeight := 0

	nPartial := 0
	for _, commit := range commits {
		if commit.partial() {
			nPartial++
		}
	}
	if nPartial >= len(commits)/10 {
		// Limit the number of partially completed revisions
		// to 10% by only choosing a partial commit in this
		// case.
		for i, commit := range commits {
			if commit.partial() {
				// Bias toward commits that are
				// further from done.
				weights[i] = iterations - commit.count
			}
		}
	} else {
		// Pick a new commit weighted by its distance from a
		// commit that we already have.
		leftDistance, rightDistance := len(commits), len(commits)
		haveAny := false
		for i, lcommit := range commits {
			if lcommit.count > 0 {
				leftDistance = 0
				haveAny = true
			} else if lcommit.runnable() {
				leftDistance++
			}

			ri := len(commits) - i - 1
			rcommit := commits[ri]
			if rcommit.count > 0 {
				rightDistance = 0
			} else if rcommit.runnable() {
				rightDistance++
			}

			if i <= ri || leftDistance < weights[i] {
				weights[i] = leftDistance
			}
			if i < ri || rightDistance < weights[ri] {
				weights[ri] = rightDistance
			}
		}
		if !haveAny {
			// We don't have any commits. Pick one uniformly.
			for i := range commits {
				weights[i] = 1
			}
		}
		// Zero non-runnable commits.
		for i, commit := range commits {
			if !commit.runnable() {
				weights[i] = 0
			}
		}
	}

	for _, w := range weights {
		totalWeight += w
	}
	if totalWeight == 0 {
		return nil
	}

	// Pick a commit based on the weights.
	x := rand.Intn(totalWeight)
	cumulative := 0
	for i, w := range weights {
		cumulative += w
		if cumulative > x {
			return commits[i]
		}
	}
	panic("unreachable")
}

// runBenchmark runs the benchmark at commit. It updates commit.count,
// commit.fails, and commit.buildFailed as appropriate and writes to
// the commit log to record the outcome.
func runBenchmark(commit *commitInfo) {
	// Build the benchmark if necessary.
	if !exists(commit.binPath) {
		status(commit, "building")

		var buildCmd []string
		if commit.gover {
			buildCmd = []string{"gover", "run", commit.hash, "go"}
		} else {
			// Check out the appropriate commit.
			git("checkout", "-q", commit.hash)

			// If this is the Go toolchain, do a full
			// make.bash. Otherwise, we assume that go
			// test -c will build the necessary
			// dependencies.
			if exists(filepath.Join(topLevel, "src", "make.bash")) {
				cmd := exec.Command("./make.bash")
				cmd.Dir = filepath.Join(topLevel, "src")
				if dryRun {
					dryPrint(cmd)
				} else if out, err := cmd.CombinedOutput(); err != nil {
					detail := indent(string(out)) + indent(err.Error())
					fmt.Fprintf(os.Stderr, "failed to build toolchain at %s:\n%s", commit.hash, detail)
					commit.buildFailed = true
					commit.writeLog([]byte("BUILD FAILED:\n" + detail))
					return
				}
				buildCmd = []string{filepath.Join(topLevel, "bin", "go")}
			} else {
				// Assume go is in $PATH.
				buildCmd = []string{"go"}
			}
		}

		buildCmd = append(buildCmd, "test", "-c", "-o", commit.binPath)
		cmd := exec.Command(buildCmd[0], buildCmd[1:]...)
		if dryRun {
			dryPrint(cmd)
		} else if out, err := cmd.CombinedOutput(); err != nil {
			detail := indent(string(out)) + indent(err.Error())
			fmt.Fprintf(os.Stderr, "failed to build tests at %s:\n%s", commit.hash, detail)
			commit.buildFailed = true
			commit.writeLog([]byte("BUILD FAILED:\n" + detail))
			return
		}
	}

	// Run the benchmark.
	status(commit, "running")
	cmd := exec.Command("./"+commit.binPath, "-test.run", "NONE", "-test.bench", ".")
	if dryRun {
		dryPrint(cmd)
		commit.count++
		return
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		commit.count++
	} else {
		fmt.Fprintf(os.Stderr, "failed to run benchmark at %s:\n%s", commit.hash, out)
		commit.fails++
		// Indent the output so we don't get confused by
		// benchmarks in it or "PASS" lines.
		out = []byte("FAILED:\n" + indent(string(out)) + indent(err.Error()))
	}

	// Write the benchmark output.
	commit.writeLog(out)
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
		if !(subcmd == "rev-parse" || subcmd == "rev-list") {
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
