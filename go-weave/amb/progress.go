// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var count int64

var progress struct {
	printLock sync.Mutex
	stop      chan struct{}
	done      chan struct{}
}

const resetLine = "\r\x1b[2K"

func startProgress() {
	progress.stop = make(chan struct{})
	progress.done = make(chan struct{})

	go func() {
		// Redirect process stdout and stderr.
		//
		// Alternatively, we could dup our pipes over stdout
		// and stderr, but then we're in the way of any
		// runtime debug output.
		origStdout, origStderr := os.Stdout, os.Stderr
		newStdoutR, newStdoutW, err := os.Pipe()
		if err != nil {
			log.Fatalf("failed to create stdout self-pipe: %v", err)
		}
		newStderrR, newStderrW, err := os.Pipe()
		if err != nil {
			log.Fatalf("failed to create stderr self-pipe: %v", err)
		}

		defer func() {
			os.Stdout, os.Stderr = origStdout, origStderr
			// Stop the feeder. It will close the write sides.
			newStdoutR.Close()
			newStderrR.Close()
		}()
		os.Stdout, os.Stderr = newStdoutW, newStderrW
		go pipeFeeder(newStdoutR, origStdout, origStdout)
		go pipeFeeder(newStderrR, origStderr, origStderr)

		report := func(final bool) {
			progress.printLock.Lock()
			fmt.Fprintf(origStderr, "%s%d done", resetLine, atomic.LoadInt64(&count))
			if final {
				fmt.Fprintf(origStderr, "\n")
			}
			progress.printLock.Unlock()
		}
		ticker := time.NewTicker(100 * time.Millisecond)
	loop:
		for {
			report(false)

			select {
			case <-ticker.C:
			case <-progress.stop:
				report(true)
				break loop
			}
		}
		ticker.Stop()
		close(progress.done)
	}()
}

func pipeFeeder(r, w, pstream *os.File) {
	var buf [256]byte
	bol := true
	for {
		n, err := r.Read(buf[:])
		if n == 0 {
			break
		}
		if bol {
			bol = false
			// Stop progress printing.
			progress.printLock.Lock()
			// Clear the progress line.
			pstream.WriteString(resetLine)
		}
		// Print this message.
		if n, err = w.Write(buf[:n]); err != nil {
			panic(err)
		}
		if bytes.HasSuffix(buf[:n], []byte("\n")) {
			// Resume progress printing.
			progress.printLock.Unlock()
			bol = true
		}
	}
	w.Close()
}

func stopProgress() {
	close(progress.stop)
	<-progress.done
}
