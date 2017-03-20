// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type commitInfo struct {
	hash         string
	commitDate   time.Time
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
func getCommits(revRange []string, logPath string) []*commitInfo {
	// Get commit sequence.
	hashes := lines(git("rev-list", append([]string{"--no-walk", "--"}, revRange...)...))
	commits := make([]*commitInfo, len(hashes))
	commitMap := make(map[string]*commitInfo)
	for i, hash := range hashes {
		commits[i] = &commitInfo{
			hash:    hash,
			logPath: logPath,
		}
		commitMap[hash] = commits[i]
	}

	// Get commit dates.
	//
	// TODO: This can produce a huge command line.
	args := append([]string{"-s", "--format=format:%cI"}, hashes...)
	dates := lines(git("show", args...))
	for i := range commits {
		d, err := time.Parse(time.RFC3339, dates[i])
		if err != nil {
			log.Fatalf("cannot parse commit date: %v", err)
		}
		commits[i].commitDate = d
	}

	// Get gover-cached builds. It's okay if this fails.
	if fis, err := ioutil.ReadDir(goverDir()); err == nil {
		for _, fi := range fis {
			if ci := commitMap[fi.Name()]; ci != nil && fi.IsDir() {
				ci.gover = true
			}
		}
	}

	// Load current benchmark state.
	logf, err := os.Open(logPath)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Fatalf("opening %s: %v", logPath, err)
		}
	} else {
		defer logf.Close()
		parseLog(commitMap, logf)
	}

	return commits
}

// goverDir returns the directory containing gover-cached builds.
func goverDir() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home := os.Getenv("HOME")
		if home == "" {
			u, err := user.Current()
			if err != nil {
				home = u.HomeDir
			}
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "gover")
}

// parseLog parses benchmark runs and failures from r and updates
// commits in commitMap.
func parseLog(commitMap map[string]*commitInfo, r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		b := scanner.Bytes()
		switch {
		case bytes.HasPrefix(b, []byte("commit: ")):
			hash := scanner.Text()[len("commit: "):]
			if ci := commitMap[hash]; ci != nil {
				ci.count++
			}

		case bytes.HasPrefix(b, []byte("# FAILED at ")):
			hash := scanner.Text()[len("# FAILED at "):]
			if ci := commitMap[hash]; ci != nil {
				ci.fails++
			}

		case bytes.HasPrefix(b, []byte("# BUILD FAILED at ")):
			hash := scanner.Text()[len("# BUILD FAILED at "):]
			if ci := commitMap[hash]; ci != nil {
				ci.buildFailed = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		log.Fatal("parsing benchmark log: ", err)
	}
}

// binPath returns the file name of the binary for this commit.
func (c *commitInfo) binPath() string {
	// TODO: This assumes the short commit hash is unique.
	return fmt.Sprintf("bench.%s", c.hash[:7])
}

// failed returns whether commit c has failed and should not be run
// any more.
func (c *commitInfo) failed() bool {
	return c.buildFailed || c.fails >= maxFails
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

var commitRe = regexp.MustCompile(`^commit: |^# FAILED|^# BUILD FAILED`)

// cleanLog escapes lines in l that may confuse the log parser and
// makes sure l is newline terminated.
func cleanLog(l string) string {
	l = commitRe.ReplaceAllString(l, "# $0")
	if !strings.HasSuffix(l, "\n") {
		l += "\n"
	}
	return l
}

// logRun updates c with a successful run.
func (c *commitInfo) logRun(out string) {
	c.writeLog(fmt.Sprintf("commit: %s\n\n%s\n", c.hash, cleanLog(out)))
	c.count++
}

// logFailed updates c with a failed run. If buildFailed is true, this
// is considered a permanent failure and buildFailed is set.
func (c *commitInfo) logFailed(buildFailed bool, out string) {
	typ := "FAILED"
	if buildFailed {
		typ = "BUILD FAILED"
	}
	c.writeLog(fmt.Sprintf("# %s at %s\n# %s\n", typ, c.hash, strings.Replace(cleanLog(out), "\n", "\n# ", -1)))
	if buildFailed {
		c.buildFailed = true
	} else {
		c.fails++
	}
}

// writeLog appends msg to c's log file. The caller is responsible for
// properly formatting it.
func (c *commitInfo) writeLog(msg string) {
	logFile, err := os.OpenFile(c.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Fatalf("opening %s: %v", c.logPath, err)
	}
	if _, err := logFile.WriteString(msg); err != nil {
		log.Fatalf("writing to %s: %v", c.logPath, err)
	}
	if err := logFile.Close(); err != nil {
		log.Fatalf("closing %s: %v", c.logPath, err)
	}
}
