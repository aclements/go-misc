// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
)

type rev struct {
	path string
	date time.Time

	metaOnce sync.Once
	meta     revMeta
}

var pathDateRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})-[0-9a-f]+$`)

func getRevs(since time.Time) []*rev {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		log.Fatal("getting cache directory: ", err)
	}
	revDir := filepath.Join(cacheDir, "fetchlogs", "rev")
	dirs, err := os.ReadDir(revDir)
	if err != nil {
		log.Fatalf("reading rev directory %s: %s", revDir, err)
	}

	var matches []*rev
	for i, dir := range dirs {
		fmt.Fprintf(os.Stderr, "\rLoading rev %d/%d...", i+1, len(dirs))
		if !dir.IsDir() {
			continue
		}
		name := dir.Name()
		m := pathDateRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		t, err := time.Parse(rfc3339DateTime, m[1])
		if err != nil {
			continue
		}
		if t.Before(since) {
			continue
		}
		matches = append(matches, &rev{
			path: filepath.Join(revDir, name),
			date: t,
		})
	}
	fmt.Fprintf(os.Stderr, "\n")

	return matches
}

func (r *rev) String() string {
	return r.path
}

type revMeta struct {
	Repo     string   `json:"repo"`
	Builders []string // not in JSON
	Results  []string `json:"results"`
}

func (r *rev) getMeta() revMeta {
	r.metaOnce.Do(func() {
		path := filepath.Join(r.path, ".rev.json")
		b, err := ioutil.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if err = json.Unmarshal(b, &r.meta); err != nil {
			log.Fatalf("decoding %s: %s", path, err)
		}

		path = filepath.Join(r.path, ".builders.json")
		b, err = ioutil.ReadFile(path)
		if err != nil {
			log.Fatal(err)
		}
		if err = json.Unmarshal(b, &r.meta.Builders); err != nil {
			log.Fatalf("decoding %s: %s", path, err)
		}
	})

	return r.meta
}
