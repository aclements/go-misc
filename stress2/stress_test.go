// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"strings"
	"testing"
)

func TestPrintTail(t *testing.T) {
	check := func(t *testing.T, data, want string) {
		t.Helper()
		var got strings.Builder
		printTail(&got, []byte(data))
		if got.String() != want {
			t.Errorf("for:\n%s\ngot:\n%s\nwant:\n%s", data, got.String(), want)
		}
	}

	// Basic
	check(t, "", "")
	check(t, "a", "a\n")
	check(t, "a\nb\n", "a\nb\n")
	// Line trimming
	a20 := strings.Repeat("a\n", 20)
	check(t, a20, strings.Repeat("a\n", 10))
	check(t, a20[:len(a20)-1], strings.Repeat("a\n", 10))
	// Test rune limits.
	long := strings.Repeat("a", 2000) + "\n"
	check(t, long, "")
	long += "x\n"
	check(t, long, "x\n")
}
