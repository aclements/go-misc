// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"math"
	"os"
	"time"

	"golang.org/x/crypto/ssh/terminal"
)

type StatusReporter struct {
	update chan<- statusUpdate
	done   chan bool
}

type statusUpdate struct {
	progress float64
	message  string
}

func NewStatusReporter() *StatusReporter {
	if os.Getenv("TERM") == "dumb" || !terminal.IsTerminal(1) {
		return &StatusReporter{}
	}
	update := make(chan statusUpdate)
	sr := &StatusReporter{update: update}
	go sr.loop(update)
	return sr
}

func (sr *StatusReporter) Progress(msg string, frac float64) {
	if sr.update != nil {
		sr.update <- statusUpdate{message: msg, progress: frac}
	}
}

func (sr *StatusReporter) Message(msg string) {
	if sr.update == nil {
		fmt.Println(msg)
	} else {
		sr.update <- statusUpdate{message: msg, progress: -1}
	}
}

func (sr *StatusReporter) Stop() {
	if sr.update != nil {
		sr.done = make(chan bool)
		close(sr.update)
		<-sr.done
		sr.update = nil
	}
}

func (sr *StatusReporter) loop(updates <-chan statusUpdate) {
	const resetLine = "\r\x1b[2K"
	const wrapOff = "\x1b[?7l"
	const wrapOn = "\x1b[?7h"

	tick := time.NewTicker(time.Second / 4)
	defer tick.Stop()

	var end time.Time
	t0 := time.Now()

	var times, progress, weights []float64
	var msg string
	for {
		select {
		case update, ok := <-updates:
			if !ok {
				fmt.Print(resetLine)
				close(sr.done)
				return
			}
			if update.progress == -1 {
				fmt.Print(resetLine)
				fmt.Println(update.message)
				break
			}
			now := float64(time.Now().Sub(t0))
			times = append(times, float64(now))
			progress = append(progress, update.progress)
			weights = append(weights, 0)
			msg = update.message

			// Compute ETA using linear regression with
			// exponentially decaying weights.
			const halfLife = 150 * time.Second
			for i, t := range times {
				weights[i] = math.Exp(-1 / float64(halfLife) * (now - t))
			}
			params := PolynomialRegression(1, times, progress, weights)
			a, b := params[0], params[1]

			// The intercept of a + b*x - 1 is the ending
			// time.
			if b == 0 {
				end = time.Time{}
			} else {
				end = t0.Add(time.Duration((1 - a) / b))
			}

		case <-tick.C:
		}

		var eta string

		if end.IsZero() {
			eta = "unknown"
		} else {
			etaDur := end.Sub(time.Now())
			// Trim off sub-second precision.
			etaDur -= etaDur % time.Second
			if etaDur <= 0 {
				eta = "0s"
			} else {
				eta = etaDur.String()
			}
		}
		if msg == "" {
			eta = "ETA " + eta
		} else {
			eta = ", ETA " + eta
		}
		// TODO: This isn't quite right. If we hit the right
		// edge of the terminal, it won't wrap, but the
		// right-most character will be the *last* character
		// in the string, since terminal keeps overwriting it.
		fmt.Printf("%s%s%s%s%s", resetLine, wrapOff, msg, eta, wrapOn)
	}
}
