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

// TODO: Mode to prioritize commits around big performance changes.

// TODO: Support running pre-built binaries without specific hashes.
// This is useful for testing things that aren't yet committed or that
// require unusual build steps.

var run struct {
	order      string
	topLevel   string
	benchFlags string
	buildCmd   string
	iterations int
	goverSave  bool
}

var cmdRunFlags = flag.NewFlagSet(os.Args[0]+" run", flag.ExitOnError)

func init() {
	isXBenchmark := false
	if abs, _ := os.Getwd(); strings.HasSuffix(abs, "golang.org/x/benchmarks/bench") {
		isXBenchmark = true
	}

	f := cmdRunFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s run [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&run.order, "order", "seq", "run benchmarks in `order`, which must be one of: seq, spread")
	f.StringVar(&gitDir, "C", "", "run git in `dir`")
	defaultBenchFlags := "-test.run NONE -test.bench ."
	if isXBenchmark {
		defaultBenchFlags = ""
	}
	f.StringVar(&run.benchFlags, "benchflags", defaultBenchFlags, "pass `flags` to benchmark")
	defaultBuildCmd := "go test -c"
	if isXBenchmark {
		defaultBuildCmd = "go build"
	}
	f.StringVar(&run.buildCmd, "buildcmd", defaultBuildCmd, "build benchmark using \"`cmd` -o <bin>\"")
	f.IntVar(&run.iterations, "n", 5, "run each benchmark `N` times")
	f.StringVar(&outDir, "o", "", "write binaries and logs to `directory`")
	f.BoolVar(&run.goverSave, "gover-save", false, "save toolchain builds with gover")
	f.BoolVar(&dryRun, "dry-run", false, "print commands but do not run them")
	registerSubcommand("run", "[flags] <revision range> - run benchmarks", cmdRun, f)
}

func cmdRun() {
	if cmdRunFlags.NArg() < 1 {
		cmdRunFlags.Usage()
		os.Exit(2)
	}

	var pickCommit func([]*commitInfo) *commitInfo
	switch run.order {
	case "seq":
		pickCommit = pickCommitSeq
	case "spread":
		pickCommit = pickCommitSpread
	default:
		fmt.Fprintf(os.Stderr, "unknown order: %s\n", run.order)
		cmdRunFlags.Usage()
		os.Exit(2)
	}

	commits := getCommits(cmdRunFlags.Args())

	// Get other git information.
	run.topLevel = trimNL(git("rev-parse", "--show-toplevel"))

	status := NewStatusReporter()
	defer status.Stop()

	for {
		doneIters, totalIters, partialCommits, doneCommits, failedCommits := runStats(commits)
		unstartedCommits := len(commits) - (partialCommits + doneCommits + failedCommits)
		msg := fmt.Sprintf("%d/%d runs, %d unstarted+%d partial+%d done+%d failed commits", doneIters, totalIters, unstartedCommits, partialCommits, doneCommits, failedCommits)
		// TODO: Count builds and runs separately.
		status.Progress(msg, float64(doneIters)/float64(totalIters))

		commit := pickCommit(commits)
		if commit == nil {
			break
		}
		runBenchmark(commit, status)
	}
}

func runStats(commits []*commitInfo) (doneIters, totalIters, partialCommits, doneCommits, failedCommits int) {
	for _, c := range commits {
		if c.count >= run.iterations {
			// Don't care if it failed.
			doneIters += c.count
			totalIters += c.count
		} else if c.runnable() {
			doneIters += c.count
			totalIters += run.iterations
		}

		if c.count == run.iterations {
			doneCommits++
		} else if c.runnable() {
			if c.count != 0 {
				partialCommits++
			}
		} else {
			failedCommits++
		}
	}
	return
}

// pickCommitSeq picks the next commit to run based on the most recent
// commit with the fewest iterations.
func pickCommitSeq(commits []*commitInfo) *commitInfo {
	var minCommit *commitInfo
	for _, commit := range commits {
		if !commit.runnable() {
			continue
		}
		if minCommit == nil || commit.count < minCommit.count {
			minCommit = commit
		}
	}
	return minCommit
}

// pickCommitSpread picks the next commit to run from commits using an
// algorithm that spreads out the runs.
func pickCommitSpread(commits []*commitInfo) *commitInfo {
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
				weights[i] = run.iterations - commit.count
			}
		}
	} else {
		// Pick a new commit weighted by its distance from a
		// commit that we already have.

		// Find distance from left to right.
		distance := len(commits)
		haveAny := false
		for i, commit := range commits {
			if commit.count > 0 {
				distance = 1
				haveAny = true
			} else if commit.runnable() {
				distance++
			}
			weights[i] = distance
		}

		// Find distance from right to left.
		distance = len(commits)
		for i := len(commits) - 1; i >= 0; i-- {
			commit := commits[i]
			if commit.count > 0 {
				distance = 1
			} else if commit.runnable() {
				distance++
			}

			if distance < weights[i] {
				weights[i] = distance
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
func runBenchmark(commit *commitInfo, status *StatusReporter) {
	// Build the benchmark if necessary.
	if !exists(commit.binPath) {
		runStatus(status, commit, "building")

		// Check out the appropriate commit. This is necessary
		// even if we're using gover because the benchmark
		// itself might have changed (e.g., bug fixes).
		git("checkout", "-q", commit.hash)

		var buildCmd []string
		if commit.gover {
			// TODO: It would be better if gover took a
			// long hash and did the right thing.
			buildCmd = []string{"gover", "run", commit.hash[:7]}
		} else {
			// If this is the Go toolchain, do a full
			// make.bash. Otherwise, we assume that go
			// test -c will build the necessary
			// dependencies.
			if exists(filepath.Join(run.topLevel, "src", "make.bash")) {
				cmd := exec.Command("./make.bash")
				cmd.Dir = filepath.Join(run.topLevel, "src")
				if dryRun {
					dryPrint(cmd)
				} else if out, err := cmd.CombinedOutput(); err != nil {
					detail := indent(string(out)) + indent(err.Error())
					fmt.Fprintf(os.Stderr, "failed to build toolchain at %s:\n%s", commit.hash, detail)
					commit.buildFailed = true
					commit.writeLog([]byte("BUILD FAILED:\n" + detail))
					return
				}
				if run.goverSave && doGoverSave() == nil {
					commit.gover = true
				}
			}
			// Assume build command is in $PATH.
			//
			// TODO: Force PATH if we built the toolchain.
			buildCmd = []string{}
		}

		buildCmd = append(buildCmd, strings.Fields(run.buildCmd)...)
		buildCmd = append(buildCmd, "-o", commit.binPath)
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
	runStatus(status, commit, "running")
	name := commit.binPath
	if filepath.Base(name) == name {
		// Make exec.Command treat this as a relative path.
		name = "./" + name
	}
	cmd := exec.Command(name, strings.Fields(run.benchFlags)...)
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
	cmd.Dir = run.topLevel
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

// runStatus updates the status message for commit.
func runStatus(sr *StatusReporter, commit *commitInfo, status string) {
	sr.Message(fmt.Sprintf("commit %s, iteration %d/%d: %s...", commit.hash[:7], commit.count+1, run.iterations, status))
}
