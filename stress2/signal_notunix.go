// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build plan9 windows

package main

import "os"

var exitSignals = []os.Signal{os.Interrupt}

var traceSignal = nil
