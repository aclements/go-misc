// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/pprof"
	"sort"
	"strings"

	"github.com/aclements/go-misc/findflakes/internal/loganal"
)

var (
	flagRevDir = flag.String("dir", defaultRevDir(), "search logs under `directory`")
	flagBranch = flag.String("branch", "master", "analyze commits to `branch`")
	flagHTML   = flag.Bool("html", false, "print an HTML report")
	flagLimit  = flag.Int("limit", 0, "process only most recent `N` revisions")

	// TODO: Is this really just a separate mode? Should we have
	// subcommands?
	flagGrep = flag.String("grep", "", "show analysis for logs matching `regexp`")
)

func defaultRevDir() string {
	paths := append([]string{runtime.GOROOT()}, filepath.SplitList(os.Getenv("GOPATH"))...)

	for _, p := range paths {
		fetchlogs := filepath.Join(p, "src/golang.org/x/buildx/cmd/fetchlogs")
		if st, err := os.Stat(fetchlogs); err == nil && st.IsDir() {
			return filepath.Join(fetchlogs, "rev")
		}
	}
	return ""
}

// TODO: Tool you can point at a failure log to annotate each failure
// in the log with links to past instances of that failure. This just
// uses log analysis.

// TODO: If we were careful about merges, we could potentially use
// information from other branches to add additional samples between
// merge points.

// TODO: Consider each build a separate event, rather than each
// revision. It doesn't matter what "order" they're in, though we
// should randomize it for each revision. History subdivision should
// only happen on revision boundaries.
//
// OTOH, this makes deterministic failures on specific
// OSs/architectures looks like non-deterministic failures.
//
// This would also mean it's more important to identify builds in
// which a test wasn't even executed (e.g., because an earlier test
// failed) so we don't count those as "successes". OTOH, it may be
// sufficient to consider a test executed unless we see a failure in
// that test or that build didn't happen (e.g., a gap in the history).

// TODO: Support pointing this at a set of stress test failures (along
// with the count of total runs, I guess) and having it classify and
// report failures. In this case there's no order or commit sequence
// involved, so there's no time series analysis or
// first/last/culprits, but the classification and failure probability
// are still useful.
//
// It also makes sense to point this at a stress test of a sequence of
// commits, in which case the culprit analysis is still useful. This
// probably integrates well with the previous TODO of considering each
// build a separate event.

func main() {
	var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")

	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	allRevs, err := LoadRevisions(*flagRevDir)
	if err != nil {
		log.Fatal(err)
	}

	// Filter to revisions on this branch.
	revs := []*Revision{}
	for _, rev := range allRevs {
		if rev.Branch == *flagBranch {
			revs = append(revs, rev)
		}
	}
	if len(revs) == 0 {
		log.Fatal("no revisions found")
	}

	// Limit to most recent N revisions.
	if *flagLimit > 0 && len(revs) > *flagLimit {
		revs = revs[len(revs)-*flagLimit:]
	}

	if *flagGrep != "" {
		// Grep mode.
		re, err := regexp.Compile(*flagGrep)
		if err != nil {
			log.Fatal(err)
		}
		failures := grepFailures(revs, re)
		if len(failures) == 0 {
			return
		}
		fc := newFailureClass(revs, failures)
		printTextFlakeReport(os.Stdout, fc)
		return
	}

	// Extract failures from logs.
	failures, meta := extractFailures(revs)

	// Classify failures.
	failureClasses := loganal.Classify(failures)

	// Gather failures from each class and perform flakiness
	// tests.
	classes := []*failureClass{}
	for class, indexes := range failureClasses {
		failures := []*failure{}
		for _, fi := range indexes {
			failures = append(failures, meta[fi])
		}
		fc := newFailureClass(revs, failures)
		fc.Class = class

		// Trim failure classes below thresholds. We leave out
		// classes with extremely low failure probabilities
		// because the chance that these are still happening
		// takes a long time to decay and there's almost
		// nothing we can do for culprit analysis.
		if fc.Current < 0.05 || fc.Latest.FailureProbability < 0.01 {
			continue
		}

		classes = append(classes, fc)
	}

	// Sort failure classes by likelihood that failure is still
	// happening.
	sort.Sort(sort.Reverse(currentSorter(classes)))

	if *flagHTML {
		printHTMLReport(os.Stdout, classes)
	} else {
		printTextReport(os.Stdout, classes)
	}
}

func extractFailures(revs []*Revision) ([]*loganal.Failure, []*failure) {
	// Create failure extraction tasks.
	type Task struct {
		t     int
		build *Build
		res   chan []*loganal.Failure
	}
	tasks := []Task{}
	for t, rev := range revs {
		for _, build := range rev.Builds {
			if build.Status != BuildFailed {
				continue
			}
			tasks = append(tasks, Task{t, build, make(chan []*loganal.Failure, 1)})
		}
	}
	todo := make(chan int)
	go func() {
		for i := range tasks {
			todo <- i
		}
		close(todo)
	}()

	// Run failure extraction.
	for i := 0; i < 4*runtime.GOMAXPROCS(-1); i++ {
		go func() {
			for i := range todo {
				task := tasks[i]

				data, err := task.build.ReadLog()
				if err != nil {
					log.Fatal(err)
				}

				// TODO: OS/Arch
				failures, err := loganal.Extract(string(data), "", "")
				if err != nil {
					log.Printf("%s: %v\n", task.build.LogPath())
					task.res <- nil
				} else {
					task.res <- failures
				}
			}
		}()
	}

	// Gather results.
	failures := []*loganal.Failure{}
	meta := []*failure{}
	for _, task := range tasks {
		extracted := <-task.res

		for _, f := range extracted {
			// Ignore build failures.
			if strings.Contains(f.Message, "build failed") {
				continue
			}

			failures = append(failures, f)
			meta = append(meta, &failure{
				T:          task.t,
				CommitsAgo: len(revs) - task.t - 1,
				Rev:        revs[task.t],
				Build:      task.build,
			})
		}
	}
	return failures, meta
}

func grepFailures(revs []*Revision, re *regexp.Regexp) []*failure {
	// TODO: Unify better with extractFailures. Consider moving
	// *loganal.Failure in to struct failure so the task is to
	// produce a *failure in both functions.

	// Create grep tasks.
	type Task struct {
		t     int
		build *Build
		res   chan *failure
	}
	tasks := []Task{}
	for t, rev := range revs {
		for _, build := range rev.Builds {
			if build.Status != BuildFailed {
				continue
			}
			tasks = append(tasks, Task{t, build, make(chan *failure, 1)})
		}
	}
	todo := make(chan int)
	go func() {
		for i := range tasks {
			todo <- i
		}
		close(todo)
	}()

	// Run failure extraction.
	for i := 0; i < 4*runtime.GOMAXPROCS(-1); i++ {
		go func() {
			for i := range todo {
				task := tasks[i]

				data, err := task.build.ReadLog()
				if err != nil {
					log.Fatal(err)
				}

				if re.Match(data) {
					task.res <- &failure{
						T:          task.t,
						CommitsAgo: len(revs) - task.t - 1,
						Rev:        revs[task.t],
						Build:      task.build,
					}
				} else {
					task.res <- nil
				}
			}
		}()
	}

	// Gather results.
	failures := []*failure{}
	for _, task := range tasks {
		f := <-task.res
		if f != nil {
			failures = append(failures, f)
		}
	}
	return failures
}

type failure struct {
	T          int
	CommitsAgo int
	Rev        *Revision
	Build      *Build
}

type failureClass struct {
	// Class gives the common features of this failure class.
	Class loganal.Failure

	// Revs is the sequence of all revisions indexed by time (both
	// success and failure).
	Revs []*Revision

	// Failures is a slice of all failures, by order of increasing
	// time T. Note that there may be more than one failure at the
	// same time T.
	Failures []*failure

	// Test is the results of the flake test for this failure
	// class.
	Test *FlakeTestResult

	// Latest is the latest flake region (Test.All[0]).
	Latest *FlakeRegion

	// Current is the probability that this failure is still
	// happening.
	Current float64
}

func newFailureClass(revs []*Revision, failures []*failure) *failureClass {
	fc := failureClass{
		Revs:     revs,
		Failures: failures,
	}
	times := []int{}
	for i, f := range failures {
		t := f.T
		if i == 0 || times[len(times)-1] != t {
			times = append(times, t)
		}
	}
	fc.Test = FlakeTest(times)
	fc.Latest = &fc.Test.All[0]
	fc.Current = fc.Latest.StillHappening(len(revs) - 1)
	return &fc
}

type currentSorter []*failureClass

func (s currentSorter) Len() int {
	return len(s)
}

func (s currentSorter) Less(i, j int) bool {
	if s[i].Current != s[j].Current {
		return s[i].Current < s[j].Current
	}
	if s[i].Latest.FailureProbability != s[j].Latest.FailureProbability {
		return s[i].Latest.FailureProbability < s[j].Latest.FailureProbability
	}
	return s[i].Class.String() < s[j].Class.String()
}

func (s currentSorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
