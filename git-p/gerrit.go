// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"
)

// GerritChangeInfo is the JSON struct returned by a Gerrit CL query.
type GerritChangeInfo struct {
	ID                     string
	Project                string
	Branch                 string
	ChangeId               string `json:"change_id"`
	Subject                string
	Status                 string
	Created                string
	Updated                string
	Mergeable              bool
	Submittable            bool // Requires SUBMITTABLE
	Insertions             int
	Deletions              int
	UnresolvedCommentCount int `json:"unresolved_comment_count"`
	Number                 int `json:"_number"`
	Owner                  *GerritAccount
	Labels                 map[string]*GerritLabel    ``                        // Requires LABELS or DETAILED_LABELS
	CurrentRevision        string                     `json:"current_revision"` // Requires CURRENT_REVISION or ALL_REVISIONS
	Revisions              map[string]*GerritRevision ``                        // Requires CURRENT_REVISION or ALL_REVISIONS
	Messages               []*GerritChangeMessageInfo ``                        // Requires MESSAGES
}

// GerritChangeMessageInfo is the JSON struct for a Gerrit ChangeMessageInfo.
type GerritChangeMessageInfo struct {
	Author   *GerritAccount
	Message  string
	PatchSet int `json:"_revision_number"`
	Tag      string
}

// GerritLabel is the JSON struct for a Gerrit LabelInfo.
type GerritLabel struct {
	Optional bool
	Blocking bool
	Approved *GerritAccount
	Rejected *GerritAccount
	All      []*GerritApproval
}

// GerritAccount is the JSON struct for a Gerrit AccountInfo.
type GerritAccount struct {
	ID       int    `json:"_account_id"` // Requires DETAILED_ACCOUNTS
	Name     string // Requires DETAILED_ACCOUNTS
	Email    string // Requires DETAILED_ACCOUNTS
	Username string // Requires DETAILED_ACCOUNTS
}

// GerritApproval is the JSON struct for a Gerrit ApprovalInfo.
type GerritApproval struct {
	GerritAccount
	Value int
	Date  string
}

// GerritRevision is the JSON struct for a Gerrit RevisionInfo.
type GerritRevision struct {
	Number int `json:"_number"`
	Ref    string
}

type Gerrit struct {
	url     string
	project string
	req     chan<- *GerritChanges
}

func NewGerrit(gerritUrl string) (*Gerrit, error) {
	url, err := url.Parse(gerritUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to parse origin URL %q: %w", gerritUrl, err)
	}
	suf := ".googlesource.com"
	if !strings.HasSuffix(url.Host, suf) {
		return nil, fmt.Errorf("origin URL %q host does not end in %q (not Gerrit?)", url, suf)
	}
	if url.Scheme != "https" {
		return nil, fmt.Errorf("origin URL %q must be https", url)
	}
	// Remove trailing slash from the origin, if any.
	url.Path = strings.TrimRight(url.Path, "/")
	// The path is now the project name (with a leading /).
	if url.Path == "" || strings.Contains(url.Path[1:], "/") {
		return nil, fmt.Errorf("origin URL %q path must be a single non-empty project name", url)
	}
	project := url.Path[1:]
	// Drop the project from the URL
	url.Path = ""
	// The API host adds "-review".
	i := len(url.Host) - len(suf)
	url.Host = url.Host[:i] + "-review" + url.Host[i:]

	ch := make(chan *GerritChanges, 10)
	g := &Gerrit{url.String(), project, ch}
	go func() {
		done := false
		for !done {
			// Pull queries off the channel in batches of
			// up to 10 (Gerrit's limit). Wait a tiny
			// amount of time to get a batch.
			var batch []*GerritChanges
			timeout := time.After(1 * time.Millisecond)
		loop:
			for len(batch) < 10 {
				select {
				case req, ok := <-ch:
					if !ok {
						done = true
						break loop
					}
					batch = append(batch, req)
				case <-timeout:
					break loop
				}
			}

			if len(batch) > 0 {
				g.queryChanges(batch)
			}
		}
	}()
	return g, nil
}

type GerritChanges struct {
	query   string
	options []string

	result []*GerritChangeInfo
	err    error
	done   chan struct{}
}

func (req *GerritChanges) Wait() ([]*GerritChangeInfo, error) {
	<-req.done
	return req.result, req.err
}

func (g *Gerrit) QueryChanges(query string, options ...string) *GerritChanges {
	req := &GerritChanges{query: query, options: options, done: make(chan struct{})}
	g.req <- req
	return req
}

func (g *Gerrit) queryChanges(queries []*GerritChanges) {
	// Split up queries by consistent options.
	subs := make([][]*GerritChanges, 1)
	options := queries[0].options
	for _, q := range queries {
		if !reflect.DeepEqual(q.options, options) {
			subs = append(subs, nil)
			options = q.options
		}
		subs[len(subs)-1] = append(subs[len(subs)-1], q)
	}
	for _, subQueries := range subs {
		g.queryChanges1(subQueries, subQueries[0].options)
	}
}

func (g *Gerrit) queryChanges1(queries []*GerritChanges, options []string) {
	failAll := func(err error) {
		for _, q := range queries {
			q.err = err
			close(q.done)
		}
	}

	// Construct query.
	var queryParams []string
	for _, q := range queries {
		queryParams = append(queryParams, "q="+url.QueryEscape(q.query))
	}
	for _, opt := range options {
		queryParams = append(queryParams, "o="+opt)
	}
	queryUrl := g.url + "/changes/?" + strings.Join(queryParams, "&")

	// Get results.
	resp, err := http.Get(queryUrl)
	if err != nil {
		failAll(err)
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		failAll(err)
		return
	}
	if resp.StatusCode != http.StatusOK {
		failAll(fmt.Errorf("%s: %s", queryUrl, resp.Status))
		return
	}
	i := bytes.IndexByte(body, '\n')
	if i < 0 {
		failAll(fmt.Errorf("%s: malformed json response", queryUrl))
		return
	}
	body = body[i:]
	var target interface{}
	var changes [][]*GerritChangeInfo
	if len(queries) == 1 {
		changes = make([][]*GerritChangeInfo, 1)
		target = &changes[0]
	} else {
		target = &changes
	}
	if err := json.Unmarshal(body, target); err != nil {
		failAll(fmt.Errorf("%s: malformed json response", queryUrl))
		return
	}
	if debugGerrit {
		r, _ := json.MarshalIndent(target, "", "    ")
		log.Printf("GET %s =>\n%s", queryUrl, r)
	}
	if len(changes) != len(queries) {
		failAll(fmt.Errorf("%s: made %d queries, but got %d responses", queryUrl, len(queries), len(changes)))
		return
	}

	// Complete requests.
	for i, q := range queries {
		q.result = changes[i]
		close(q.done)
	}
}
