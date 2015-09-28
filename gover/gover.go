// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/sha1"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	verbose = flag.Bool("v", false, "print commands being run")
)

var goroot = runtime.GOROOT()

var binTools = []string{"go", "godoc", "gofmt"}

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n  %s [flags] save [name]\n  %s [flags] run name command...\n\nFlags:\n", os.Args[0], os.Args[0])
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	switch flag.Arg(0) {
	case "save":
		if flag.NArg() > 2 {
			flag.Usage()
			os.Exit(2)
		}
		hash, diff := getHash()
		name := ""
		if flag.NArg() >= 2 {
			name = flag.Arg(1)
		}
		doSave(name, hash, diff)

	case "run":
		if flag.NArg() < 3 {
			flag.Usage()
			os.Exit(2)
		}
		doRun(flag.Arg(1), flag.Args()[2:])

	default:
		flag.Usage()
		os.Exit(2)
	}
}

func getHash() (string, []byte) {
	c := exec.Command("git", "-C", goroot, "rev-parse", "--short", "HEAD")
	out, err := c.CombinedOutput()
	if err != nil {
		log.Fatalf("git error %s: %s", err, out)
	}

	rev := strings.TrimSpace(string(out))

	c = exec.Command("git", "-C", goroot, "diff", "HEAD")
	out, err = c.CombinedOutput()
	if err != nil {
		log.Fatal("git error %s: %s", err, out)
	}

	if len(bytes.TrimSpace(out)) > 0 {
		diffHash := fmt.Sprintf("%x", sha1.Sum(out))
		return rev + "+" + diffHash[:10], out
	}
	return rev, nil
}

func doSave(name string, hash string, diff []byte) {
	// Create a minimal GOROOT at $GOROOT/gover/hash.
	savePath := filepath.Join(goroot, "gover", hash)
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if x := os.Getenv("GOOS"); x != "" {
		goos = x
	}
	if x := os.Getenv("GOARCH"); x != "" {
		goarch = x
	}
	osArch := goos + "_" + goarch

	for _, binTool := range binTools {
		src := filepath.Join(goroot, "bin", binTool)
		if _, err := os.Stat(src); err == nil {
			cp(src, filepath.Join(savePath, "bin", binTool))
		}
	}
	cpR(filepath.Join(goroot, "pkg", osArch), filepath.Join(savePath, "pkg", osArch))
	cpR(filepath.Join(goroot, "pkg", "tool", osArch), filepath.Join(savePath, "pkg", "tool", osArch))
	cpR(filepath.Join(goroot, "src"), filepath.Join(savePath, "src"))

	if diff != nil {
		if err := ioutil.WriteFile(filepath.Join(savePath, "diff"), diff, 0666); err != nil {
			log.Fatal(err)
		}
	}

	// If there's a name, symlink it under that name.
	if name != "" {
		err := os.Symlink(hash, filepath.Join(goroot, "gover", name))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func doRun(name string, cmd []string) {
	savePath := filepath.Join(goroot, "gover", name)

	c := exec.Command(filepath.Join(savePath, "bin", cmd[0]), cmd[1:]...)
	c.Env = append([]string(nil), os.Environ()...)
	c.Env = append(c.Env, "GOROOT="+savePath)

	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Printf("command failed: %s\n", err)
		os.Exit(1)
	}
}

func cp(src, dst string) {
	if *verbose {
		fmt.Printf("cp %s %s\n", src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
		log.Fatal(err)
	}
	data, err := ioutil.ReadFile(src)
	if err != nil {
		log.Fatal(err)
	}
	st, err := os.Stat(src)
	if err != nil {
		log.Fatal(err)
	}
	if err := ioutil.WriteFile(dst, data, st.Mode()); err != nil {
		log.Fatal(err)
	}
}

func cpR(src, dst string) {
	filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		base := filepath.Base(path)
		if base == "core" || strings.HasSuffix(base, ".test") {
			return nil
		}

		cp(path, dst+path[len(src):])
		return nil
	})
}
