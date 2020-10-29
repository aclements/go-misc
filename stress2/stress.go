// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
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
	output []byte
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

func (s *Stress) resultKind(res result) ResultKind {
	switch {
	case res.status == nil:
		return ResultTimeout
	case s.PassRe == nil && res.status.Success(),
		s.PassRe != nil && s.PassRe.Match(res.output):
		return ResultPass
	case s.FailRe == nil && res.status.ExitCode() != 125,
		s.FailRe != nil && s.FailRe.Match(res.output):
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
	outIdx := 0
	counts := make(map[ResultKind]int)
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

		// Classify the result.
		kind := s.resultKind(res)
		totalRuns++
		counts[kind]++

		// Update time stats.
		duration := time.Since(activeStartTimes[res.id])
		delete(activeStartTimes, res.id)
		if kind == ResultPass || kind == ResultFail {
			passFailTime += duration
		}

		// Save failure logs.
		//
		// TODO: Do we want to save success logs, too?
		// Especially if you're not entirely sure you're
		// testing the right thing, it can be valuable to look
		// at these, and there can be patterns in the
		// successes that are just as useful as patterns in
		// failures. Maybe they should be deduped?
		if kind != ResultPass {
			out := res.output
			if len(out) > 0 && out[len(out)-1] != '\n' {
				out = append(out, '\n')
			}
			if kind == ResultTimeout {
				out = append(out, []byte("timeout\n")...)
			} else {
				msg := fmt.Sprintf("exited: %s\n", formatProcessState(res.status))
				out = append(out, []byte(msg)...)
			}

			printTail(reporter, out)

			path, err := saveLog(s.OutDir, &outIdx, out)
			if err != nil {
				log.Printf("error saving log: %s", err)
				fatal = true
				break
			}
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
		// TODO: Stream output to a hidden file in s.OutDir so
		// it's possible to see.
		cmd, err := StartCommand(s.Command)
		if err != nil {
			// TODO(test): Run command that doesn't exist.
			results <- result{tok.id, nil, nil, err}
			continue
		}

		// Wait for cancellation, timeout, or completion.
		timeout := time.NewTimer(s.Timeout)
		select {
		case <-stop:
			cmd.Kill()
			return

		case <-timeout.C:
			cmd.Kill()
			<-cmd.Done()
			results <- result{tok.id, cmd.Output, nil, nil}

		case <-cmd.Done():
			results <- result{tok.id, cmd.Output, cmd.Status, nil}
		}
		timeout.Stop()
	}
}

func saveLog(outDir string, idx *int, data []byte) (string, error) {
	var name string
	var f *os.File
	for {
		var err error
		name = path.Join(outDir, fmt.Sprintf("%06d", *idx))
		*idx++
		f, err = os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
		if os.IsExist(err) {
			// Try the next file name.
			continue
		}
		if err != nil {
			return "", err
		}
		break
	}

	_, err := f.Write(data)
	if err == nil {
		err = f.Close()
	}
	if err != nil {
		f.Close()
		os.Remove(name)
		return "", err
	}
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
