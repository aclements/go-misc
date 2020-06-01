// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh/terminal"
)

func NewStdoutReporter() StressReporter {
	if os.Getenv("TERM") == "" || os.Getenv("TERM") == "dumb" || !terminal.IsTerminal(syscall.Stdout) {
		return &ReporterDumb{w: os.Stdout}
	}
	return &ReporterVT100{w: os.Stdout}
}

type ReporterDumb struct {
	w io.Writer
}

func (r *ReporterDumb) StartStatus() {}
func (r *ReporterDumb) StopStatus()  {}
func (r *ReporterDumb) Status(status string) {
	fmt.Fprintf(r.w, "%s\n", status)
}
func (r *ReporterDumb) Write(data []byte) (int, error) {
	return r.w.Write(data)
}

type ReporterVT100 struct {
	w      io.Writer
	stop   chan struct{}
	update chan string
	wg     sync.WaitGroup
	mu     sync.Mutex
}

func (r *ReporterVT100) StartStatus() {
	r.stop = make(chan struct{})
	r.update = make(chan string)
	r.wg.Add(1)
	go r.run()
}

func (r *ReporterVT100) StopStatus() {
	close(r.stop)
	r.wg.Wait()
}

func (r *ReporterVT100) Status(status string) {
	r.update <- status
}

// VT100 control sequences
const (
	resetLine = "\r\x1b[2K"
	wrapOff   = "\x1b[?7l"
	moveEOL   = "\x1b[999C"
	wrapOn    = "\x1b[?7h"
)

func (r *ReporterVT100) Write(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Clear the status line.
	fmt.Fprintf(r.w, "%s%s", resetLine, wrapOn)
	return r.w.Write(data)
}

func (r *ReporterVT100) run() {
	const ticker = "-\\|/"

	i := 0
	status := ""
	tick := time.NewTicker(time.Second / 2)
	defer func() {
		tick.Stop()

		// Keep the last status line.
		r.mu.Lock()
		fmt.Fprintf(r.w, "%s%s%s%s\n", resetLine, wrapOff, status, wrapOn)
		r.mu.Unlock()

		r.wg.Done()
	}()

	for {
		// Print the status line plus a ticker.
		r.mu.Lock()
		fmt.Fprintf(r.w, "%s%s%s%s%c", resetLine, wrapOff, status, moveEOL, ticker[i%len(ticker)])
		r.mu.Unlock()

		select {
		case <-tick.C:
			i++

		case status = <-r.update:

		case <-r.stop:
			return
		}
	}
}
