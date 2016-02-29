// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var minHashRe = regexp.MustCompile("^[0-9a-f]{7,40}$")
var fullHashRe = regexp.MustCompile("^[0-9a-f]{40}$")

// resolveName returns the path to the root of the named build and
// whether or not that path exists. It will log an error and exit if
// name is ambiguous. If the path does not exist, the returned path is
// where this build should be saved.
func resolveName(name string) (path string, ok bool) {
	// If the name exactly matches a saved version, return it.
	savePath := filepath.Join(*verDir, name)
	st, err := os.Stat(savePath)
	if err == nil && st.IsDir() {
		return savePath, true
	}

	// Otherwise, try to resolve it as an unambiguous hash prefix.
	if minHashRe.MatchString(name) {
		files, err := ioutil.ReadDir(*verDir)
		if os.IsNotExist(err) {
			return savePath, false
		} else if err != nil {
			log.Fatalf("reading %s: %v", *verDir, err)
		}
		var fullName string
		for _, f := range files {
			if !f.IsDir() || !fullHashRe.MatchString(f.Name()) {
				continue
			}
			if strings.HasPrefix(f.Name(), name) {
				if fullName != "" {
					log.Fatalf("ambiguous name `%s`", name)
				}
				fullName = f.Name()
			}
		}
		if fullName != "" {
			return filepath.Join(*verDir, fullName), true
		}
	}

	return savePath, false
}
