// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command git-p prints the status of pending commits on all branches.
//
// git-p summarizes the status of each commit, including its review
// state in Gerrit and whether or not there are any comments or TryBot
// failures.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// TODO: Provide a way to exclude branches (like archive/, etc)

// TODO: Do the right thing if the terminal is dumb.

const (
	// TODO: Support other repos.
	remoteUrl = "https://go.googlesource.com/go"
	project   = "go"
	gerritUrl = "https://go-review.googlesource.com"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [branches...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "With no arguments, list branches from newest to oldest.\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	branches := flag.Args()

	// Check the branch names.
	for _, b := range branches {
		if out, err := tryGit("rev-parse", b, "--"); err != nil {
			fmt.Printf("%s\n", out)
			os.Exit(1)
		}
	}

	setupPager()

	// Find the Gerrit remote name.
	remote, err := getRemote(remoteUrl)
	if err != nil {
		log.Fatal(err)
	}

	// Get commits that are available from the Gerrit remote.
	upstreams := lines(git("for-each-ref", "--format", "%(objectname)", "refs/remotes/"+remote+"/"))
	if len(upstreams) == 0 {
		log.Fatalf("no refs for remote %s", remote)
	}

	gerrit := NewGerrit(gerritUrl)

	// Pass a token through each showBranch so we can pipeline
	// fetching branch information, while displaying it in order.
	token := make(chan struct{}, 1)
	token <- struct{}{}
	// But if the output of showBranch is blocked (e.g., by
	// back-pressure from a pager), don't start new showBranches.
	// This avoids making lots of ultimately ignored requests to
	// Gerrit.
	limit := make(chan struct{}, 3)

	var head string
	if len(branches) == 0 {
		// Resolve HEAD and show it first regardless of age.
		head, _ = tryGit("symbolic-ref", "HEAD")
		if head != "" {
			token = showBranch(gerrit, head, "HEAD", remote, upstreams, token, limit)
		}

		// Get all local branches, sorted by most recent commit date.
		branches = lines(git("for-each-ref", "--format", "%(refname)", "--sort", "-committerdate", "refs/heads/"))
	}

	// Show all branches.
	for _, branch := range branches {
		if branch == head {
			continue
		}
		token = showBranch(gerrit, branch, "", remote, upstreams, token, limit)
	}

	<-token
}

func showBranch(gerrit *Gerrit, branch, extra string, remote string, upstreams []string, token, limit chan struct{}) chan struct{} {
	// Don't start too many showBranches.
	limit <- struct{}{}

	// Get the Gerrit upstream name so we can construct full
	// Change-IDs.
	upstream := upstreamOf(branch)
	if upstream == "" {
		upstream = "refs/" + remote + "/master"
	}

	// Get commits from the branch to any upstream.
	args := []string{"rev-list", branch}
	for _, u := range upstreams {
		args = append(args, "^"+u)
	}
	args = append(args, "--")
	commits := lines(git(args...))

	// Get Change-Ids from these commits.
	cids := changeIds(project, upstream, commits)

	// Fetch information on all of these changes.
	//
	// We need DETAILED_LABELS to get numeric values of labels.
	changes := make([]*GerritChanges, len(cids))
	for i, cid := range cids {
		// TODO: Would this be simpler with a single big OR query?
		if cid != "" {
			changes[i] = gerrit.QueryChanges("change:"+cid, printChangeOptions...)
		}
	}

	if len(changes) == 0 {
		<-limit
		return token
	}

	done := make(chan struct{})
	go func() {
		<-token
		// Print changes.
		fmt.Printf("\x1b[1;32m%s\x1b[0m", strings.TrimPrefix(branch, "refs/heads/"))
		if extra != "" {
			fmt.Printf(" (\x1b[1;36m%s\x1b[0m)", extra)
		}
		fmt.Printf("\n")
		for i, change := range changes {
			printChange(commits[i], change)
		}
		fmt.Println()
		<-limit
		done <- struct{}{}
	}()
	return done
}

var labelMsg = regexp.MustCompile(`^Patch Set [0-9]+: [-a-zA-Z]+\+[0-9]$`)
var trybotFailures = regexp.MustCompile(`(?m)^Failed on ([^:]+):`)

func changeStatus(commit string, info *GerritChange) (status string, warnings []string) {
	switch info.Status {
	default:
		return fmt.Sprintf("Unknown status %q", info.Status), nil
	case "MERGED":
		return "Submitted", nil
	case "ABANDONED":
		return "Abandoned", nil
	case "DRAFT":
		return "Draft", nil
	case "NEW":
	}

	// Check for warnings on current PS. (Requires
	// CURRENT_REVISION or ALL_REVISIONS option.)
	curPatchSet := info.Revisions[info.CurrentRevision].Number
	// Are there unmailed changes?
	if info.CurrentRevision != commit {
		// How serious are the differences with the mailed changes?
		pid1, err1 := gitPatchID(info.CurrentRevision)
		pid2, err2 := gitPatchID(commit)
		if !(err1 == nil && err2 == nil && pid1 == pid2) {
			// The patches are different.
			warnings = append(warnings, "Local commit differs from mailed commit")
		} else {
			msg1, err1 := gitCommitMessage(info.CurrentRevision)
			msg2, err2 := gitCommitMessage(commit)
			if !(err1 == nil && err2 == nil && msg1 == msg2) {
				// Patches are the same, but the
				// commit message has changed.
				warnings = append(warnings, "Local commit message differs")
			}
		}
	}
	// Are there rejections?
	rejected := false
	for labelName, label := range info.Labels {
		if !label.Optional && label.Rejected != nil {
			if labelName == "Do-Not-Submit" {
				warnings = append(warnings, "Marked \"Do not submit\"")
			} else {
				warnings = append(warnings, fmt.Sprintf("Rejected by %s", label.Rejected.Name))
				rejected = true
			}
		}
	}
	// Are there comments on the latest PS? (Requires
	// MESSAGES option.)
	nComments := 0
	commentUsers, commentUsersSet := []string{}, map[string]bool{}
	for _, msg := range info.Messages {
		if msg.PatchSet != curPatchSet {
			continue
		}
		// Ignore automated comments.
		if strings.HasPrefix(msg.Tag, "autogenerated:gerrit:") {
			continue
		}
		// Ignore label-only messages (ugh, why aren't these
		// better marked?)
		if labelMsg.MatchString(msg.Message) {
			continue
		}
		// Ignore TryBot comments (Requires
		// DETAILED_ACCOUNTS option.)
		if msg.Author.Email == "gobot@golang.org" {
			continue
		}
		nComments++
		if !commentUsersSet[msg.Author.Name] {
			commentUsersSet[msg.Author.Name] = true
			commentUsers = append(commentUsers, msg.Author.Name)
		}
	}
	if nComments > 0 {
		msg := "1 comment"
		if nComments > 1 {
			msg = fmt.Sprintf("%d comments", nComments)
		}
		msg += " on latest PS from " + strings.Join(commentUsers, ", ")
		warnings = append(warnings, msg)
	}
	// Are the trybots unhappy? (Requires LABELS option.)
	if tbr := info.Labels["TryBot-Result"]; tbr != nil && tbr.Rejected != nil {
		// Get the failed configs. (Requires MESSAGES option.)
		configs := []string{}
		for _, msg := range info.Messages {
			// Requires DETAILED_ACCOUNTS option.
			if msg.PatchSet != curPatchSet || msg.Author.Email != "gobot@golang.org" {
				continue
			}
			for _, f := range trybotFailures.FindAllStringSubmatch(msg.Message, -1) {
				configs = append(configs, f[1])
			}
		}
		if len(configs) == 0 {
			warnings = append(warnings, "TryBots failed")
		} else {
			warnings = append(warnings, "TryBots failed on "+strings.Join(configs, ", "))
		}
	} else if tbr == nil || tbr.Approved == nil {
		warnings = append(warnings, "TryBots not run")
	}

	// Submittable? (Requires SUBMITTABLE option.)
	status = "Pending"
	if rejected {
		status = "Rejected"
	} else if info.Submittable {
		status = "Ready"
	}

	return status, warnings
}

var printChangeOptions = []string{"SUBMITTABLE", "LABELS", "CURRENT_REVISION", "MESSAGES", "DETAILED_ACCOUNTS"}

var display = map[string]string{
	"Not mailed": "\x1b[35m", // Magenta

	"Pending warn":  "\x1b[33m",   // Yellow
	"Ready warn":    "\x1b[33m",   // Yellow
	"Rejected warn": "\x1b[1;31m", // Bright red

	"Ready": "\x1b[32m", // Green

	"Submitted": "\x1b[37m",   // Gray
	"Abandoned": "\x1b[9;37m", // Gray, strike-through
	"Draft":     "\x1b[37m",   // Gray
}

// printChange prints a summary of change's status and warnings.
//
// change must be retrieved with options printChangeOptions.
func printChange(commit string, change *GerritChanges) {
	logMsg := git("log", "-n1", "--oneline", commit)

	status, warnings, link := "Not mailed", []string(nil), ""
	if change != nil {
		results, err := change.Wait()
		if err != nil {
			log.Fatal(err)
		}
		if len(results) > 1 {
			log.Fatal("multiple changes found for commit %s", commit)
		}
		if len(results) == 1 {
			status, warnings = changeStatus(commit, results[0])
			//link = fmt.Sprintf("[%s/c/%d]", gerritUrl, results[0].Number)
			link = fmt.Sprintf(" [golang.org/cl/%d]", results[0].Number)
		}
	}

	var control, eControl string
	if len(warnings) != 0 {
		if c, ok := display[status+" warn"]; ok {
			control = c
		}
	}
	if control == "" {
		if c, ok := display[status]; ok {
			control = c
		}
	}
	if control != "" {
		eControl = "\x1b[0m"
	}

	hdr := fmt.Sprintf("%-10s %s", status, logMsg)
	hdrMax := 80 - len(link) - 2
	if utf8.RuneCountInString(hdr) > hdrMax {
		hdr = fmt.Sprintf("%*.*s…", hdrMax-1, hdrMax-1, hdr)
	}
	fmt.Printf("  %s%-*s%s%s\n", control, hdrMax, hdr, eControl, link)
	for _, w := range warnings {
		fmt.Printf("    %s\n", w)
	}
}
