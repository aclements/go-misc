// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

var style = map[string]string{
	"reset": "\x1b[0m",

	"branch":       "\x1b[1;32m", // Bright green
	"symbolic-ref": "\x1b[1;36m", // Bright cyan

	// CL status styles

	"Not mailed": "\x1b[35m", // Magenta

	"Pending warn":  "\x1b[33m",   // Yellow
	"Ready warn":    "\x1b[33m",   // Yellow
	"Rejected warn": "\x1b[1;31m", // Bright red

	"Ready": "\x1b[32m", // Green

	"Submitted": "\x1b[37m",   // Gray
	"Abandoned": "\x1b[9;37m", // Gray, strike-through
	"Draft":     "\x1b[37m",   // Gray
}
