// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aclements/go-misc/bench"
)

func TestPickSpread(t *testing.T) {
	run.iterations = 5

	for iter := 0; iter < 10; iter++ {
		commits := []*commitInfo{}
		for i := 0; i < 100; i++ {
			commits = append(commits, &commitInfo{})
		}

		for {
			commit := pickCommitSpread(commits)
			if commit == nil {
				break
			}

			if rand.Intn(50) == 0 {
				commit.buildFailed = true
			} else if rand.Intn(50) == 1 {
				commit.fails++
			} else {
				commit.count++
			}
		}

		// Test that all of the commits ran the expected
		// number of times.
		for _, c := range commits {
			if c.runnable() {
				t.Fatalf("commit still runnable %+v", c)
			}
		}
	}
}

func TestRun(t *testing.T) {
	// Create a git repo for testing.
	repo, err := ioutil.TempDir("", "benchmany-test")
	if err != nil {
		t.Fatal("creating temp dir: ", err)
	}
	defer os.RemoveAll(repo)
	tgit(t, repo, "init")
	tgit(t, repo, "config", "user.name", "gopher")
	tgit(t, repo, "config", "user.email", "gopher@example.com")

	// Write benchmark.
	err = ioutil.WriteFile(filepath.Join(repo, "x_test.go"), []byte(`
package main

import "testing"

func TestMain(m *testing.M) {
	println("BenchmarkX 1 100 ns/op")
}`), 0666)
	if err != nil {
		t.Fatal("writing x_test.go: ", err)
	}
	tgit(t, repo, "add", "x_test.go")
	tgit(t, repo, "commit", "-m", "initial")

	// Create several commits.
	var revs []string
	for i := 0; i < 3; i++ {
		str := fmt.Sprintf("%d", i)
		err = ioutil.WriteFile(filepath.Join(repo, "x"), []byte(str), 0666)
		if err != nil {
			t.Fatal("writing x: ", err)
		}
		tgit(t, repo, "add", "x")
		tgit(t, repo, "commit", "-m", str)
		revs = append(revs, trimNL(tgit(t, repo, "rev-parse", "HEAD")))
	}

	for iters := 4; iters <= 5; iters++ {
		// Run benchmark.
		tgit(t, repo, "checkout", "master")
		oldArgs := os.Args
		oldWD, err := os.Getwd()
		if err != nil {
			t.Fatal("Getwd: ", err)
		}
		os.Args = []string{os.Args[0], "-n", fmt.Sprintf("%d", iters), "HEAD~3..HEAD"}
		os.Chdir(repo)
		defer func() {
			os.Args = oldArgs
			os.Chdir(oldWD)
		}()
		main()

		// Check results.
		f, err := os.Open(filepath.Join(repo, "bench.log"))
		if err != nil {
			t.Fatal("opening bench.log: ", err)
		}
		defer f.Close()
		bs, err := bench.Parse(f)
		if err != nil {
			t.Fatal("malformed benchmark log: ", err)
		}
		counts := make(map[string]int)
		for _, b := range bs {
			t.Log(b, b.Config["commit"].RawValue)
			counts[b.Config["commit"].RawValue]++
		}
		for _, rev := range revs {
			if counts[rev] != iters {
				t.Errorf("expected %d results for %s, got %d", iters, rev, counts[rev])
			}
		}
	}
}

func tgit(t *testing.T, repo string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", args, err, out)
	}
	return string(out)
}
