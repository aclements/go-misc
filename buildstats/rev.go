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
	"time"
)

type rev struct {
	path string
	date time.Time

	revMeta
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

	// Filter the paths down without additional I/O.
	var revs []*rev
	for _, dir := range dirs {
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

		path := filepath.Join(revDir, dir.Name())
		revs = append(revs, &rev{
			path: path,
			date: t,
		})
	}

	// Load revision metadata.
	for i, rev := range revs {
		fmt.Fprintf(os.Stderr, "\rLoading rev %d/%d...", i+1, len(revs))
		rev.revMeta = readMeta(rev.path)
	}
	fmt.Fprintf(os.Stderr, "\n")

	return revs
}

func (r *rev) String() string {
	return r.path
}

type revMeta struct {
	Repo     string   `json:"repo"`
	Builders []string `json:""`
	Results  []string `json:"results"`
}

func readMeta(revPath string) revMeta {
	var meta revMeta

	path := filepath.Join(revPath, ".rev.json")
	b, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err = json.Unmarshal(b, &meta); err != nil {
		log.Fatalf("decoding %s: %s", path, err)
	}

	path = filepath.Join(revPath, ".builders.json")
	b, err = ioutil.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	if err = json.Unmarshal(b, &meta.Builders); err != nil {
		log.Fatalf("decoding %s: %s", path, err)
	}

	return meta
}

func (r *rev) getLogPath(builder string) (string, error) {
	p := filepath.Join(r.path, builder)
	target, err := os.Readlink(p)
	if err != nil {
		return "", fmt.Errorf("error getting log path: %e", err)
	}
	return filepath.Clean(filepath.Join(p, target)), nil
}

func (r *rev) readLog(builder string) ([]byte, error) {
	path, err := r.getLogPath(builder)
	if err != nil {
		return nil, err
	}
	return ioutil.ReadFile(path)
}
