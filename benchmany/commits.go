// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// outDir is the directory containing benchmark binaries and logs.
var outDir = "."

type commitInfo struct {
	hash         string
	commitDate   time.Time
	binPath      string
	gover        bool
	logPath      string
	count, fails int
	buildFailed  bool
}

// getCommits returns the commit info for all of the revisions in the
// given git revision range, where the revision range is spelled as
// documented in gitrevisions(7). Commits are returned in reverse
// chronological order, most recent commit first (the same as
// git-rev-list(1)).
func getCommits(revRange []string) []*commitInfo {
	// Get commit sequence.
	hashes := lines(git("rev-list", append([]string{"--no-walk"}, revRange...)...))

	// Get commit dates.
	args := append([]string{"-s", "--format=format:%cI"}, hashes...)
	dates := lines(git("show", args...))

	// Get gover-cached builds. It's okay if this fails.
	cachedHashes := make(map[string]bool)
	x, _ := exec.Command("gover", "list").CombinedOutput()
	for _, cached := range lines(string(x)) {
		fs := strings.SplitN(cached, " ", 2)
		cachedHashes[fs[0]] = true
	}

	// Load current benchmark state.
	var commits []*commitInfo
	for i, hash := range hashes {
		logPath := filepath.Join(outDir, fmt.Sprintf("log.%s", hash[:7]))
		count, fails, buildFailed := countRuns(logPath)
		commitDate, err := time.Parse(time.RFC3339, dates[i])
		if err != nil {
			log.Fatalf("cannot parse commit date: %v", err)
		}
		// TODO: This assumes the 7 character hash is
		// unambiguous.
		commit := &commitInfo{
			hash:        hash,
			commitDate:  commitDate,
			binPath:     filepath.Join(outDir, fmt.Sprintf("bench.%s", hash[:7])),
			gover:       cachedHashes[hash[:7]],
			logPath:     logPath,
			count:       count,
			fails:       fails,
			buildFailed: buildFailed,
		}
		commits = append(commits, commit)
	}

	return commits
}

// countRuns parses the log at path and returns the number of
// successful runs, the number of failed runs, and whether the build
// failed.
func countRuns(path string) (count, fails int, buildFailed bool) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return
	} else if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	inXMetrics := false
	for scanner.Scan() {
		t := scanner.Text()
		if t == "PASS" {
			count++
		} else if t == "FAILED:" {
			fails++
		} else if t == "BUILD FAILED:" {
			buildFailed = true
		}
		if strings.HasPrefix(t, "GOPERF-METRIC:") {
			if !inXMetrics {
				count++
			}
			inXMetrics = true
		} else {
			inXMetrics = false
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "reading log %s: %v", path, err)
		os.Exit(1)
	}
	return
}

// runnable returns whether commit c needs to be benchmarked at least
// one more time.
func (c *commitInfo) runnable() bool {
	return !c.buildFailed && c.fails < maxFails && c.count < run.iterations
}

// partial returns true if this commit is both runnable and already
// has some runs.
func (c *commitInfo) partial() bool {
	return c.count > 0 && c.runnable()
}

// writeLog appends msg to c's log file.
func (c *commitInfo) writeLog(msg []byte) {
	logFile, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := logFile.Write(msg); err != nil {
		log.Fatal(err)
	}
	if err := logFile.Close(); err != nil {
		log.Fatal(err)
	}
}
