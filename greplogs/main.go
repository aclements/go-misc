// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command greplogs is deprecated.
//
// Please see golang.org/x/build/cmd/greplogs.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintf(os.Stderr, "This copy of greplogs is deprecated. Please update your greplogs using:\n")
	fmt.Fprintf(os.Stderr, "\tgo install golang.org/x/build/cmd/greplogs@latest\n")
	os.Exit(2)
}
