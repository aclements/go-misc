// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path"
	"regexp"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"
)

// A Stress stress tests a command.
type Stress struct {
	Command     []string
	Parallelism int
	Timeout     time.Duration
	OutDir      string

	MaxPasses    int // If 0, no limit
	MaxFails     int
	MaxRuns      int // Limit on passes+fails (but not flakes)
	MaxTotalRuns int // Limit on all types of runs

	FailRe *regexp.Regexp
	PassRe *regexp.Regexp

	Interrupt <-chan struct{}
}

type startRun struct {
	id int64
}

type result struct {
	id     int64
	output *os.File
	status *os.ProcessState // nil on timeout
	err    error            // If non-nil, error starting command
}

type ResultKind int

const (
	ResultPass ResultKind = iota
	ResultFail
	ResultFlake
	ResultTimeout
)

func (s *Stress) resultKind(res result, output []byte) ResultKind {
	switch {
	case res.status == nil:
		return ResultTimeout
	case s.PassRe == nil && res.status.Success(),
		s.PassRe != nil && s.PassRe.Match(output):
		return ResultPass
	case s.FailRe == nil && res.status.ExitCode() != 125,
		s.FailRe != nil && s.FailRe.Match(output):
		return ResultFail
	default:
		return ResultFlake
	}
}

func (s *Stress) Run(reporter StressReporter) ResultKind {
	// Replace "0 as infinity" limits with a value that's easy to
	// compare against.
	const MaxInt = int(^uint(0) >> 1)
	for _, limit := range []*int{&s.MaxPasses, &s.MaxFails, &s.MaxRuns, &s.MaxTotalRuns} {
		if *limit <= 0 {
			*limit = MaxInt
		}
	}

	start := make(chan startRun, s.Parallelism)
	stop := make(chan struct{})
	results := make(chan result, s.Parallelism)
	var id int64
	activeStartTimes := make(map[int64]time.Time)

	reporter.StartStatus()

	// TODO: Do a smoke test. Start just one task and if it fails
	// within a second, go into rate-limited starting mode.

	var wg sync.WaitGroup
	for i := 0; i < s.Parallelism; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runner(start, stop, results)
		}()
		start <- startRun{id}
		activeStartTimes[id] = time.Now()
		id++
	}

	// TODO: Rate limit restarts after failures.

	fatal := false
	totalRuns := 0
	counts := make(map[ResultKind]int)
	logIdxPass, logIdxFail, logIdxFlake := 0, 0, 0
	var passFailTime time.Duration
	updateStatus := func() {
		// TODO: ETA if we have s.Max*?
		buf := new(bytes.Buffer)
		fmt.Fprintf(buf, "%d passes, %d fails", counts[ResultPass], counts[ResultFail])
		if n := counts[ResultFlake]; n > 0 {
			fmt.Fprintf(buf, ", %d flakes", n)
		}
		if n := counts[ResultTimeout]; n > 0 {
			fmt.Fprintf(buf, ", %d timeouts", n)
		}
		var avg interface{} = "?"
		if passFail := counts[ResultPass] + counts[ResultFail]; passFail > 0 {
			avg = (passFailTime / time.Duration(passFail)).Round(time.Second)
		}
		var oldest time.Time
		for _, t := range activeStartTimes {
			if oldest.IsZero() || t.Before(oldest) {
				oldest = t
			}
		}
		reporter.Status("%s, avg %s, max active %s", buf.String(), avg, TimeSince(oldest))
	}
loop:
	for {
		updateStatus()

		var res result
		select {
		case res = <-results:
		case <-s.Interrupt:
			break loop
		}

		if res.err != nil {
			log.Printf("error starting command: %s", res.err)
			fatal = true
			break
		}

		// Read the command output back from the log file.
		if _, err := res.output.Seek(0, 0); err != nil {
			log.Printf("error seeking log file: %s", err)
			fatal = true
			break
		}
		output, err := ioutil.ReadAll(res.output)
		if err != nil {
			log.Printf("error reading log file: %s", err)
			fatal = true
			break
		}
		logPath := res.output.Name()
		if err := res.output.Close(); err != nil {
			log.Printf("error saving log file: %s", err)
			fatal = true
			break
		}

		// Classify the result.
		kind := s.resultKind(res, output)
		totalRuns++
		counts[kind]++

		// Update time stats.
		duration := time.Since(activeStartTimes[res.id])
		delete(activeStartTimes, res.id)
		if kind == ResultPass || kind == ResultFail {
			passFailTime += duration
		}

		// Save log.
		var prefix string
		var logIdx *int
		switch kind {
		default:
			panic("bad kind")
		case ResultPass:
			prefix, logIdx = ".pass-", &logIdxPass
		case ResultFail, ResultTimeout:
			prefix, logIdx = "", &logIdxFail
		case ResultFlake:
			prefix, logIdx = "flake-", &logIdxFlake
		}
		path, err := saveLog(s.OutDir, prefix, logIdx, logPath)
		if err != nil {
			log.Printf("error saving log: %s", err)
			fatal = true
			break
		}

		// Show failures.
		if kind != ResultPass {
			printTail(reporter, output)
			fmt.Fprintf(reporter, "full output written to %s\n", path)
		}

		// Check if we're done.
		if totalRuns >= s.MaxTotalRuns ||
			counts[ResultPass]+counts[ResultFail] >= s.MaxRuns ||
			counts[ResultPass] >= s.MaxPasses ||
			counts[ResultFail] >= s.MaxFails {
			break
		}

		// Start another process.
		start <- startRun{id}
		activeStartTimes[id] = time.Now()
		id++
	}
	updateStatus()
	reporter.StopStatus()

	// Shut down runners. This will kill the subprocesses.
	fmt.Fprintf(reporter, "stopping processes...\n")
	close(start)
	close(stop)
	wg.Wait()

	if fatal {
		// There was something wrong with the command. Don't
		// treat this as a success or a failure.
		return ResultFlake
	} else if counts[ResultFail] > 0 {
		// If there were any failures, exit with failure.
		return ResultFail
	} else if counts[ResultPass] > 0 {
		// If there were no failures and only successes, exit
		// with success.
		return ResultPass
	} else {
		// If there were no failures or passes, then they were
		// all timeouts or flakes.
		return ResultFlake
	}
}

func (s *Stress) runner(start <-chan startRun, stop <-chan struct{}, results chan<- result) {
	for tok := range start {
		if !s.run1(tok, stop, results) {
			return
		}
	}
}

func (s *Stress) run1(tok startRun, stop <-chan struct{}, results chan<- result) bool {
	// Open a hidden file to stream in-progress output to
	// so the user can see it.
	name := path.Join(s.OutDir, fmt.Sprintf(".run-%06d", tok.id))
	f, err := os.Create(name)
	if err != nil {
		results <- result{tok.id, nil, nil, err}
		return true
	}
	deleteFile := true
	defer func() {
		if deleteFile {
			f.Close()
			os.Remove(name)
		}
	}()

	// Start command.
	cmd, err := StartCommand(s.Command, f)
	if err != nil {
		// TODO(test): Run command that doesn't exist.
		results <- result{id: tok.id, err: err}
		return true
	}

	// Wait for cancellation, timeout, or completion.
	timeout := time.NewTimer(s.Timeout)
	select {
	case <-stop:
		cmd.Kill()
		// Stop the runner loop
		return false

	case <-timeout.C:
		cmd.Kill()
		<-cmd.Done()
		fmt.Fprintf(f, "timeout after %s\n", s.Timeout)
		deleteFile = false
		results <- result{id: tok.id, output: f}

	case <-cmd.Done():
		if !cmd.Status.Success() {
			fmt.Fprintf(f, "exited: %s\n", formatProcessState(cmd.Status))
		}
		deleteFile = false
		results <- result{id: tok.id, output: f, status: cmd.Status}
	}
	timeout.Stop()
	return true
}

func saveLog(outDir, prefix string, idx *int, oldName string) (string, error) {
	var name string
	for {
		name = path.Join(outDir, fmt.Sprintf("%s%06d", prefix, *idx))
		*idx++
		err := os.Link(oldName, name)
		if err == nil {
			// Found a name.
			break
		} else if !os.IsExist(err) {
			return "", err
		}
		// Name already exists. Try the next index.
	}

	// Delete the old name.
	os.Remove(oldName)
	return name, nil
}

func printTail(w io.Writer, data []byte) {
	const maxLines = 10
	const maxRunes = maxLines * 100

	// Ensure data ends with a \n if there are any lines.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data[:len(data):len(data)], '\n')
	}

	pos := len(data)
	lastNL := len(data)
	lineCount := -1
	runeCount := 0
	for pos > 0 {
		// Find beginning of the next line.
		bol := bytes.LastIndexByte(data[:lastNL], '\n') + 1

		// Would this line push us over either limit?
		runeCount += utf8.RuneCount(data[bol:lastNL])
		if runeCount > maxRunes {
			break
		}

		// Include the line.
		pos = bol
		lastNL = pos - 1
		lineCount++
		if lineCount >= maxLines {
			break
		}
	}

	w.Write(data[pos:])
}

func formatProcessState(state *os.ProcessState) string {
	// While this is syscall-specific, in practice all supported
	// OSes have a WaitStatus with the same interface (though
	// different representations).
	s := state.Sys().(syscall.WaitStatus)
	switch {
	case s.Exited():
		return fmt.Sprintf("status %d", s.ExitStatus())
	case s.Signaled():
		extra := ""
		if s.CoreDump() {
			extra = " (dumped core)"
		}
		return fmt.Sprintf("signal %s%s", s.Signal(), extra)
	default:
		return fmt.Sprintf("unknown wait status %v", s)
	}
}
