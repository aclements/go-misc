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
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"rsc.io/github"
)

var docjson = flag.Bool("docjson", false, "print google doc info in json")
var doccsv = flag.Bool("doccsv", false, "print google doc info in json")

var failure = false

func main() {
	log.SetPrefix("minutes3: ")
	log.SetFlags(0)

	flag.Parse()
	doc := parseDoc()
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

	r, err := NewReporter()
	if err != nil {
		log.Fatal(err)
	}
	r.RetireOld()

	minutes := r.Update(doc)
	if failure {
		return
	}
	fmt.Printf("TO POST TO https://go.dev/s/proposal-minutes:\n\n")
	r.Print(minutes)
}

type Reporter struct {
	Client    *github.Client
	Proposals *github.Project
	Items     map[int]*github.ProjectItem
	Labels    map[string]*github.Label
	Backlog   *github.Milestone
}

func NewReporter() (*Reporter, error) {
	c, err := github.Dial("")
	if err != nil {
		return nil, err
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

func (r *Reporter) Update(doc *Doc) *Minutes {
	const prefix = "https://github.com/golang/go/issues/"

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

	seen := make(map[int]bool)
Issues:
	for _, di := range doc.Issues {
		item := r.Items[di.Number]
		if item == nil {
			log.Printf("missing from proposal project: #%d", di.Number)
			failure = true
			continue
		}
		seen[di.Number] = true
		issue := item.Issue
		status := item.FieldByName("Status")
		if status == nil {
			log.Printf("item missing status: #%d", di.Number)
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
			log.Printf("#%d missing action")
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

		if check {
			comments, err := r.Client.IssueComments(issue)
			if err != nil {
				log.Printf("%s: cannot read issue comments\n", url)
				failure = true
				continue
			}
			for i := len(comments) - 1; i >= 0; i-- {
				c := comments[i]
				if time.Since(c.CreatedAt) < 5*24*time.Hour && strings.Contains(c.Body, checkQuestion) {
					log.Printf("%s: recently checked", url)
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
			if err := r.Client.AddIssueComment(issue, msg); err != nil {
				log.Printf("%s: posting comment: %v", url, err)
				failure = true
			}
			log.Printf("posted %s", url)
		}

		if status.Option.Name != col {
			msg := updateMsg(status.Option.Name, col, reason)
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
			if err := r.Client.AddIssueComment(issue, msg); err != nil {
				log.Printf("%s: posting comment: %v", url, err)
				failure = true
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
	return m
}

func (r *Reporter) Print(m *Minutes) {
	var buf bytes.Buffer

	fmt.Fprintf(&buf, "**%s / ", m.Date.Format("2006-01-02"))
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

	os.Stdout.Write(buf.Bytes())
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
