// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command gopool manages a pool of gomote buildlets.
//
// The pool has a limited size and new buildlets are created as needed
// to run commands. The pool can run a setup command when a new
// buildlet is created (for example "gomote push $VM" is useful). If a
// buildlet fails, it is removed from the pool.
//
// Example usage:
//
//     gopool create -setup 'gomote push $VM' linux-amd64 5 &
//     stress -p 5 gopool run go/src/all.bash
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/build/buildlet"
)

func main() {
	buildlet.RegisterFlags()
	flag.Usage = func() {
		w := flag.CommandLine.Output()
		fmt.Fprintf(w, "Usage: %s [flags] <subcommand...>\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(w, "\nSubcommands:\n")
		fmt.Fprintf(w, "  create   create a new buildlet pool\n")
		fmt.Fprintf(w, "  run      run a command on a buildlet from the pool\n")
	}
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

	case "run":
		cmdRun(args)
	}
}

func cmdCreate(args []string) {
	// TODO: It may be confusing that setup is part of the create
	// command, since it runs with the environment and cwd of the
	// run command. Maybe the setup should run in the gopool
	// server process?

	flags := flag.NewFlagSet("create", flag.ExitOnError)
	setupFlag := flags.String("setup", "", "run shell command `cmd` to set up new instances; $VM will be set to the buildlet name")
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s create [flags] <type> <limit>\n", os.Args[0])
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() != 2 {
		flags.Usage()
		os.Exit(2)
	}
	kind := flags.Arg(0)
	limit, err := strconv.Atoi(flags.Arg(1))
	if err != nil {
		log.Fatalf("limit argument must be a number: %s", err)
	}
	create(kind, limit, *setupFlag)
}

func cmdRun(args []string) {
	log.SetPrefix("")
	log.SetFlags(0)

	flags := flag.NewFlagSet("run", flag.ExitOnError)
	flags.Usage = func() {
		fmt.Fprintf(flags.Output(), "Usage: %s run <cmd...>\n", os.Args[0])
		flags.PrintDefaults()
	}
	flags.Parse(args)
	if flags.NArg() < 1 {
		flags.Usage()
		os.Exit(2)
	}

	rc, wc := connect()
	coord, err := buildlet.NewCoordinatorClientFromFlags()
	if err != nil {
		log.Fatalf("error connecting to coordinator: %v", err)
	}

	// Get a gomote.
	wc <- ReqCheckout{}
	var checkout RepCheckout
	switch rep := (<-rc).(type) {
	default:
		log.Fatalf("unexpected reply: %v", rep)

	case nil:
		log.Fatalf("server disconnected")

	case PipeChanError:
		log.Fatalf("gopool read error: %v", rep.Err)

	case RepError:
		log.Fatal(rep.Msg)

	case RepCheckout:
		checkout = rep
	}
	log.Printf("got buildlet %s", checkout.Name)
	checkin := ReqCheckin{Fresh: checkout.Fresh, Broken: false}

	bc, err := coord.NamedBuildlet(checkout.Name)
	if err != nil {
		log.Fatalf("failed to look up buildlet %s: %v", checkout.Name, err)
	}

	// Set up gomote if fresh.
	if checkout.Fresh && checkout.Setup != "" {
		log.Print("setting up fresh buildlet")
		cmd := exec.Command("/bin/sh", "-c", checkout.Setup)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Env = append(os.Environ(), "VM="+checkout.Name)
		err := cmd.Run()
		if err != nil {
			log.Fatalf("failed to set up fresh buildlet: %v", err)
		}
	}
	checkin.Fresh = false

	// Run command.
	cmd := flags.Arg(0)
	remoteErr, execErr := bc.Exec(cmd, buildlet.ExecOpts{
		Args:        flags.Args()[1:],
		Output:      os.Stdout,
		SystemLevel: strings.HasPrefix(cmd, "/"),
	})
	if execErr != nil {
		log.Printf("buildlet error: %v", execErr)
		checkin.Broken = true
	} else if remoteErr != nil {
		log.Printf("error executing command: %v", remoteErr)
	}

	// Check gomote back in.
	wc <- checkin
	switch rep := (<-rc).(type) {
	default:
		log.Fatalf("unexpected reply: %v", rep)

	case nil:
		// Server disconnected. That's fine.

	case PipeChanError:
		log.Fatalf("gopool read error: %v", rep.Err)

	case RepError:
		log.Fatal(rep.Msg)

	case RepCheckin:
	}

	close(wc)

	if remoteErr != nil || execErr != nil {
		os.Exit(1)
	}
}
