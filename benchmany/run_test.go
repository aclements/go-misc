// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"math/rand"
	"testing"
)

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

			if rand.Intn(50) == 0 {
				commit.buildFailed = true
			} else if rand.Intn(50) == 1 {
				commit.fails++
			} else {
				commit.count++
			}
		}

		// Test that all of the commits ran the expected
		// number of times.
		for _, c := range commits {
			if c.runnable() {
				t.Fatalf("commit still runnable %+v", c)
			}
		}
	}
}
