// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package main

import (
	"os"
	"runtime"
	"syscall"
)

func getMaxRSS(ps *os.ProcessState) (bytes uint64, ok bool) {
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok {
		return 0, false
	}

	var rssToBytes uint64
	switch runtime.GOOS {
	default:
		return 0, false
	case "aix", "android", "dragonfly", "freebsd", "linux", "netbsd", "openbsd":
		rssToBytes = 1 << 10
	case "darwin", "ios":
		rssToBytes = 1
	case "illumos", "solaris":
		rssToBytes = uint64(syscall.Getpagesize())
	}
	return uint64(ru.Maxrss) * rssToBytes, true
}
