// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

func TestStdoutExitRace(t *testing.T) {
	// The stdout pipe is asynchronous with exiting, so even if a
	// child cleanly writes to stdout, then exits, wait may return
	// before we're done reading from the pipe. Check that we
	// handle this correctly.

	for i := 0; i < 1000; i++ {
		cmd, err := StartCommand([]string{"/bin/echo", "hi"})
		if err != nil {
			t.Fatal(err)
		}
		<-cmd.Done()
		if !cmd.Status.Success() {
			t.Fatal("command failed: ", cmd.Status)
		}
		if got, want := string(cmd.Output), "hi\n"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
