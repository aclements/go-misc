// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// cl-fetch fetches and tags CLs from Gerrit.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"golang.org/x/build/gerrit"
)

var (
	flagOutgoing = flag.Bool("outgoing", false, "fetch outgoing CLs")
	flagIncoming = flag.Bool("incoming", false, "fetch incoming CLs")
	flagQuery    = flag.String("q", "", "fetch CLs matching `query`")
	flagVerbose  = flag.Bool("v", false, "verbose output")
	flagDry      = flag.Bool("dry-run", false, "print but do not execute commands")
)

var clRe = regexp.MustCompile("[0-9]+|I[0-9a-f]{40}")

type Tag struct {
	tag    string
	commit *gerrit.CommitInfo
}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] [CLs...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	queryParts := []string{}
	if *flagOutgoing {
		queryParts = append(queryParts, "is:open owner:self")
	}
	if *flagIncoming {
		queryParts = append(queryParts, "is:open reviewer:self -owner:self")
	}
	if *flagQuery != "" {
		queryParts = append(queryParts, *flagQuery)
	}
	for _, arg := range flag.Args() {
		if !clRe.MatchString(arg) {
			fmt.Fprintf(os.Stderr, "CL must be a CL number or Change-Id")
			os.Exit(2)
		}
		queryParts = append(queryParts, "change:"+arg)
	}
	if len(queryParts) == 0 {
		fmt.Fprintf(os.Stderr, "must specify something to fetch\n")
		os.Exit(2)
	}
	query := "(" + strings.Join(queryParts, ") OR (") + ")"

	if *flagVerbose {
		log.Printf("query: %s", query)
	}

	// Get the origin so we don't pull CLs for other repositories
	// in to this one.
	origin := gitOutput("config", "remote.origin.url")

	// Get the existing CL tags.
	haveTags := map[string]bool{}
	for _, tag := range strings.Split(gitOutput("tag"), "\n") {
		haveTags[tag] = true
	}

	c := gerrit.NewClient("https://go-review.googlesource.com", gerrit.GitCookiesAuth())

	cls, err := c.QueryChanges(query, gerrit.QueryChangesOpt{
		Fields: []string{"CURRENT_REVISION", "CURRENT_COMMIT"},
	})
	if err != nil {
		log.Fatal(err)
	}

	if *flagVerbose {
		v, _ := json.MarshalIndent(cls, "", "  ")
		log.Printf("Query response:\n%s\n", v)
	}

	// Collect git fetch and tag commands.
	fetchCmd := []string{"fetch", "--", origin}
	tags := make(map[string]*Tag)
	hashOrder := []string{}
	for _, cl := range cls {
		for commitID, rev := range cl.Revisions {
			tag := fmt.Sprintf("cl/%d/%d", cl.ChangeNumber, rev.PatchSetNumber)
			if !haveTags[tag] {
				any := false
				for _, fetch := range rev.Fetch {
					if fetch.URL == origin {
						fetchCmd = append(fetchCmd, fetch.Ref)
						any = true
						break
					}
				}
				if !any {
					continue
				}
			}

			tags[commitID] = &Tag{
				tag:    tag,
				commit: rev.Commit,
			}

			hashOrder = append(hashOrder, commitID)
		}
	}

	// Execute git fetch and tag commands.
	if len(fetchCmd) != 3 {
		git(fetchCmd...)
		fmt.Println()
	}
	for commitID, tag := range tags {
		if !haveTags[tag.tag] {
			git("tag", tag.tag, commitID)
		}
	}
	if *flagDry {
		// Separate command from printed tags.
		fmt.Println()
	}

	// Print tags.
	leafs := make(map[string]bool)
	for commitID, _ := range tags {
		leafs[commitID] = true
	}
	for _, tag := range tags {
		for _, parent := range tag.commit.Parents {
			leafs[parent.CommitID] = false
		}
	}

	printed := make(map[string]bool)
	needBlank := false
	for i := range hashOrder {
		commitID := hashOrder[len(hashOrder)-i-1]
		if !leafs[commitID] {
			continue
		}
		if needBlank {
			fmt.Println()
		}
		needBlank = printChain(tags, commitID, printed)
	}
}

func git(args ...string) {
	if *flagDry {
		fmt.Printf("git %s\n", strings.Join(args, " "))
		return
	}

	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("git %s failed: %s", strings.Join(args, " "), err)
	}
}

func gitOutput(args ...string) string {
	if *flagDry {
		fmt.Printf("git %s\n", strings.Join(args, " "))
	}

	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		log.Fatalf("git %s failed: %s", strings.Join(args, " "), err)
	}
	return strings.TrimRight(string(out), "\n")
}

func printChain(tags map[string]*Tag, commitID string, printed map[string]bool) bool {
	if printed[commitID] {
		return false
	}
	printed[commitID] = true

	tag := tags[commitID]
	for _, parent := range tag.commit.Parents {
		if tags[parent.CommitID] != nil {
			printChain(tags, parent.CommitID, printed)
		}
	}
	fmt.Printf("%s %s\n", tag.tag, tag.commit.Subject)
	return true
}
