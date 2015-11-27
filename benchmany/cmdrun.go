// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// TODO: Check CPU performance governor before each benchmark.

// TODO: Support both sequential and randomized mode.

// TODO: Support adding builds to gover cache.

// TODO: Flag to specify output directory.

var (
	topLevel   string
	benchFlags string
	iterations int
	goverSave  bool
	dryRun     bool
)

var cmdRunFlags = flag.NewFlagSet(os.Args[0]+" run", flag.ExitOnError)

func init() {
	f := cmdRunFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s run [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&gitDir, "C", "", "run git in `dir`")
	f.StringVar(&benchFlags, "benchflags", "", "pass `flags` to benchmark")
	f.IntVar(&iterations, "n", 5, "run each benchmark `N` times")
	f.BoolVar(&goverSave, "gover-save", false, "save toolchain builds with gover")
	f.BoolVar(&dryRun, "dry-run", false, "print commands but do not run them")
	registerSubcommand("run", "[flags] <revision range> - run benchmarks", cmdRun, f)
}

func cmdRun() {
	if cmdRunFlags.NArg() < 1 {
		cmdRunFlags.Usage()
		os.Exit(2)
	}

	commits := getCommits(cmdRunFlags.Args(), (*commitInfo).runnable)

	// Get other git information.
	topLevel = trimNL(git("rev-parse", "--show-toplevel"))

	totalRuns := 0
	for _, c := range commits {
		totalRuns += iterations - c.count
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
	isXBenchmark := false
	if abs, _ := os.Getwd(); strings.Contains(abs, "golang.org/x/benchmarks/") {
		isXBenchmark = true
	}

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
				if goverSave && doGoverSave() == nil {
					commit.gover = true
				}
				buildCmd = []string{filepath.Join(topLevel, "bin", "go")}
			} else {
				// Assume go is in $PATH.
				buildCmd = []string{"go"}
			}
		}

		if isXBenchmark {
			buildCmd = append(buildCmd, "build")
		} else {
			buildCmd = append(buildCmd, "test", "-c", "-o", commit.binPath)
		}
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
	var args []string
	if isXBenchmark {
		args = []string{}
	} else {
		args = []string{"-test.run", "NONE", "-test.bench", "."}
	}
	args = append(args, strings.Fields(benchFlags)...)
	cmd := exec.Command("./"+commit.binPath, args...)
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

func doGoverSave() error {
	cmd := exec.Command("gover", "save")
	cmd.Env = append([]string{"GOROOT=" + topLevel}, os.Environ()...)
	if dryRun {
		dryPrint(cmd)
		return nil
	} else {
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gover save failed: %s:\n%s", err, indent(string(out)))
		}
		return err
	}
}
