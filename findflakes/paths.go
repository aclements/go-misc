// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
)

func readPaths(r io.Reader) ([]string, error) {
	out := []string{}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		out = append(out, filepath.Join(*flagRevDir, scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func pathFailures(revs []*Revision, paths []string) []*failure {
	pathSet := make(map[string]bool, len(paths))
	for _, path := range paths {
		pathSet[path] = true
	}

	failures := []*failure{}
	for t, rev := range revs {
		for _, build := range rev.Builds {
			path := build.LogPath()
			if pathSet[path] {
				// TODO: Fill OS/Arch.
				failures = append(failures, &failure{
					T:          t,
					CommitsAgo: len(revs) - t - 1,
					Rev:        rev,
					Build:      build,
				})
			}
		}
	}
	return failures
}
