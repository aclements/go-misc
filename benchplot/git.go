// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type CommitInfo struct {
	Hash, Subject, Branch  string
	AuthorDate, CommitDate time.Time

	Parents, Children []string
}

func Commits(repo string, revs ...string) (commits []CommitInfo) {
	args := []string{"-C", repo, "log", "-s",
		"--format=format:%H %aI %cI %P\n%s\n"}
	if len(revs) == 0 {
		args = append(args, "--all")
	} else {
		args = append(append(args, "--"), revs...)
	}
	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatal("git show failed: ", err)
	}
	for _, line := range strings.Split(string(out), "\n\n") {
		parts := strings.Split(line, "\n")
		subject := parts[1]
		parts = strings.Split(parts[0], " ")

		adate, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			log.Fatal("cannot parse author date: ", err)
		}
		cdate, err := time.Parse(time.RFC3339, parts[2])
		if err != nil {
			log.Fatal("cannot parse commit date: ", err)
		}

		commits = append(commits, CommitInfo{
			parts[0], subject, "", adate, cdate,
			parts[3:], nil,
		})
	}

	// Compute hash indexes.
	hashset := make(map[string]*CommitInfo)
	for i := range commits {
		hashset[commits[i].Hash] = &commits[i]
	}

	// Compute children hashes.
	for h, ci := range hashset {
		for _, parent := range ci.Parents {
			if ci2, ok := hashset[parent]; ok {
				ci2.Children = append(ci2.Children, h)
			}
		}
	}

	// Compute branch names.
	var branchRe = regexp.MustCompile(`^\[[^] ]+\] `)
	var branchOf func(ci *CommitInfo) string
	branchOf = func(ci *CommitInfo) string {
		subject := ci.Subject
		if strings.HasPrefix(subject, "[") {
			m := branchRe.FindString(subject)
			if m != "" {
				return m[1 : len(m)-2]
			}
		}
		if strings.HasPrefix(subject, "Merge") || strings.HasPrefix(subject, "Revert") {
			// Walk children looking for a branch name.
			for _, child := range ci.Children {
				if ci2 := hashset[child]; ci2 != nil {
					branch := branchOf(ci2)
					if branch != "master" {
						return branch
					}
				}
			}
		}
		return "master"
	}
	for _, ci := range hashset {
		ci.Branch = branchOf(ci)
	}
	// Clean up missing branch tags: if all parents and children
	// of a commit have the same non-master branch, that commit
	// must also have been from that branch.
cleanBranches:
	for _, ci := range hashset {
		if ci.Branch == "master" {
			alt := ""
			for _, child := range ci.Children {
				if ci2 := hashset[child]; ci2 != nil {
					if alt == "" {
						alt = ci2.Branch
					} else if ci2.Branch != alt {
						continue cleanBranches
					}
				}
			}
			for _, parent := range ci.Parents {
				if ci2 := hashset[parent]; ci2 != nil {
					if alt == "" {
						alt = ci2.Branch
					} else if ci2.Branch != alt {
						continue cleanBranches
					}
				}
			}
			if alt != "" {
				ci.Branch = alt
			}
		}
	}

	return
}
