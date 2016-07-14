// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package amb

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var count int64
var progressDone chan bool

func startProgress() {
	const resetLine = "\r\x1b[2K"

	progressDone = make(chan bool)
	go func() {
		for {
			buf := fmt.Sprintf("%s%d done", resetLine, atomic.LoadInt64(&count))
			fmt.Fprint(os.Stderr, buf)
			select {
			case <-time.After(100 * time.Millisecond):
			case <-progressDone:
				fmt.Fprintf(os.Stderr, "%s%d done\n", resetLine, atomic.LoadInt64(&count))
				close(progressDone)
				return
			}
		}
	}()
}

func stopProgress() {
	progressDone <- true
	<-progressDone
}
