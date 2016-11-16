// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/aclements/go-misc/bench"
	"github.com/aclements/go-moremath/stats"
)

// TODO: Check CPU performance governor before each benchmark.

// TODO: Support running pre-built binaries without specific hashes.
// This is useful for testing things that aren't yet committed or that
// require unusual build steps.

var run struct {
	order      string
	metric     string
	benchFlags string
	buildCmd   string
	iterations int
	saveTree   bool
	timeout    time.Duration
	clean      bool
	cleanFlags string

	logPath string
	binDir  string
}

func init() {
	// TODO: This makes a mess of flags during testing.
	isXBenchmark := false
	if abs, _ := os.Getwd(); strings.HasSuffix(abs, "golang.org/x/benchmarks/bench") {
		isXBenchmark = true
	}

	f := flag.CommandLine
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&run.order, "order", "seq", "run benchmarks in `order`, which must be one of: seq, spread, metric")
	f.StringVar(&run.metric, "metric", "ns/op", "for -order metric, the benchmark metric to find differences in")
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
	f.StringVar(&run.logPath, "o", "", "write benchmark results to `file` (default \"bench.log\" in -d directory)")
	f.StringVar(&run.binDir, "d", ".", "write binaries to `directory`")
	f.BoolVar(&run.saveTree, "save-tree", false, "save Go trees using gover and run benchmarks under saved trees")
	f.DurationVar(&run.timeout, "timeout", 30*time.Minute, "time out a run after `duration`")
	f.BoolVar(&dryRun, "dry-run", false, "print commands but do not run them")
	f.BoolVar(&run.clean, "clean", false, "run \"git clean -f\" after every checkout")
	f.StringVar(&run.cleanFlags, "cleanflags", "", "add `flags` to git clean command")
}

func doRun() {
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	var pickCommit func([]*commitInfo) *commitInfo
	switch run.order {
	case "seq":
		pickCommit = pickCommitSeq
	case "spread":
		pickCommit = pickCommitSpread
	case "metric":
		pickCommit = pickCommitMetric
	default:
		fmt.Fprintf(os.Stderr, "unknown order: %s\n", run.order)
		flag.Usage()
		os.Exit(2)
	}

	if run.logPath == "" {
		run.logPath = filepath.Join(run.binDir, "bench.log")
	}

	commits := getCommits(flag.Args(), run.logPath)

	// Write header block to log.
	if len(commits) > 0 {
		header := new(bytes.Buffer)
		fmt.Fprintf(header, "# Run started at %s\n", time.Now())
		writeHeader(header)
		fmt.Fprintf(header, "\n")
		commits[0].writeLog(header.String())
	}

	// Always run git from the top level of the git tree. Some
	// commands, like git clean, care about this.
	gitDir = trimNL(git("rev-parse", "--show-toplevel"))

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

func writeHeader(w io.Writer) {
	goos, err := exec.Command("go", "env", "GOOS").Output()
	if err != nil {
		log.Fatal("error running go env GOOS: %s", err)
	}
	fmt.Fprintf(w, "goos: %s\n", strings.TrimSpace(string(goos)))

	goarch, err := exec.Command("go", "env", "GOARCH").Output()
	if err != nil {
		log.Fatal("error running go env GOARCH: %s", err)
	}
	fmt.Fprintf(w, "goarch: %s\n", strings.TrimSpace(string(goarch)))

	kernel, err := exec.Command("uname", "-sr").Output()
	if err != nil {
		log.Fatal("error running uname -sr: %s", err)
	}
	fmt.Fprintf(w, "uname-sr: %s\n", strings.TrimSpace(string(kernel)))

	cpuinfo, err := ioutil.ReadFile("/proc/cpuinfo")
	fmt.Printf("cpuinfo=%s\nerr=%s\n", cpuinfo, err)
	if err == nil {
		subs := regexp.MustCompile(`(?m)^model name\s*:\s*(.*)`).FindSubmatch(cpuinfo)
		fmt.Printf("subs: %s\n", subs)
		if subs != nil {
			fmt.Fprintf(w, "cpu: %s\n", string(subs[1]))
		}
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

func pickCommitMetric(commits []*commitInfo) *commitInfo {
	// If there are any partial commits, finish them up.
	for _, c := range commits {
		if c.partial() {
			return c
		}
	}

	// Remove failed commits. This makes it easier to avoid
	// picking a failed commit below.
	ncommits := []*commitInfo{}
	for _, c := range commits {
		if !c.failed() {
			ncommits = append(ncommits, c)
		}
	}
	commits = ncommits
	if len(ncommits) == 0 {
		return nil
	}

	// Make sure we've run the most recent commit.
	if commits[0].runnable() {
		return commits[0]
	}

	// Make sure we've run the earliest commit.
	if c := commits[len(commits)-1]; c.runnable() {
		return c
	}

	// We're bounded from both sides and every commit we've run
	// has the best stats we're going to get. Parse run.metric
	// from the log file.
	logf, err := os.Open(run.logPath)
	if err != nil {
		log.Fatal("opening benchmark log: ", err)
	}
	defer logf.Close()
	bs, err := bench.Parse(logf)
	if err != nil {
		log.Fatal("parsing benchmark log for metrics: ", err)
	}
	results := make(map[string]map[string][]float64)
	for _, b := range bs {
		var hash string
		if commitConfig, ok := b.Config["commit"]; !ok {
			continue
		} else {
			hash = commitConfig.RawValue
		}
		result, ok := b.Result[run.metric]
		if !ok {
			continue
		}

		if results[hash] == nil {
			results[hash] = make(map[string][]float64)
		}
		results[hash][b.Name] = append(results[hash][b.Name], result)
	}
	geomeans := make(map[string]float64)
	for hash, benches := range results {
		var means []float64
		for _, results := range benches {
			means = append(means, stats.Mean(results))
		}
		geomeans[hash] = stats.GeoMean(means)
	}

	// Find the pair of commits with the biggest difference in the
	// metric.
	prevI := -1
	maxDiff, maxMid := -1.0, (*commitInfo)(nil)
	for i, c := range commits {
		if c.count == 0 || geomeans[c.hash] == 0 {
			continue
		}
		if prevI == -1 {
			prevI = i
			continue
		}

		if i > prevI+1 {
			// TODO: This isn't branch-aware. We should
			// only compare commits with an ancestry
			// relationship.
			diff := math.Abs(geomeans[c.hash] - geomeans[commits[prevI].hash])
			if diff > maxDiff {
				maxDiff = diff
				maxMid = commits[(prevI+i)/2]
			}
		}
		prevI = i
	}
	return maxMid
}

// runBenchmark runs the benchmark at commit. It updates commit.count,
// commit.fails, and commit.buildFailed as appropriate and writes to
// the commit log to record the outcome.
func runBenchmark(commit *commitInfo, status *StatusReporter) {
	// Build the benchmark if necessary.
	binPath := filepath.Join(run.binDir, commit.binPath())
	if !exists(binPath) {
		runStatus(status, commit, "building")

		// Check out the appropriate commit. This is necessary
		// even if we're using gover because the benchmark
		// itself might have changed (e.g., bug fixes).
		git("checkout", "-q", commit.hash)

		if run.clean {
			args := append([]string{"-f"}, strings.Fields(run.cleanFlags)...)
			git("clean", args...)
		}

		var buildCmd []string
		if commit.gover {
			buildCmd = []string{"gover", "with", commit.hash}
		} else {
			// If this is the Go toolchain, do a full
			// make.bash. Otherwise, we assume that go
			// test -c will build the necessary
			// dependencies.
			if exists(filepath.Join(gitDir, "src", "make.bash")) {
				cmd := exec.Command("./make.bash")
				cmd.Dir = filepath.Join(gitDir, "src")
				if dryRun {
					dryPrint(cmd)
				} else if out, err := combinedOutputTimeout(cmd); err != nil {
					detail := indent(string(out)) + indent(err.Error())
					fmt.Fprintf(os.Stderr, "failed to build toolchain at %s:\n%s", commit.hash, detail)
					commit.logFailed(true, detail)
					return
				}
				if run.saveTree && doGoverSave() == nil {
					commit.gover = true
				}
			}
			// Assume build command is in $PATH.
			//
			// TODO: Force PATH if we built the toolchain.
			buildCmd = []string{}
		}

		buildCmd = append(buildCmd, strings.Fields(run.buildCmd)...)
		buildCmd = append(buildCmd, "-o", binPath)
		cmd := exec.Command(buildCmd[0], buildCmd[1:]...)
		if dryRun {
			dryPrint(cmd)
		} else if out, err := combinedOutputTimeout(cmd); err != nil {
			detail := indent(string(out)) + indent(err.Error())
			fmt.Fprintf(os.Stderr, "failed to build tests at %s:\n%s", commit.hash, detail)
			commit.logFailed(true, detail)
			return
		}
	}

	// Run the benchmark.
	runStatus(status, commit, "running")
	if filepath.Base(binPath) == binPath {
		// Make exec.Command treat this as a relative path.
		binPath = "./" + binPath
	}
	args := append([]string{binPath}, strings.Fields(run.benchFlags)...)
	if run.saveTree {
		args = append([]string{"gover", "with", commit.hash}, args...)
	}
	cmd := exec.Command(args[0], args[1:]...)
	if dryRun {
		dryPrint(cmd)
		commit.count++
		return
	}
	out, err := combinedOutputTimeout(cmd)
	if err == nil {
		commit.logRun(string(out))
	} else {
		detail := indent(string(out)) + indent(err.Error())
		fmt.Fprintf(os.Stderr, "failed to run benchmark at %s:\n%s", commit.hash, detail)
		commit.logFailed(false, detail)
	}
}

func doGoverSave() error {
	cmd := exec.Command("gover", "save")
	cmd.Dir = gitDir
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

// combinedOutputTimeout is like c.CombinedOutput(), but if
// run.timeout != 0, it will kill c after run.timeout time expires.
func combinedOutputTimeout(c *exec.Cmd) (out []byte, err error) {
	var b bytes.Buffer
	c.Stdout = &b
	c.Stderr = &b
	if err := c.Start(); err != nil {
		return nil, err
	}

	if run.timeout == 0 {
		err := c.Wait()
		return b.Bytes(), err
	}

	tick := time.NewTimer(run.timeout)
	trace := signalTrace
	done := make(chan error)
	go func() {
		done <- c.Wait()
	}()
loop:
	for {
		select {
		case err = <-done:
			break loop
		case <-tick.C:
			if trace != nil {
				fmt.Fprintf(os.Stderr, "command timed out; sending %v\n", trace)
				c.Process.Signal(trace)
				tick = time.NewTimer(5 * time.Second)
				trace = nil
			} else {
				fmt.Fprintf(os.Stderr, "command timed out; killing\n")
				c.Process.Kill()
			}
		}
	}
	tick.Stop()
	return b.Bytes(), err
}
