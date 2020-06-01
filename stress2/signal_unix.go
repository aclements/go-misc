// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build aix darwin dragonfly freebsd js linux netbsd openbsd solaris

package main

import (
	"os"
	"syscall"
)

var exitSignals = []os.Signal{syscall.SIGHUP, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM}

// traceSignal is the signal to send a Go program to make it crash
// with a stack trace.
var traceSignal = syscall.SIGQUIT
