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
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	verbose = flag.Bool("v", false, "print commands being run")
	verDir  = flag.String("dir", defaultVerDir(), "`directory` of saved Go roots")
	noDedup = flag.Bool("no-dedup", false, "disable deduplication of saved trees")
)

// TODO: Is this is sane default? If your working directory is another
// Go tree and you do a 'save' or a 'build', this is probably
// surprising.
var goroot = runtime.GOROOT()

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

func main() {
	log.SetFlags(0)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  %s [flags] save [name] - save current build\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] <name> <args>... - run go <args> using build <name>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] run <name> <command>... - run <command> using build <name>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] build [name] - build and save current version\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] list - list saved builds\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s [flags] clean - clean the deduplication cache", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nFlags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
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
		savePath, hashExists := getSavePath(hash)

		nameExists, nameRight := false, true
		if name != "" {
			st2, err := os.Stat(filepath.Join(*verDir, name))
			nameExists = err == nil && st2.IsDir()
			if nameExists {
				st, _ := os.Stat(savePath)
				nameRight = os.SameFile(st, st2)
			}
		}

		if flag.Arg(0) == "build" {
			if hashExists {
				if !nameRight {
					log.Fatalf("name `%s' exists and refers to another build", name)
				}
				msg := fmt.Sprintf("saved build `%s' already exists", hash)
				if !nameExists {
					doLink(hash, name)
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
		doLink(hash, name)
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

	case "clean":
		if flag.NArg() > 1 {
			flag.Usage()
			os.Exit(2)
		}
		doClean()

	default:
		if flag.NArg() < 2 {
			flag.Usage()
			os.Exit(2)
		}
		if _, ok := getSavePath(flag.Arg(0)); !ok {
			log.Fatalf("unknown name or subcommand `%s'", flag.Arg(0))
		}
		doRun(flag.Arg(0), append([]string{"go"}, flag.Args()[1:]...))
	}
}

func gitCmd(cmd string, args ...string) string {
	args = append([]string{"-C", goroot, cmd}, args...)
	c := exec.Command("git", args...)
	c.Stderr = os.Stderr
	output, err := c.Output()
	if err != nil {
		log.Fatalf("error executing git %s: %s", strings.Join(args, " "), err)
	}
	return string(output)
}

func getSavePath(name string) (string, bool) {
	savePath := filepath.Join(*verDir, name)
	st, err := os.Stat(savePath)
	return savePath, err == nil && st.IsDir()
}

func getHash() (string, []byte) {
	rev := strings.TrimSpace(string(gitCmd("rev-parse", "--short", "HEAD")))

	diff := []byte(gitCmd("diff", "HEAD"))

	if len(bytes.TrimSpace(diff)) > 0 {
		diffHash := fmt.Sprintf("%x", sha1.Sum(diff))
		return rev + "+" + diffHash[:10], diff
	}
	return rev, nil
}

func doBuild() {
	c := exec.Command("./make.bash")
	c.Dir = filepath.Join(goroot, "src")
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		log.Fatalf("error executing make.bash: %s", err)
		os.Exit(1)
	}
}

func doSave(hash string, diff []byte) {
	// Create a minimal GOROOT at $GOROOT/gover/hash.
	savePath, _ := getSavePath(hash)
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

func doLink(hash, name string) {
	if name != "" && name != hash {
		savePath, _ := getSavePath(name)
		err := os.Symlink(hash, savePath)
		if err != nil {
			log.Fatal(err)
		}
	}
}

type commit struct {
	authorDate time.Time
	topLine    string
}

func parseCommit(obj []byte) commit {
	out := commit{}
	lines := strings.Split(string(obj), "\n")
	for i, line := range lines {
		if strings.HasPrefix(line, "author ") {
			fs := strings.Fields(line)
			secs, err := strconv.ParseInt(fs[len(fs)-2], 10, 64)
			if err != nil {
				log.Fatalf("malformed author in commit: %s", err)
			}
			out.authorDate = time.Unix(secs, 0)
		}
		if len(line) == 0 {
			out.topLine = lines[i+1]
			break
		}
	}
	return out
}

type saveInfo struct {
	base   string
	names  []string
	commit commit
}

type saveInfoSorter []*saveInfo

func (s saveInfoSorter) Len() int {
	return len(s)
}

func (s saveInfoSorter) Less(i, j int) bool {
	return s[i].commit.authorDate.Before(s[j].commit.authorDate)
}

func (s saveInfoSorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func doList() {
	files, err := ioutil.ReadDir(*verDir)
	if os.IsNotExist(err) {
		return
	} else if err != nil {
		log.Fatal(err)
	}

	baseMap := make(map[string]*saveInfo)
	bases := []*saveInfo{}
	for _, file := range files {
		if !file.IsDir() || file.Name() == "_dedup" {
			continue
		}
		info := &saveInfo{base: file.Name(), names: []string{}}
		baseMap[file.Name()] = info
		bases = append(bases, info)

		commit, err := ioutil.ReadFile(filepath.Join(*verDir, file.Name(), "commit"))
		if os.IsNotExist(err) {
			continue
		}
		info.commit = parseCommit(commit)
	}
	for _, file := range files {
		if file.Mode()&os.ModeType == os.ModeSymlink {
			base, err := os.Readlink(filepath.Join(*verDir, file.Name()))
			if err != nil {
				continue
			}
			if info, ok := baseMap[base]; ok {
				info.names = append(info.names, file.Name())
			}
		}
	}

	sort.Sort(saveInfoSorter(bases))

	for _, info := range bases {
		fmt.Print(info.base)
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
	savePath, ok := getSavePath(name)
	if !ok {
		log.Fatalf("unknown name `%s'", name)
	}

	c := exec.Command(filepath.Join(savePath, "bin", cmd[0]), cmd[1:]...)
	c.Env = append([]string(nil), os.Environ()...)
	c.Env = append(c.Env, "GOROOT="+savePath)

	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := c.Run(); err != nil {
		fmt.Printf("command failed: %s\n", err)
		os.Exit(1)
	}
}

var goodDedupPath = regexp.MustCompile("/[0-9a-f]{2}/[0-9a-f]{38}$")

func doClean() {
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
