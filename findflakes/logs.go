// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/build/types"
)

type Revision struct {
	types.BuildRevision
	Date time.Time

	Builds []*Build

	path string
}

func (r *Revision) String() string {
	// Use time format from dashboard, plus year.
	return fmt.Sprintf("%s %s", r.Revision[:7], r.Date.Format("02 Jan 15:04 2006"))
}

func (r *Revision) Subject() string {
	subject := r.Desc
	if i := strings.Index(subject, "\n"); i >= 0 {
		subject = subject[:i]
	}
	return subject
}

func (r *Revision) OneLine() string {
	return fmt.Sprintf("%s %s", r.Revision[:7], r.Subject())
}

type Build struct {
	Revision *Revision
	Builder  string
	Status   BuildStatus
	LogURL   string
}

type BuildStatus int

const (
	BuildOK BuildStatus = iota
	BuildRunning
	BuildFailed
)

func (b *Build) LogPath() string {
	return filepath.Join(b.Revision.path, b.Builder)
}

func (b *Build) ReadLog() ([]byte, error) {
	return ioutil.ReadFile(b.LogPath())
}

// LoadRevisions loads all saved build revisions from revDir, which
// must be the "rev" directory written by fetchlogs. The returned
// revisions are ordered from oldest to newest.
func LoadRevisions(revDir string) ([]*Revision, error) {
	revFiles, err := ioutil.ReadDir(revDir)
	if err != nil {
		return nil, err
	}

	revs := []*Revision{}
	for _, revFile := range revFiles {
		if !revFile.IsDir() {
			continue
		}

		rev := &Revision{path: filepath.Join(revDir, revFile.Name())}

		// Load revision metadata.
		var builders []string
		err1 := readJSONFile(filepath.Join(rev.path, ".rev.json"), &rev.BuildRevision)
		err2 := readJSONFile(filepath.Join(rev.path, ".builders.json"), &builders)
		if os.IsNotExist(err1) || os.IsNotExist(err2) {
			continue
		} else if err1 != nil {
			return nil, err1
		} else if err2 != nil {
			return nil, err2
		}

		rev.Date, err = time.Parse(time.RFC3339, rev.BuildRevision.Date)
		if err != nil {
			return nil, err
		}

		rev.Builds = make([]*Build, len(builders))
		for i, builder := range builders {
			var status BuildStatus
			var logURL string
			s := rev.Results[i]
			switch s {
			case "ok":
				status = BuildOK
			case "":
				status = BuildRunning
			default:
				status = BuildFailed
				logURL = s
			}
			rev.Builds[i] = &Build{
				Revision: rev,
				Builder:  builder,
				Status:   status,
				LogURL:   logURL,
			}
		}

		revs = append(revs, rev)
	}

	return revs, nil
}

func readJSONFile(path string, v interface{}) error {
	r, err := os.Open(path)
	if err != nil {
		return err
	}
	defer r.Close()

	return json.NewDecoder(r).Decode(&v)
}
