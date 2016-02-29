// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command gover manages saved versions of the Go tree.
//
// gover saves builds of the Go source tree and runs commands using
// saved Go versions. For example,
//
//     cd $GOROOT
//     git checkout go1.5.1
//     gover build 1.5.1
//
// will checkout Go 1.5.1, build the source tree, and save it under
// the name "1.5.1", as well as its commit hash (f2e4c8b). You can
// then later run commands with Go 1.5.1. For example, the following
// will run "go install" using Go 1.5.1:
//
//     gover 1.5.1 install
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
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
)

var (
	verbose    = flag.Bool("v", false, "print commands being run")
	verDir     = flag.String("dir", defaultVerDir(), "`directory` of saved Go roots")
	noDedup    = flag.Bool("no-dedup", false, "disable deduplication of saved trees")
	gorootFlag = flag.String("C", defaultGoroot(), "use `dir` as the root of the Go tree for save and build")
)

var binTools = []string{"go", "godoc", "gofmt"}

func defaultVerDir() string {
	cache := os.Getenv("XDG_CACHE_HOME")
	if cache == "" {
		home := os.Getenv("HOME")
		if home == "" {
			u, err := user.Current()
			if err != nil {
				home = u.HomeDir
			}
		}
		cache = filepath.Join(home, ".cache")
	}
	return filepath.Join(cache, "gover")
}

func defaultGoroot() string {
	c := exec.Command("git", "rev-parse", "--show-cdup")
	output, err := c.Output()
	if err != nil {
		return ""
	}
	goroot := strings.TrimSpace(string(output))
	if goroot == "" {
		// The empty string is --show-cdup's helpful way of
		// saying "the current directory".
		goroot = "."
	}
	if !isGoroot(goroot) {
		return ""
	}
	return goroot
}

// isGoroot returns true if path is the root of a Go tree. It is
// somewhat heuristic.
func isGoroot(path string) bool {
	st, err := os.Stat(filepath.Join(path, "src", "cmd", "go"))
	return err == nil && st.IsDir()
}

func main() {
	log.SetFlags(0)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags] save [name] - save current build\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] <name> <args>... - run go <args> using build <name>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] run <name> <command>... - run <command> using PATH and GOROOT for build <name>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] env <name> - print the environment for build <name> as shell code\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] build [name] - build and save current version\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] list - list saved builds\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] gc - clean the deduplication cache", os.Args[0])
		fmt.Fprintf(os.Stderr, "\n\n")
		fmt.Fprintf(os.Stderr, "<name> may be an unambiguous commit hash or a string name.\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}

	// Make gorootFlag absolute.
	if *gorootFlag != "" {
		abs, err := filepath.Abs(*gorootFlag)
		if err != nil {
			*gorootFlag = abs
		}
	}

	switch flag.Arg(0) {
	case "save", "build":
		if flag.NArg() > 2 {
			flag.Usage()
			os.Exit(2)
		}
		hash, diff := getHash()
		name := ""
		if flag.NArg() >= 2 {
			name = flag.Arg(1)
			if name == hash {
				name = ""
			}
		}

		// Validate paths.
		savePath, hashExists := resolveName(hash)

		namePath, nameExists, nameRight := "", false, true
		if name != "" && name != hash {
			namePath, nameExists = resolveName(name)
			if nameExists {
				st1, _ := os.Stat(savePath)
				st2, _ := os.Stat(namePath)
				nameRight = os.SameFile(st1, st2)
			}
		}

		if flag.Arg(0) == "build" {
			if hashExists {
				if !nameRight {
					log.Fatalf("name `%s' exists and refers to another build", name)
				}
				msg := fmt.Sprintf("saved build `%s' already exists", hash)
				if namePath != "" && !nameExists {
					doLink(hash, namePath)
					msg += fmt.Sprintf("; added name `%s'", name)
				}
				fmt.Fprintln(os.Stderr, msg)
				os.Exit(0)
			}

			doBuild()
		} else {
			if hashExists {
				log.Fatalf("saved build `%s' already exists", hash)
			}
			if nameExists {
				log.Fatalf("saved build `%s' already exists", name)
			}
		}
		doSave(hash, diff)
		if namePath != "" {
			doLink(hash, namePath)
		}
		if name == "" {
			fmt.Fprintf(os.Stderr, "saved build as `%s'\n", hash)
		} else {
			fmt.Fprintf(os.Stderr, "saved build as `%s' and `%s'\n", hash, name)
		}

	case "list":
		if flag.NArg() > 1 {
			flag.Usage()
			os.Exit(2)
		}
		doList()

	case "run":
		if flag.NArg() < 3 {
			flag.Usage()
			os.Exit(2)
		}
		doRun(flag.Arg(1), flag.Args()[2:])

	case "env":
		if flag.NArg() != 2 {
			flag.Usage()
			os.Exit(2)
		}
		doEnv(flag.Arg(1))

	case "gc":
		if flag.NArg() > 1 {
			flag.Usage()
			os.Exit(2)
		}
		doGC()

	default:
		if flag.NArg() < 2 {
			flag.Usage()
			os.Exit(2)
		}
		if _, ok := resolveName(flag.Arg(0)); !ok {
			log.Fatalf("unknown name or subcommand `%s'", flag.Arg(0))
		}
		doRun(flag.Arg(0), append([]string{"go"}, flag.Args()[1:]...))
	}
}

func goroot() string {
	if *gorootFlag == "" {
		log.Fatal("not a git repository")
	}
	return *gorootFlag
}

func gitCmd(cmd string, args ...string) string {
	args = append([]string{"-C", goroot(), cmd}, args...)
	c := exec.Command("git", args...)
	c.Stderr = os.Stderr
	output, err := c.Output()
	if err != nil {
		log.Fatalf("error executing git %s: %s", strings.Join(args, " "), err)
	}
	return string(output)
}

func getHash() (string, []byte) {
	rev := strings.TrimSpace(string(gitCmd("rev-parse", "HEAD")))

	diff := []byte(gitCmd("diff", "HEAD"))

	if len(bytes.TrimSpace(diff)) > 0 {
		diffHash := fmt.Sprintf("%x", sha1.Sum(diff))
		return rev + "+" + diffHash[:10], diff
	}
	return rev, nil
}

func doBuild() {
	c := exec.Command("./make.bash")
	c.Dir = filepath.Join(goroot(), "src")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		log.Fatalf("error executing make.bash: %s", err)
		os.Exit(1)
	}
}

func doSave(hash string, diff []byte) {
	// Create a minimal GOROOT at $GOROOT/gover/hash.
	savePath, _ := resolveName(hash)
	goos, goarch := runtime.GOOS, runtime.GOARCH
	if x := os.Getenv("GOOS"); x != "" {
		goos = x
	}
	if x := os.Getenv("GOARCH"); x != "" {
		goarch = x
	}
	osArch := goos + "_" + goarch

	goroot := goroot()
	for _, binTool := range binTools {
		src := filepath.Join(goroot, "bin", binTool)
		if _, err := os.Stat(src); err == nil {
			cp(src, filepath.Join(savePath, "bin", binTool))
		}
	}
	cpR(filepath.Join(goroot, "pkg", osArch), filepath.Join(savePath, "pkg", osArch))
	cpR(filepath.Join(goroot, "pkg", "tool", osArch), filepath.Join(savePath, "pkg", "tool", osArch))
	cpR(filepath.Join(goroot, "pkg", "include"), filepath.Join(savePath, "pkg", "include"))
	cpR(filepath.Join(goroot, "src"), filepath.Join(savePath, "src"))

	if diff != nil {
		if err := ioutil.WriteFile(filepath.Join(savePath, "diff"), diff, 0666); err != nil {
			log.Fatal(err)
		}
	}

	// Save commit object.
	commit := gitCmd("cat-file", "commit", "HEAD")
	if err := ioutil.WriteFile(filepath.Join(savePath, "commit"), []byte(commit), 0666); err != nil {
		log.Fatal(err)
	}
}

func doLink(hash, namePath string) {
	err := os.Symlink(hash, namePath)
	if err != nil {
		log.Fatal(err)
	}
}

type buildInfoSorter []*buildInfo

func (s buildInfoSorter) Len() int {
	return len(s)
}

func (s buildInfoSorter) Less(i, j int) bool {
	return s[i].commit.authorDate.Before(s[j].commit.authorDate)
}

func (s buildInfoSorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func doList() {
	builds, err := listBuilds(listNames | listCommit)
	if err != nil {
		log.Fatal(err)
	}

	sort.Sort(buildInfoSorter(builds))

	for _, info := range builds {
		fmt.Print(info.fullName())
		if !info.commit.authorDate.IsZero() {
			fmt.Printf(" %s", info.commit.authorDate.Local().Format("2006-01-02T15:04:05"))
		}
		if len(info.names) > 0 {
			fmt.Printf(" %s", info.names)
		}
		if info.commit.topLine != "" {
			fmt.Printf(" %s", info.commit.topLine)
		}
		fmt.Println()
	}
}

func doRun(name string, cmd []string) {
	savePath, ok := resolveName(name)
	if !ok {
		log.Fatalf("unknown name `%s'", name)
	}
	goroot, path := getEnv(savePath)

	// exec.Command looks up the command in this process' PATH.
	// Unfortunately, this is a rather complex process and there's
	// no way to provide a different PATH, so set the process'
	// PATH.
	os.Setenv("PATH", path)
	c := exec.Command(cmd[0], cmd[1:]...)

	// Build the rest of the command environment.
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "GOROOT=") {
			continue
		}
		c.Env = append(c.Env, env)
	}
	c.Env = append(c.Env, "GOROOT="+goroot)

	// Run command.
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Printf("command failed: %s\n", err)
		os.Exit(1)
	}
}

func doEnv(name string) {
	savePath, ok := resolveName(name)
	if !ok {
		log.Fatalf("unknown name `%s'", name)
	}

	goroot, path := getEnv(savePath)
	fmt.Printf("PATH=%s;\n", shellEscape(path))
	fmt.Printf("GOROOT=%s;\n", shellEscape(goroot))
	fmt.Printf("export GOROOT;\n")
}

// getEnv returns the GOROOT and PATH for the Go tree rooted at savePath.
func getEnv(savePath string) (goroot, path string) {
	p := []string{filepath.Join(savePath, "bin")}
	// Strip existing Go tree from PATH.
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if isGoroot(filepath.Join(dir, "..")) {
			continue
		}
		p = append(p, dir)
	}

	return savePath, strings.Join(p, string(filepath.ListSeparator))
}

var goodDedupPath = regexp.MustCompile("/[0-9a-f]{2}/[0-9a-f]{38}$")

func doGC() {
	removed := 0
	filepath.Walk(filepath.Join(*verDir, "_dedup"), func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		if st, err := os.Stat(path); err == nil {
			st, ok := st.Sys().(*syscall.Stat_t)
			if !ok || st.Nlink != 1 {
				return nil
			}
			if !goodDedupPath.MatchString(path) {
				// Be paranoid about removing files.
				log.Printf("unexpected file in dedup cache: %s\n", path)
				return nil
			}
			if err := os.Remove(path); err != nil {
				log.Printf("failed to remove %s: %v", path, err)
			} else {
				removed++
			}
		}
		return nil
	})
	fmt.Printf("removed %d unused file(s)\n", removed)
}

func cp(src, dst string) {
	data, err := ioutil.ReadFile(src)
	if err != nil {
		log.Fatal(err)
	}

	writeFile, xdst := true, dst
	if !*noDedup {
		hash := fmt.Sprintf("%x", sha1.Sum(data))
		xdst = filepath.Join(*verDir, "_dedup", hash[:2], hash[2:])
		if _, err := os.Stat(xdst); err == nil {
			writeFile = false
		}
	}
	if writeFile {
		if *verbose {
			fmt.Printf("cp %s %s\n", src, xdst)
		}
		st, err := os.Stat(src)
		if err != nil {
			log.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(xdst), 0777); err != nil {
			log.Fatal(err)
		}
		if err := ioutil.WriteFile(xdst, data, st.Mode()); err != nil {
			log.Fatal(err)
		}
		if err := os.Chtimes(xdst, st.ModTime(), st.ModTime()); err != nil {
			log.Fatal(err)
		}
	}

	if dst != xdst {
		if *verbose {
			fmt.Printf("ln %s %s\n", xdst, dst)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0777); err != nil {
			log.Fatal(err)
		}
		if err := os.Link(xdst, dst); err != nil {
			log.Fatal(err)
		}
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
