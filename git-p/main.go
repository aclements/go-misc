// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command git-p prints the status of pending commits on all branches.
//
// git-p summarizes the status of each commit on every branch,
// starting with HEAD and then the most recently committed-to branch.
//
// git-p shows the Gerrit status of each commit and performs several
// status checks:
//
// * It checks if there are any local changes that haven't been mailed
// (and is sensitive to rebases, so it won't complain if the diff
// hasn't changed).
//
// * It checks if there are any rejections (-1 or -2), or if the CL is
// marked "Do not submit".
//
// * It checks if there are any comments on the latest version of the
// CL, which may indicate it needs changes even if it is submittable.
//
// * It checks if the trybots are sad or weren't run.
//
// The output is color-coded by status: green indicates a CL is
// submittable and has no warnings, yellow indicates a CL has
// warnings, and red indicates a CL has been rejected. Submitted CLs
// are greyed out.
//
// git-p uses the git pager if one is configured.
//
// Currently git-p only supports the main Go repository.
//
// Example output
//
//  $ git-p gc-free-wbufs-v3
//  gc-free-wbufs-v3 for master
//    Not mailed c1e17d722f fixup! runtime: allocate GC workbufs from manually-…
//    Pending    326537d00c runtime: free workbufs during… [golang.org/cl/38582]
//      Local commit message differs
//      1 comment on latest PS from Rick Hudson
//      TryBots failed on linux-386, windows-386-gce, nacl-386, linux-arm
//    Ready      b3b8fef6cb runtime: allocate GC workbufs… [golang.org/cl/38581]
//      1 comment on latest PS from Rick Hudson
//      TryBots failed on windows-386-gce, linux-386, nacl-386, linux-arm
//    Ready      5fc11e7173 runtime: eliminate write barr… [golang.org/cl/38580]
//    Ready      b5c7f08ccb runtime: rename gcBits -> gcB… [golang.org/cl/38579]
//    Pending    d9dd54b571 runtime: eliminate write barr… [golang.org/cl/38578]
//      2 comments on latest PS from Rick Hudson, Austin Clements
//      TryBots failed on linux-amd64
//    Pending    b70f9f7dc2 runtime: don't count manually… [golang.org/cl/38577]
//      1 comment on latest PS from Rick Hudson
//    Ready      1eae861947 runtime: generalize {alloc,fr… [golang.org/cl/38576]
//    Ready      670d05695f runtime: rename mspan.stackfr… [golang.org/cl/38575]
//    Ready      3e531adf5f runtime: rename _MSpanStack -… [golang.org/cl/38574]
//      1 comment on latest PS from Rick Hudson
//    Ready      3e3125c7e5 runtime: initialize more fiel… [golang.org/cl/38573]
//      2 comments on latest PS from Rick Hudson, Austin Clements
//    Submitted  302daf57f6 runtime: improve systemstack-… [golang.org/cl/38572]
//      Local commit message differs
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	// TODO: Support other repos.
	remoteUrl = "https://go.googlesource.com/go"
	project   = "go"
	gerritUrl = "https://go-review.googlesource.com"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] [branches...]\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "With no arguments, list branches from newest to oldest.\n\n")
		flag.PrintDefaults()
	}
	defIgnore, _ := tryGit("config", "p.ignore")
	flagIgnore := flag.String("ignore", defIgnore, "ignore branches matching shell `pattern` [git config p.ignore]")
	flagLocal := flag.Bool("l", false, "local state only; don't query Gerrit")
	flag.Parse()
	branches := flag.Args()
	ignores := strings.Fields(*flagIgnore)

	// Check the branch names.
	for _, b := range branches {
		if out, err := tryGit("rev-parse", b, "--"); err != nil {
			fmt.Printf("%s\n", out)
			os.Exit(1)
		}
	}

	// Check ignore patterns.
	for _, ig := range ignores {
		if _, err := filepath.Match(ig, ""); err != nil {
			fmt.Fprintf(os.Stderr, "bad ignore pattern %q: %s", ig, err)
			os.Exit(1)
		}
	}

	if !setupPager() {
		// We're in a dumb terminal. Turn off control codes.
		style = nil
	}

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

	var gerrit *Gerrit
	if !*flagLocal {
		gerrit = NewGerrit(gerritUrl)
	}

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
		if len(ignores) > 0 {
			nBranches := []string{}
		branchLoop:
			for _, b := range branches {
				for _, ig := range ignores {
					if m, _ := filepath.Match(ig, b); m {
						continue branchLoop
					}
					if m, _ := filepath.Match("refs/heads/"+ig, b); m {
						continue branchLoop
					}
				}
				nBranches = append(nBranches, b)
			}
			branches = nBranches
		}
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
	var haveUpstream bool
	upstream := upstreamOf(branch)
	if upstream == "" {
		upstream = "refs/remotes/" + remote + "/master"
	} else {
		haveUpstream = true
	}

	// Get commits from the branch to any upstream.
	//
	// TODO: This can be quite slow (50–100 ms). git is clearly
	// reasonably clever about this, but it has to expand the
	// exclusion list and can't share work across all of these
	// branches. Maybe this should fully expand the exclusion set
	// just once, do limited rev-lists, and cut them off at the
	// exclusion set.
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
	if gerrit != nil {
		for i, cid := range cids {
			// TODO: Would this be simpler with a single big OR query?
			if cid != "" {
				changes[i] = gerrit.QueryChanges("change:"+cid, printChangeOptions...)
			}
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
		fmt.Printf("%s%s%s", style["branch"], strings.TrimPrefix(branch, "refs/heads/"), style["reset"])
		if extra != "" {
			fmt.Printf(" (%s%s%s)", style["symbolic-ref"], extra, style["reset"])
		}
		if haveUpstream {
			fmt.Printf(" for %s", strings.TrimPrefix(upstream, "refs/remotes/"+remote+"/"))
		}
		fmt.Printf("\n")
		for i, change := range changes {
			printChange(commits[i], change, gerrit == nil)
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
		// Some messages have no author?
		if msg.Author == nil {
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
		// TryBots haven't run. If it's submitted, we don't care.
		if info.Status != "MERGED" {
			// Are they running?
			if rtb := info.Labels["Run-TryBot"]; rtb != nil && rtb.Approved != nil {
				warnings = append(warnings, "TryBots running")
			} else {
				warnings = append(warnings, "TryBots not run")
			}
		}
	}

	switch info.Status {
	default:
		status = fmt.Sprintf("Unknown status %q", info.Status)
	case "MERGED":
		status = "Submitted"
	case "ABANDONED":
		status = "Abandoned"
	case "DRAFT":
		status = "Draft"
	case "NEW":
		// Submittable? (Requires SUBMITTABLE option.)
		status = "Pending"
		if rejected {
			status = "Rejected"
		} else if info.Submittable {
			status = "Ready"
		}
	}

	return status, warnings
}

var printChangeOptions = []string{"SUBMITTABLE", "LABELS", "CURRENT_REVISION", "MESSAGES", "DETAILED_ACCOUNTS"}

// printChange prints a summary of change's status and warnings.
//
// change must be retrieved with options printChangeOptions.
func printChange(commit string, change *GerritChanges, local bool) {
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
	} else if local {
		status = ""
	}

	var control, eControl string
	if len(warnings) != 0 {
		if c, ok := style[status+" warn"]; ok {
			control = c
		}
	}
	if control == "" {
		if c, ok := style[status]; ok {
			control = c
		}
	}
	if control != "" {
		eControl = style["reset"]
	}

	hdr := logMsg
	if status != "" {
		hdr = fmt.Sprintf("%-10s %s", status, logMsg)
	}
	hdrMax := 80 - len(link) - 2
	if utf8.RuneCountInString(hdr) > hdrMax {
		hdr = fmt.Sprintf("%*.*s…", hdrMax-1, hdrMax-1, hdr)
	}
	fmt.Printf("  %s%-*s%s%s\n", control, hdrMax, hdr, eControl, link)
	for _, w := range warnings {
		fmt.Printf("    %s\n", w)
	}
}
