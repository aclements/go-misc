// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"log/slog"
	"strings"

	"rsc.io/github"
	"rsc.io/github/schema"
)

// GitHubClient wraps the [github.Client] API to provide injection points.
type GitHubClient interface {
	GraphQLQuery(query string, vars github.Vars) (*schema.Query, error)
	GraphQLMutation(query string, vars github.Vars) (*schema.Mutation, error)

	SearchLabels(org string, repo string, query string) ([]*github.Label, error)
	SearchMilestones(org string, repo string, query string) ([]*github.Milestone, error)

	Issue(org string, repo string, n int) (*github.Issue, error)
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

// GitHubDryClient is a dry-run client that logs mutation operations without
// performing them.
type GitHubDryClient struct {
	c      GitHubClient
	logger *slog.Logger
}

func (c *GitHubDryClient) GraphQLQuery(query string, vars github.Vars) (*schema.Query, error) {
	return c.c.GraphQLQuery(query, vars)
}

var ErrReadOnly = errors.New("cannot perform mutation on read-only client")

func (c *GitHubDryClient) GraphQLMutation(query string, vars github.Vars) (*schema.Mutation, error) {
	c.logger.Info("github", "action", "GraphQLMutation", "query", query, "vars", vars)
	return nil, ErrReadOnly
}

func (c *GitHubDryClient) SearchLabels(org string, repo string, query string) ([]*github.Label, error) {
	return c.c.SearchLabels(org, repo, query)
}

func (c *GitHubDryClient) SearchMilestones(org string, repo string, query string) ([]*github.Milestone, error) {
	return c.c.SearchMilestones(org, repo, query)
}

func (c *GitHubDryClient) Issue(org string, repo string, n int) (*github.Issue, error) {
	return c.c.Issue(org, repo, n)
}

func (c *GitHubDryClient) IssueComments(issue *github.Issue) ([]*github.IssueComment, error) {
	return c.c.IssueComments(issue)
}

func (c *GitHubDryClient) AddIssueComment(issue *github.Issue, text string) error {
	c.logger.Info("github", "action", "AddIssueComment", "issue", issue.Number, "text", text)
	return nil
}

type labelList []*github.Label

func (ll labelList) String() string {
	var b strings.Builder
	for i, l := range ll {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(l.Name)
	}
	return b.String()
}

func (c *GitHubDryClient) AddIssueLabels(issue *github.Issue, labels ...*github.Label) error {
	c.logger.Info("github", "action", "AddIssueLabels", "issue", issue.Number, "labels", labelList(labels))
	return nil
}

func (c *GitHubDryClient) RemoveIssueLabels(issue *github.Issue, labels ...*github.Label) error {
	c.logger.Info("github", "action", "RemoveIssueLabels", "issue", issue.Number, "labels", labelList(labels))
	return nil
}

func (c *GitHubDryClient) CloseIssue(issue *github.Issue) error {
	c.logger.Info("github", "action", "CloseIssue", "issue", issue.Number)
	return nil
}

func (c *GitHubDryClient) RetitleIssue(issue *github.Issue, title string) error {
	c.logger.Info("github", "action", "RetitleIssue", "issue", issue.Number, "title", title)
	return nil
}

func (c *GitHubDryClient) RemilestoneIssue(issue *github.Issue, milestone *github.Milestone) error {
	c.logger.Info("github", "action", "RemilestoneIssue", "issue", issue.Number, "milestone", milestone.Title)
	return nil
}

func (c *GitHubDryClient) Projects(org string, query string) ([]*github.Project, error) {
	return c.c.Projects(org, query)
}

func (c *GitHubDryClient) ProjectItems(p *github.Project) ([]*github.ProjectItem, error) {
	return c.c.ProjectItems(p)
}

func (c *GitHubDryClient) DeleteProjectItem(project *github.Project, item *github.ProjectItem) error {
	c.logger.Info("github", "action", "DeleteProjectItem", "project", project.Title, "item", item.Issue.Number)
	return nil
}

func (c *GitHubDryClient) SetProjectItemFieldOption(project *github.Project, item *github.ProjectItem, field *github.ProjectField, option *github.ProjectFieldOption) error {
	c.logger.Info("github", "action", "SetProjectItemFieldOption", "project", project.Title, "item", item.Issue.Number, "field", field.Name, "option", option.Name)
	return nil
}

func (c *GitHubDryClient) Discussions(org string, repo string) ([]*github.Discussion, error) {
	return c.c.Discussions(org, repo)
}

// GitHubUser returns the user name of the current user.
func GitHubUser(c GitHubClient) (string, error) {
	query := `
	query {
		viewer {
			login
		}
	}
	`
	out, err := c.GraphQLQuery(query, nil)
	if err != nil {
		return "", err
	}
	return out.Viewer.Login, nil
}

// GitHubAddIssueComment is equivalent to [github.Client.AddIssueComment], but
// returns the URL of the new comment.
func GitHubAddIssueComment(c GitHubClient, issue *github.Issue, text string) (url string, err error) {
	graphql := `
	  mutation($ID: ID!, $Text: String!) {
	    addComment(input: {subjectId: $ID, body: $Text}) {
	      clientMutationId
		  commentEdge {
			node {
			  url
			}
		  }
	    }
	  }
	`
	m, err := c.GraphQLMutation(graphql, github.Vars{"ID": issue.ID, "Text": text})
	if err != nil {
		return "", err
	}
	return string(m.AddComment.CommentEdge.Node.Url), nil
}
