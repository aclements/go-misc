// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "rsc.io/github"

// GitHubClient wraps the [github.Client] API to provide injection points.
type GitHubClient interface {
	SearchLabels(org string, repo string, query string) ([]*github.Label, error)
	SearchMilestones(org string, repo string, query string) ([]*github.Milestone, error)

	IssueComments(issue *github.Issue) ([]*github.IssueComment, error)
	AddIssueComment(issue *github.Issue, text string) error
	AddIssueLabels(issue *github.Issue, labels ...*github.Label) error
	RemoveIssueLabels(issue *github.Issue, labels ...*github.Label) error
	CloseIssue(issue *github.Issue) error
	RetitleIssue(issue *github.Issue, title string) error
	RemilestoneIssue(issue *github.Issue, milestone *github.Milestone) error

	Projects(org string, query string) ([]*github.Project, error)
	ProjectItems(p *github.Project) ([]*github.ProjectItem, error)
	DeleteProjectItem(project *github.Project, item *github.ProjectItem) error
	SetProjectItemFieldOption(project *github.Project, item *github.ProjectItem, field *github.ProjectField, option *github.ProjectFieldOption) error

	Discussions(org string, repo string) ([]*github.Discussion, error)
}
