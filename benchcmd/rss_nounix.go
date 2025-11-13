// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package main

import "os"

func getMaxRSS(ps *os.ProcessState) (bytes uint64, ok bool) {
	return 0, false
}
