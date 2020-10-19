// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/build/buildlet"
)

var poolPath string

func main() {
	buildlet.RegisterFlags()
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "Usage: %s [flags] <subcommand...>\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(w, "\nSubcommands:\n")
		fmt.Fprintf(w, "  create   create a new buildlet pool\n")
		fmt.Fprintf(w, "  destroy  destroy the buildlet pool\n")
		fmt.Fprintf(w, "  run      run a command with a buildlet from the pool\n")
	}
	flag.StringVar(&poolPath, "pool-path", defaultPoolPath(), "pool state `directory`")
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	cmd, args := flag.Arg(0), flag.Args()[1:]
	switch cmd {
	default:
		flag.Usage()
		os.Exit(2)

	case "create":
		cmdCreate(args)
		return

	case "destroy":
		cmdDestroy(args)
		return

	case "run":
		cmdRun(args)
		return
	}
}

func defaultPoolPath() string {
	const fallback = "/tmp/gopool"
	uid := os.Getuid()
	if uid <= 0 {
		return fallback
	}
	path := fmt.Sprintf("/var/run/user/%d", uid)
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return fallback
	}
	return path + "/gopool"
}

type Config struct {
	Setup struct {
		Cmd string
		Env []string
		Dir string
	}
	Kind string
	Max  int

	Free     []string
	InUse    []string
	Creating []int
}

func (c *Config) dropInUse(name string) {
	for i, name2 := range c.InUse {
		if name == name2 {
			copy(c.InUse[i:], c.InUse[i+1:])
			c.InUse = c.InUse[:len(c.InUse)-1]
			break
		}
	}
}

func (c *Config) dropCreating(pid int) {
	for i, pid2 := range c.Creating {
		if pid == pid2 {
			copy(c.Creating[i:], c.Creating[i+1:])
			c.Creating = c.Creating[:len(c.Creating)-1]
			break
		}
	}
}

func cmdCreate(args []string) {
	var cfg Config

	flags := flag.NewFlagSet("create", flag.ExitOnError)
	flags.StringVar(&cfg.Setup.Cmd, "setup", "", "run shell command `cmd` to set up new instances; $VM will be set to the buildlet name")
	flags.IntVar(&cfg.Max, "max", 10, "create at most `n` buildlets at once")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s create [flags] <type>\n", os.Args[0])
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() != 1 {
		flags.Usage()
		os.Exit(2)
	}
	cfg.Kind = flags.Arg(0)

	err := os.MkdirAll(poolPath, 0777)
	if err != nil {
		log.Fatalf("error creating pool path: %s", err)
	}

	cfg.Setup.Env = os.Environ()
	cfg.Setup.Dir, err = os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	f, err := os.OpenFile(path.Join(poolPath, "config"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0666)
	if os.IsExist(err) {
		log.Fatalf("pool already exists at %s: %s", poolPath, err)
	} else if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(&cfg); err != nil {
		log.Fatalf("writing pool config: %s", err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	touch(path.Join(poolPath, "lock"))
}

type FileLock struct {
	f *os.File
}

func LockFile(path string) (*FileLock, error) {
	lockFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		lockFile.Close()
		return nil, err
	}
	return &FileLock{lockFile}, nil
}

func TryLockFile(path string) (*FileLock, error) {
	lockFile, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, nil
		}
		return nil, err
	}
	return &FileLock{lockFile}, nil
}

func (fl *FileLock) Unlock() {
	fl.f.Close()
	fl.f = nil
}

var coordCache *buildlet.CoordinatorClient
var coordOnce sync.Once

func getCoordinator() *buildlet.CoordinatorClient {
	coordOnce.Do(func() {
		coord, err := buildlet.NewCoordinatorClientFromFlags()
		if err != nil {
			log.Fatalf("error connecting to coordinator: %v", err)
		}
		coordCache = coord
	})
	return coordCache
}

type Pool struct {
	path     string
	lockFile *FileLock
}

type Buildlet struct {
	Name     string
	path     string
	lockFile *FileLock
	client   *buildlet.Client
}

// OpenPool locks the pool and loads its configuration.
func OpenPool(poolPath string) *Pool {
	p := &Pool{path: poolPath}
	p.reap()
	return p
}

func (p *Pool) buildletByName(name string) *Buildlet {
	return &Buildlet{name, path.Join(p.path, name), nil, nil}
}

func (b *Buildlet) Client() *buildlet.Client {
	if b.client == nil {
		var err error
		b.client, err = getCoordinator().NamedBuildlet(b.Name)
		if err != nil {
			log.Fatalf("error getting buildlet %s client: %s", b.Name, err)
		}
	}
	return b.client
}

func (b *Buildlet) lock() {
	lock, err := LockFile(b.path)
	if err != nil {
		log.Fatalf("locking buildlet %s: %s", b.Name, err)
	}
	b.lockFile = lock
}

func (b *Buildlet) tryLock() bool {
	lock, err := TryLockFile(b.path)
	if err != nil {
		log.Fatalf("locking buildlet %s: %s", b.Name, err)
	}
	b.lockFile = lock
	return lock != nil
}

func (b *Buildlet) unlock() {
	b.lockFile.Unlock()
	b.lockFile = nil
}

// lock locks and returns the pool configuration.
func (p *Pool) lock() *Config {
	if p.lockFile != nil {
		panic("pool already locked")
	}

	// Lock the pool.
	lock, err := LockFile(path.Join(poolPath, "lock"))
	if err != nil {
		log.Fatalf("locking pool: %s", err)
	}
	p.lockFile = lock

	// Load config.
	f, err := os.Open(path.Join(poolPath, "config"))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		log.Fatalf("error reading pool config: %s", err)
	}
	return &cfg
}

func (p *Pool) unlock() {
	if p.lockFile == nil {
		panic("pool not locked")
	}

	p.lockFile.Unlock()
	p.lockFile = nil
}

// flush saves the pool state.
func (p *Pool) flush(cfg *Config) {
	// Write config to a temporary file.
	configPath := path.Join(poolPath, "config")
	f, err := os.Create(configPath + ".tmp")
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	if err := json.NewEncoder(f).Encode(cfg); err != nil {
		log.Fatalf("writing pool config: %s", err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	// Commit the new config.
	if err := os.Rename(configPath+".tmp", configPath); err != nil {
		log.Fatal(err)
	}
}

// reap checks for and destroys abandoned buildlets.
func (p *Pool) reap() {
	cfg := p.lock()
	defer p.unlock()

	inUse := append([]string(nil), cfg.InUse...)
	for _, name := range inUse {
		// See if this buildlet is still locked.
		b := p.buildletByName(name)
		if !b.tryLock() {
			continue
		}

		// Found an "in use" buildlet that isn't locked, which
		// means it got abandoned.
		log.Printf("reaping abandoned buildlet %s", name)
		p.discardLocked(cfg, b)
	}

	creating := append([]int(nil), cfg.Creating...)
	for _, pid := range creating {
		cpath := path.Join(p.path, fmt.Sprintf("creating-%d", pid))
		l, err := TryLockFile(cpath)
		if err != nil {
			log.Fatal(err)
		}
		if l == nil {
			continue
		}
		cfg.dropCreating(pid)
		p.flush(cfg)
		l.Unlock()
		os.Remove(cpath)
	}
}

func (p *Pool) discardLocked(cfg *Config, b *Buildlet) {
	// Destroy the buildlet.
	client := b.Client()
	// TODO: Check if the buildlet is still around and retry the Close?
	client.Close()
	cfg.dropInUse(b.Name)
	b.unlock()
	p.flush(cfg)
}

func (p *Pool) Put(b *Buildlet) {
	cfg := p.lock()
	defer p.unlock()
	cfg.Free = append(cfg.Free, b.Name)
	cfg.dropInUse(b.Name)
	b.unlock()
	p.flush(cfg)
}

func (p *Pool) Discard(b *Buildlet) {
	cfg := p.lock()
	defer p.unlock()
	p.discardLocked(cfg, b)
}

func (p *Pool) Get() (*Buildlet, error) {
	const maxCreateTries = 5
	createTries := 0

	cfg := p.lock()
	defer p.unlock()
	for {
		if p.lockFile == nil {
			panic("pool not locked")
		}

		if len(cfg.Free) == 0 {
			if createTries >= maxCreateTries {
				return nil, fmt.Errorf("giving up after %d retries", createTries)
			}
			createTries++

			if len(cfg.InUse)+len(cfg.Creating) >= cfg.Max {
				return nil, fmt.Errorf("reached pool limit of %d gomotes", cfg.Max)
			}

			// Record our intent to create this buildlet.
			pid := os.Getpid()
			cpath := path.Join(p.path, fmt.Sprintf("creating-%d", pid))
			touch(cpath)
			clock, err := LockFile(cpath)
			if err != nil {
				log.Fatal(err)
			}
			cfg.Creating = append(cfg.Creating, pid)
			p.flush(cfg)

			// Start a new gomote.
			//
			// Drop the lock while we're creating the
			// buildlet because this can take a while.
			log.Printf("creating %s buildlet", cfg.Kind)
			p.unlock()
			client, err := getCoordinator().CreateBuildlet(cfg.Kind)
			cfg = p.lock()
			if err != nil {
				log.Printf("error creating buildlet: %s", err)
				continue
			}
			name := client.RemoteName()
			log.Printf("created buildlet %s", name)

			// Add it to the in-use list ASAP so it gets
			// reaped in case something goes wrong during
			// setup. Also drop ourselves from creating.
			cfg.InUse = append(cfg.InUse, name)
			cfg.dropCreating(pid)
			p.flush(cfg)
			clock.Unlock()
			os.Remove(cpath)
			b := p.buildletByName(name)
			touch(b.path)

			// Set it up.
			if cfg.Setup.Cmd != "" {
				cmd := exec.Command("/bin/sh", "-c", cfg.Setup.Cmd)
				cmd.Dir = cfg.Setup.Dir
				cmd.Env = append(cfg.Setup.Env, "VM="+name)
				cmd.Stdout = os.Stdout
				cmd.Stderr = os.Stderr

				p.unlock()
				err := cmd.Run()
				cfg = p.lock()

				if err != nil {
					log.Printf("setup command failed: %s", err)
					client.Close()
					cfg.dropInUse(name)
					p.flush(cfg)
					continue
				}
			}

			// It's now available.
			cfg.Free = append(cfg.Free, name)
			cfg.dropInUse(name)
			p.flush(cfg)
		}

		// Get a buildlet from the free list.
		name := cfg.Free[len(cfg.Free)-1]
		cfg.Free = cfg.Free[:len(cfg.Free)-1]

		b := p.buildletByName(name)
		b.lock()

		// Get buildlet status. (NamedBuildlet doesn't even validate.)
		client := b.Client()
		ctx := context.TODO()
		_, err := client.Status(ctx)
		if err == nil {
			// Ping the buildlet to really check it.
			err = client.ListDir(ctx, ".", buildlet.ListDirOpts{}, func(buildlet.DirEntry) {})
			if err == nil {
				// Found a good one!
				cfg.InUse = append(cfg.InUse, name)
				p.flush(cfg)
				return b, nil
			}
		}

		// Destroy the broken buildlet.
		log.Printf("buildlet %s broken: %s", name, err)
		p.discardLocked(cfg, b)
	}
}

func (p *Pool) Destroy() {
	cfg := p.lock()
	defer p.unlock()

	// Destroy all the buildlets.
	all := append(append([]string(nil), cfg.Free...), cfg.InUse...)
	for _, name := range all {
		log.Printf("destroying %s", name)
		b := p.buildletByName(name)
		client := b.Client()
		client.Close()
	}

	// Destroy the pool.
	if err := os.RemoveAll(p.path); err != nil {
		log.Fatalf("removing pool directory: %s", err)
	}
}

func cmdDestroy(args []string) {
	flags := flag.NewFlagSet("destroy", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s destroy\n", os.Args[0])
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() != 0 {
		flags.Usage()
		os.Exit(2)
	}

	p := OpenPool(poolPath)
	p.Destroy()
}

func cmdRun(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), `Usage: %s run command...

Check out a gomote from the pool, creating one if necessary, and
invoke command as a shell command with $VM set to the gomote's name.

If the command exits successfully, the gomote will be checked back in
to the pool. Otherwise, it will be destroyed.
`, os.Args[0])
	}
	flags.Parse(args)
	if flags.NArg() == 0 {
		flags.Usage()
		os.Exit(2)
	}
	arg := strings.Join(flags.Args(), " ")

	// Get a buildlet.
	p := OpenPool(poolPath)
	buildlet, err := p.Get()
	if err != nil {
		log.Fatal(err)
	}

	// Run command.
	cmd := exec.Command("/bin/sh", "-c", arg)
	cmd.Env = append(os.Environ(), "VM="+buildlet.Name)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err == nil {
		// Check the buildlet back in.
		p.Put(buildlet)
	} else {
		// Destroy the buildlet.
		fmt.Fprintf(os.Stderr, "%s (destroying buildlet)\n", err)
		p.Discard(buildlet)
		os.Exit(1)
	}
}

func touch(path string) {
	if err := ioutil.WriteFile(path, nil, 0666); err != nil {
		log.Fatal(err)
	}
}
