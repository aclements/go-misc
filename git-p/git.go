// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// git runs git with args and returns its output.
func git(args ...string) string {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "%s\n", string(err.Stderr))
		}
		log.Fatalf("git %s failed: %s", shellEscapeList(args), err)
	}
	return strings.TrimSuffix(string(out), "\n")
}

// tryGit runs git with args and returns its output and a non-nil
// error if the command exits with a non-zero status.
func tryGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	out, err := cmd.CombinedOutput()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		log.Fatalf("git %s failed: %s", shellEscapeList(args), err)
	}
	return strings.TrimSuffix(string(out), "\n"), err
}

func lines(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// getRemote returns the remote name for the given remote URL.
func getRemote(url string) (string, error) {
	for _, line := range lines(git("remote", "-v")) {
		fs := strings.Fields(line)
		if len(fs) >= 2 && fs[1] == url {
			return fs[0], nil
		}
	}
	return "", fmt.Errorf("no remote found for %s", url)
}

// upstreamOf returns the full upstream ref name of the given ref, or
// "".
func upstreamOf(ref string) string {
	// This fails with code 128 and "fatal: no upstream configured
	// for branch 'xxx'" if there's no upstream. It also fails
	// with 128 and "fatal: HEAD does not point to a branch" if
	// ref is not a branch or a symbolic ref to a branch.
	out, err := tryGit("rev-parse", "--symbolic-full-name", ref+"@{u}")
	if err != nil {
		return ""
	}
	return out
}

// changeIds returns the full Gerrit change IDs of each commit. The
// change ID will be "" if missing.
func changeIds(project, forBranch string, commits []string) []string {
	if i := strings.LastIndexByte(forBranch, '/'); i >= 0 {
		forBranch = forBranch[i+1:]
	}

	// Construct input.
	var input bytes.Buffer
	for _, c := range commits {
		fmt.Fprintf(&input, "%s\n", c)
	}

	// Run batch cat-file command.
	args := []string{"cat-file", "--batch", "--buffer"}
	cmd := exec.Command("git", args...)
	cmd.Stdin = &input
	out, err := cmd.Output()
	if err != nil {
		if err, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "%s\n", string(err.Stderr))
		}
		log.Fatalf("git %s failed: %s", shellEscapeList(args), err)
	}

	// Parse output.
	cids := make([]string, len(commits))
	for i, commit := range commits {
		// Get "<sha1> SP <type> SP <size> LF" line.
		nl := bytes.IndexByte(out, '\n')
		if nl < 0 {
			log.Fatal("malformed git cat-file output")
		}
		fs := strings.Fields(string(out[:nl]))
		out = out[nl+1:]
		if len(fs) < 2 || fs[0] != commit {
			log.Fatal("malformed git cat-file output")
		}
		if fs[1] == "missing" {
			continue
		}
		if fs[1] != "commit" {
			log.Fatal("unexpected object type %q for %s", fs[1], fs[0])
		}

		// Get commit object.
		size, _ := strconv.Atoi(fs[2])
		if len(out) <= size || out[size] != '\n' {
			log.Fatal("git cat-file out of sync")
		}
		var obj []byte
		obj, out = out[:size], out[size+1:]

		// Find the Change-Id in the commit.
		for _, line := range bytes.Split(obj, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("Change-Id: ")) {
				lfs := bytes.Fields(line)
				if len(lfs) == 2 {
					cid := string(lfs[1])
					cids[i] = project + "~" + forBranch + "~" + cid
				}
			}
		}
	}
	return cids
}
