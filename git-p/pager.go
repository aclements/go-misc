// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/term"
)

// setupPager restarts this process under the git pager. If the
// process is already under a pager or doesn't want a pager, it
// returns.
//
// setupPager returns true if this process is running in a smart
// terminal (which includes running under a pager started by
// setupPager).
func setupPager() (inTerm bool) {
	// This is roughly based on pager.c:setup_pager in git, but
	// this starts ourselves as a subprocess instead of the pager.
	// Doing it this way around means we don't have to babysit the
	// pager: signals/panics kill us like normal and leave the
	// pager running and the shell waiting on the pager.

	if os.Getenv("GIT_P_PAGER_IN_USE") != "" {
		return true
	}
	if !term.IsTerminal(1) {
		return false
	}
	switch os.Getenv("TERM") {
	case "", "dumb":
		return false
	}

	pagerCmd := git("var", "GIT_PAGER")
	if pagerCmd == "" {
		return true
	}

	// Start ourselves as a subprocess.
	me, err := os.Executable()
	if err != nil {
		return true
	}
	os.Setenv("GIT_P_PAGER_IN_USE", "true")
	cmd := exec.Command(me, os.Args[1:]...)
	r, w, err := os.Pipe()
	if err != nil {
		return true
	}
	cmd.Stdin = nil
	cmd.Stdout = w
	cmd.Stderr = w
	cmd.Start()

	// Replace this process with the pager.
	w.Close()
	syscall.Dup2(int(r.Fd()), 0)
	r.Close()
	// We need -R at least to interpret color codes.
	// Add -F so single-screen output doesn't invoke paging.
	os.Setenv("LESS", "-FR "+os.Getenv("LESS"))
	if os.Getenv("LV") == "" {
		os.Setenv("LV", "-c")
	}
	err = syscall.Exec("/bin/sh", []string{"sh", "-c", pagerCmd}, os.Environ())

	// Didn't work, but there's not much we can do now. Try cat.
	syscall.Exec("/bin/cat", []string{"cat"}, os.Environ())

	// Still didn't work. Bail.
	panic(fmt.Sprintf("failed to start pager: %s", err))
}
