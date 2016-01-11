// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "testing"

func TestPickSpread(t *testing.T) {
	run.iterations = 5

	for iter := 0; iter < 10; iter++ {
		commits := []*commitInfo{}
		for i := 0; i < 100; i++ {
			commits = append(commits, &commitInfo{})
		}

		for {
			commit := pickCommitSpread(commits)
			if commit == nil {
				break
			}
			commit.count++
		}

		// Test that all of the commits ran the expected
		// number of times.
		for _, c := range commits {
			if c.count != run.iterations {
				t.Fatalf("commit picked %d times; want %d", c.count, run.iterations)
			}
		}
	}
}
