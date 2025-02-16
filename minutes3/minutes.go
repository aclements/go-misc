// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Minutes is the program we use to post the proposal review minutes.
// It is a demonstration of the use of the rsc.io/github API, but it is also not great code,
// which is why it is buried in an internal directory.
package main

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"rsc.io/github"
)

var docjson = flag.Bool("docjson", false, "print google doc info in json")
var doccsv = flag.Bool("doccsv", false, "print google doc info in csv")
var apply = flag.Bool("apply", false, "perform actions")
var testSheet = flag.String("test-sheet", "", "use sheet doc `id` for testing")

var failure = false

func main() {
	const sheetDocID = "1EG7oPcLls9HI_exlHLYuwk2YaN4P5mDc4O2vGyRqZHU"

	log.SetPrefix("minutes3: ")
	log.SetFlags(0)

	flag.Parse()
	docID := sheetDocID
	if *testSheet != "" {
		if *apply {
			log.Fatalf("cannot use both -test-sheet and -apply")
		}
		docID = *testSheet
		if docID == sheetDocID {
			log.Fatalf("-test-sheet is the ID of the main sheet")
		}
	}
	doc := parseDoc(docID)
	if *docjson {
		js, err := json.MarshalIndent(doc, "", "\t")
		if err != nil {
			log.Fatal(err)
		}
		os.Stdout.Write(append(js, '\n'))
		return
	}
	if *doccsv {
		var out [][]string
		for _, issue := range doc.Issues {
			out = append(out, []string{fmt.Sprint(issue.Number), issue.Minutes, issue.Title, issue.Details, issue.Comment, issue.Notes})
		}
		w := csv.NewWriter(os.Stdout)
		w.WriteAll(out)
		w.Flush()
		return
	}

	r, err := NewReporter(!*apply)
	if err != nil {
		log.Fatal(err)
	}
	r.RetireOld()

	minutes, commentURLs := r.Update(doc)
	if failure {
		// TODO: Should we delay updates and apply them only if there are no
		// failures?
		os.Exit(1)
	}
	const minutesIssue = 33502 // AKA https://go.dev/s/proposal-minutes
	r.PostMinutes(minutes, minutesIssue)

	if !*apply && *testSheet == "" {
		fmt.Println()
		fmt.Printf("Re-run with -apply to perform above actions\n")
		return
	}

	doc.FinishDoc(commentURLs)
	if failure {
		os.Exit(1)
	}
}

func getConfig(path ...string) string {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	return filepath.Join(append([]string{cfgDir, "proposal-minutes"}, path...)...)
}

func getCacheDir() string {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		log.Fatal(err)
	}
	cacheDir = filepath.Join(cacheDir, "proposal-minutes")
	if err := os.MkdirAll(cacheDir, 0777); err != nil {
		log.Fatalf("creating cache directory: %s", err)
	}
	return cacheDir
}

type Reporter struct {
	Client    GitHubClient
	Proposals *github.Project
	Items     map[int]*github.ProjectItem
	Labels    map[string]*github.Label
	Backlog   *github.Milestone
}

func NewReporter(dryRun bool) (*Reporter, error) {
	token, err := os.ReadFile(getConfig("github.tok"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Println("Please follow the instructions in README.md to create a GitHub token.")
		}
		log.Fatal(err)
	}
	token = bytes.TrimSpace(token)

	var c GitHubClient
	c = github.NewClient(string(token))
	if dryRun {
		c = &GitHubDryClient{c, slog.Default()}
	}

	r := &Reporter{Client: c}

	ps, err := r.Client.Projects("golang", "")
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.Title == "Proposals" {
			r.Proposals = p
			break
		}
	}
	if r.Proposals == nil {
		return nil, fmt.Errorf("cannot find Proposals project")
	}

	labels, err := r.Client.SearchLabels("golang", "go", "")
	if err != nil {
		return nil, err
	}
	r.Labels = make(map[string]*github.Label)
	for _, label := range labels {
		r.Labels[label.Name] = label
	}

	milestones, err := r.Client.SearchMilestones("golang", "go", "Backlog")
	if err != nil {
		return nil, err
	}
	for _, m := range milestones {
		if m.Title == "Backlog" {
			r.Backlog = m
			break
		}
	}
	if r.Backlog == nil {
		return nil, fmt.Errorf("cannot find Backlog milestone")
	}

	items, err := r.Client.ProjectItems(r.Proposals)
	if err != nil {
		return nil, err
	}
	r.Items = make(map[int]*github.ProjectItem)
	for _, item := range items {
		if item.Issue == nil {
			log.Printf("unexpected project item with no issue")
			failure = true
			continue
		}
		r.Items[item.Issue.Number] = item
	}

	return r, nil
}

type Minutes struct {
	Date   time.Time
	Who    []string
	Events []*Event
}

type Event struct {
	Column  string
	Issue   string
	Title   string
	Actions []string
}

const checkQuestion = "Have all remaining concerns about this proposal been addressed?"

func (r *Reporter) Update(doc *Doc) (*Minutes, map[int]string) {
	m := new(Minutes)
	m.Date = doc.Date

	// Attendees
	if len(doc.Who) == 0 {
		log.Fatalf("missing attendees")
	}
	m.Who = make([]string, len(doc.Who))
	for i, w := range doc.Who {
		m.Who[i] = gitWho(w)
	}
	sort.Strings(m.Who)

	// Get current user's login for constructing messages
	userName, err := GitHubUser(r.Client)
	if err != nil {
		log.Fatalf("getting current user: %v", err)
	}

	seen := make(map[int]bool)
	commentURLs := make(map[int]string)
Issues:
	for _, di := range doc.Issues {
		var commentURL string
		item := r.Items[di.Number]
		if item == nil {
			// TODO: Maybe "add" should add it to the proposal project if it
			// isn't already there and set the Status to "Active".
			log.Printf("missing from proposal project: #%d", di.Number)
			failure = true
			continue
		}
		seen[di.Number] = true
		issue := item.Issue
		status := item.FieldByName("Status")
		if status == nil {
			log.Printf("item missing status in proposal project (set to incoming?): #%d", di.Number)
			failure = true
			continue
		}

		title := strings.TrimSpace(strings.TrimPrefix(issue.Title, "proposal:"))
		if title != di.Title {
			log.Printf("#%d title mismatch:\nGH: %s\nDoc: %s", di.Number, issue.Title, di.Title)
			failure = true
		}

		url := "https://go.dev/issue/" + fmt.Sprint(di.Number)
		actions := strings.Split(di.Minutes, ";")
		if len(actions) == 1 && actions[0] == "" {
			actions = nil
		}
		if len(actions) == 0 {
			log.Printf("#%d missing action", di.Number)
			failure = true
		}
		col := "Active"
		reason := ""
		check := false
		for i, a := range actions {
			a = strings.TrimSpace(a)
			actions[i] = a
			switch a {
			case "TODO":
				log.Printf("%s: minutes TODO", url)
				failure = true
				continue Issues
			case "accept":
				a = "accepted"
			case "decline":
				a = "declined"
			case "retract":
				a = "retracted"
			case "declined as infeasible":
				a = "infeasible"
			case "check":
				check = true
				a = "comment"
			}

			switch a {
			case "likely accept":
				col = "Likely Accept"
			case "likely decline":
				col = "Likely Decline"
			case "accepted":
				col = "Accepted"
			case "declined":
				col = "Declined"
			case "retracted":
				col = "Declined"
				reason = "retracted"
			case "unhold":
				col = "Active"
				reason = "unhold"
			}
			if strings.HasPrefix(a, "declined") {
				col = "Declined"
			}
			if strings.HasPrefix(a, "duplicate") {
				col = "Declined"
				reason = "duplicate"
			}
			if strings.Contains(a, "infeasible") {
				col = "Declined"
				reason = "infeasible"
			}
			if a == "obsolete" || strings.Contains(a, "obsoleted") {
				col = "Declined"
				reason = "obsolete"
			}
			if strings.HasPrefix(a, "closed") {
				col = "Declined"
			}
			if strings.HasPrefix(a, "hold") || a == "on hold" {
				col = "Hold"
			}
			if r := actionMap[a]; r != "" {
				actions[i] = r
			}
			if strings.HasPrefix(a, "removed") {
				col = "none"
				reason = "removed"
			}
		}

		commentsOnce := sync.OnceValues(func() ([]*github.IssueComment, error) {
			comments, err := r.Client.IssueComments(issue)
			if err != nil {
				log.Printf("%s: cannot read issue comments\n", url)
				failure = true
			}
			return comments, err
		})

		if check {
			comments, err := commentsOnce()
			if err != nil {
				continue
			}
			for i := len(comments) - 1; i >= 0; i-- {
				c := comments[i]
				if time.Since(c.CreatedAt) < 5*24*time.Hour && strings.Contains(c.Body, checkQuestion) {
					log.Printf("%s: recently checked", url)
					commentURL = c.URL
					continue Issues
				}
			}

			if di.Details == "" {
				log.Printf("%s: missing proposal details", url)
				failure = true
				continue Issues
			}
			msg := fmt.Sprintf("%s\n\n%s", checkQuestion, di.Details)
			// log.Fatalf("wouldpost %s\n%s", url, msg)
			if url, err := GitHubAddIssueComment(r.Client, issue, msg); err != nil && err != ErrReadOnly {
				log.Printf("%s: posting comment: %v", url, err)
				failure = true
			} else {
				commentURL = url
			}
			log.Printf("posted %s", url)
		}

		if status.Option.Name != col {
			msg := updateMsg(status.Option.Name, col, reason, userName)
			if msg == "" {
				log.Fatalf("no update message for %s", col)
			}
			if col == "Likely Accept" || col == "Accepted" {
				if di.Details == "" {
					log.Printf("%s: missing proposal details", url)
					failure = true
					continue Issues
				}
				msg += "\n\n" + di.Details
			}
			f := r.Proposals.FieldByName("Status")
			if col == "none" {
				if err := r.Client.DeleteProjectItem(r.Proposals, item); err != nil {
					log.Printf("%s: deleting proposal item: %v", url, err)
					failure = true
					continue
				}
			} else {
				o := f.OptionByName(col)
				if o == nil {
					log.Printf("%s: moving from %s to %s: no such status\n", url, status.Option.Name, col)
					failure = true
					continue
				}
				if err := r.Client.SetProjectItemFieldOption(r.Proposals, item, f, o); err != nil {
					log.Printf("%s: moving from %s to %s: %v\n", url, status.Option.Name, col, err)
					failure = true
				}
			}
			if url, err := GitHubAddIssueComment(r.Client, issue, msg); err != nil && err != ErrReadOnly {
				log.Printf("%s: posting comment: %v", url, err)
				failure = true
			} else {
				commentURL = url
			}
		}

		needLabel := func(name string) {
			if issue.LabelByName(name) == nil {
				lab := r.Labels[name]
				if lab == nil {
					log.Fatalf("%s: cannot find label %s", url, name)
				}
				if err := r.Client.AddIssueLabels(issue, lab); err != nil {
					log.Printf("%s: adding %s: %v", url, name, err)
					failure = true
				}
			}
		}

		dropLabel := func(name string) {
			if lab := issue.LabelByName(name); lab != nil {
				if err := r.Client.RemoveIssueLabels(issue, lab); err != nil {
					log.Printf("%s: removing %s: %v", url, name, err)
					failure = true
				}
			}
		}

		setLabel := func(name string, val bool) {
			if val {
				needLabel(name)
			} else {
				dropLabel(name)
			}
		}

		forceClose := func() {
			if !issue.Closed {
				if err := r.Client.CloseIssue(issue); err != nil {
					log.Printf("%s: closing issue: %v", url, err)
					failure = true
				}
			}
		}

		if col == "Accepted" {
			if strings.HasPrefix(issue.Title, "proposal:") {
				if err := r.Client.RetitleIssue(issue, title); err != nil {
					log.Printf("%s: retitling: %v", url, err)
					failure = true
				}
			}
			if issue.Milestone == nil || issue.Milestone.Title == "Proposal" {
				if err := r.Client.RemilestoneIssue(issue, r.Backlog); err != nil {
					log.Printf("%s: moving out of Proposal milestone: %v", url, err)
					failure = true
				}
			}
		}
		if col == "Declined" {
			forceClose()
		}

		setLabel("Proposal-Accepted", col == "Accepted")
		setLabel("Proposal-FinalCommentPeriod", col == "Likely Accept" || col == "Likely Decline")
		setLabel("Proposal-Hold", col == "Hold")

		m.Events = append(m.Events, &Event{Column: col, Issue: fmt.Sprint(di.Number), Title: title, Actions: actions})

		if commentURL == "" {
			// Search for the latest comment from a committee member.
			//
			// TODO: Don't touch the link for "skip" or "discuss". There can be
			// multiple actions, so this isn't completely straightforward.
			//
			// TODO: For status "comment", check that what we find is recent?
			comments, err := commentsOnce()
			if err != nil {
				continue
			}
			for i := len(comments) - 1; i >= 0; i-- {
				c := comments[i]
				if committeeUsers[c.Author] {
					commentURL = c.URL
					break
				}
			}
		}

		if commentURL != "" {
			commentURLs[di.Number] = commentURL
		}
	}

	for id, item := range r.Items {
		status := item.FieldByName("Status")
		if status != nil {
			switch status.Option.Name {
			case "Active", "Likely Accept", "Likely Decline":
				if !seen[id] {
					log.Printf("#%d: missing from doc", id)
					failure = true
				}
			}
		}
	}

	sort.Slice(m.Events, func(i, j int) bool {
		return m.Events[i].Title < m.Events[j].Title
	})
	return m, commentURLs
}

func (r *Reporter) PostMinutes(m *Minutes, issueNum int) {
	var buf bytes.Buffer

	prefix := fmt.Sprintf("**%s / ", m.Date.Format("2006-01-02"))
	buf.WriteString(prefix)
	for i, who := range m.Who {
		if i > 0 {
			fmt.Fprintf(&buf, ", ")
		}
		fmt.Fprintf(&buf, "%s", who)
	}
	fmt.Fprintf(&buf, "**\n\n")

	disc, err := r.Client.Discussions("golang", "go")
	if err != nil {
		log.Fatal(err)
	}
	first := true
	for _, d := range disc {
		if d.Locked {
			continue
		}
		if first {
			fmt.Fprintf(&buf, "**Discussions (not yet proposals)**\n\n")
			first = false
		}
		fmt.Fprintf(&buf, "- **%s** [#%d](https://go.dev/issue/%d)\n", markdownEscape(strings.TrimSpace(d.Title)), d.Number, d.Number)
	}
	if !first {
		fmt.Fprintf(&buf, "\n")
	}

	columns := []string{
		"Accepted",
		"Declined",
		"Likely Accept",
		"Likely Decline",
		"Active",
		"Hold",
		"Other",
	}

	for _, col := range columns {
		n := 0
		for i, e := range m.Events {
			if e == nil || e.Column != col && col != "Other" {
				continue
			}
			if n == 0 {
				fmt.Fprintf(&buf, "**%s**\n\n", col)
			}
			n++
			fmt.Fprintf(&buf, "- **%s** [#%s](https://go.dev/issue/%s)\n", markdownEscape(strings.TrimSpace(e.Title)), e.Issue, e.Issue)
			for _, a := range e.Actions {
				if a == "" {
					// If we print an empty string, the - by itself will turn
					// the previous line into a markdown heading!
					// Also everything should have an action.
					log.Fatalf("#%s: missing action", e.Issue)
				}
				fmt.Fprintf(&buf, "  - %s\n", a)
			}
			m.Events[i] = nil
		}
		if n == 0 && col != "Hold" && col != "Other" {
			fmt.Fprintf(&buf, "**%s**\n\n", col)
			fmt.Fprintf(&buf, "- none\n")
		}
		fmt.Fprintf(&buf, "\n")
	}

	post := buf.String()

	// Check if we've already posted this.
	issue, err := r.Client.Issue("golang", "go", issueNum)
	if err != nil {
		log.Fatalf("could not find minutes issue #%d: %s", issueNum, err)
	}
	comments, err := r.Client.IssueComments(issue)
	if err != nil {
		log.Fatalf("could not read minutes issue comments: %s", err)
	}
	for _, c := range comments {
		if strings.Contains(c.Body, prefix) {
			if c.Body != post {
				log.Fatalf("minutes issue #%d has has comment from %s, but does not match full post", issueNum, m.Date.Format("2006-01-02"))
			}
			log.Printf("already posted to minutes #%d", issueNum)
			return
		}
	}

	// Post minutes
	log.Printf("posting to minutes #%d", issueNum)
	if err := r.Client.AddIssueComment(issue, post); err != nil {
		log.Fatalf("error posting to minutes #%d: %s", issueNum, err)
	}
}

var markdownEscaper = strings.NewReplacer(
	"_", `\_`,
	"*", `\*`,
	"`", "\\`",
	"[", `\[`,
)

func markdownEscape(s string) string {
	return markdownEscaper.Replace(s)
}

func (r *Reporter) RetireOld() {
	for _, item := range r.Items {
		issue := item.Issue
		if issue.Closed && !issue.ClosedAt.IsZero() && time.Since(issue.ClosedAt) > 365*24*time.Hour {
			log.Printf("retire #%d", issue.Number)
			if err := r.Client.DeleteProjectItem(r.Proposals, item); err != nil {
				log.Printf("#%d: deleting proposal item: %v", issue.Number, err)
			}
		}
	}
}
