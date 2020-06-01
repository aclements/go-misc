// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"time"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage: %s [flags] command...

stress runs command repeatedly and in parallel and collects failures.

If command exits with status 0, it is considered a pass. If it exits
with any non-zero status besides 125, it is considered a failure. If
it exits with status 125 or doesn't match the pass/fail regexps, it is
considered a flake: neither success nor failure. (Status 125 is the
highest status not used by POSIX shells.) If it times out, it is
considered a flake.

If -pass or -fail regular expressions are provided, they override
pass/fail exit status checking.

The -max-* flags cause the stress tool to exit after some number of
passes, failures, or total runs. This is useful for bisecting a known
flaky failure.

`, os.Args[0])
		flag.PrintDefaults()
	}

	var s Stress
	flag.IntVar(&s.Parallelism, "p", runtime.NumCPU(), "run `N` processes in parallel")
	flag.DurationVar(&s.Timeout, "timeout", 10*time.Minute, "timeout each process after `duration`")
	defaultDir := filepath.Join(os.TempDir(), time.Now().Format("stress-20060102T150405"))
	flag.StringVar(&s.OutDir, "o", defaultDir, "output failure logs to `directory`")
	flag.Var(FlagLimit{&s.MaxRuns}, "max-runs", "exit after `N` passes+fails (but not flakes)")
	flag.Var(FlagLimit{&s.MaxTotalRuns}, "max-total-runs", "exit after `N` runs with any outcome")
	flag.Var(FlagLimit{&s.MaxPasses}, "max-passes", "exit after `N` successful runs")
	flag.Var(FlagLimit{&s.MaxFails}, "max-fails", "exit after `N` failed runs")
	// TODO: Flag for max pass+fail? So you can say "I want 10
	// informative runs". Maybe that should be -max-runs and limit
	// on total runs should be -max-total-runs.
	//
	// TODO: Flag to consider timeouts to be failures?
	//
	// TODO: Flag to keep failed subprocesses around for
	// inspection. I wanted this even for #38440 so I could
	// inspect crash dumps.
	flag.Var(FlagRegexp{&s.FailRe}, "fail", "fail only if output matches `regexp`")
	flag.Var(FlagRegexp{&s.PassRe}, "pass", "pass only if output matches `regexp`")
	flag.Parse()
	s.Command = flag.Args()
	if s.Parallelism <= 0 || s.Timeout <= 0 || len(s.Command) == 0 {
		flag.Usage()
		os.Exit(1)
	}

	// Ensure the output directory exists.
	err := os.MkdirAll(s.OutDir, 0777)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("output to: %s\n", s.OutDir)

	// Trap signals and shut down cleanly.
	//
	// It's important we at least trap the signals that would
	// normally be delivered from the terminal since we put child
	// processes in their own process group.
	interrupt := make(chan struct{})
	s.Interrupt = interrupt
	sig := make(chan os.Signal)
	signal.Notify(sig, exitSignals...)
	go func() {
		<-sig
		// Let a second signal through, in case Stop gets stuck.
		signal.Stop(sig)
		close(interrupt)
	}()

	// Run the stress test.
	result := s.Run(NewStdoutReporter())

	switch result {
	case ResultPass:
		os.Exit(0)
	case ResultFail:
		os.Exit(1)
	case ResultFlake:
		os.Exit(125)
	}
}

type FlagLimit struct {
	x *int
}

func (f FlagLimit) String() string {
	if f.x == nil {
		// The flag package uses the zero value of FlagLimit
		// to test the default string.
		return "<nil>"
	}
	if *f.x <= 0 {
		return "infinity"
	}
	return strconv.FormatInt(int64(*f.x), 10)
}

func (f FlagLimit) Set(x string) error {
	switch x {
	case "inf", "infinity", "none":
		*f.x = 0
		return nil
	}

	limit, err := strconv.ParseInt(x, 10, 0)
	if err != nil {
		return err
	}
	if limit <= 0 {
		return fmt.Errorf("limit must be > 0")
	}
	*f.x = int(limit)
	return nil
}

type FlagRegexp struct {
	x **regexp.Regexp
}

func (f FlagRegexp) String() string {
	if f.x == nil || *f.x == nil {
		return ""
	}
	return (*f.x).String()
}

func (f FlagRegexp) Set(x string) error {
	re, err := regexp.Compile(x)
	if err != nil {
		return err
	}
	*f.x = re
	return nil
}
