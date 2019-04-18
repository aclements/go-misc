// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/gob"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
)

// TODO: Support multiple gopool instances.

func create(kind string, limit int, setup string) {
	lis, err := net.Listen("unix", "\x00gopool")
	if err != nil {
		log.Fatalf("error creating server socket: %s", err)
	}
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		log.Printf("Caught signal %s: shutting down.", sig)
		// Cause Accept to return.
		lis.Close()
	}()

	s := poolServer{
		NewBuildletPool(kind, limit),
		setup,
	}
	defer s.shutdown()

	Accept(lis, s.newConn)
}

func connect() (rc <-chan interface{}, wc chan<- interface{}) {
	s, err := net.Dial("unix", "\x00gopool")
	if err != nil {
		log.Fatalf("error connecting to gopool: %s", err)
	}
	return NewPipeChan(s, s)
}

type poolServer struct {
	p     *BuildletPool
	setup string
}

func (s *poolServer) shutdown() {
	// TODO: Disconnect clients first.

	s.p.Shutdown()
}

func (s *poolServer) newConn(rc <-chan interface{}, wc chan<- interface{}) {
	var err error
	var gomote *Gomote
	defer close(wc)
loop:
	for req := range rc {
		switch req := req.(type) {
		case PipeChanError:
			log.Printf("client read error: %s", req.Err)
			break loop

		case ReqCheckout:
			if gomote != nil {
				wc <- RepError{"gomote already checked out"}
				break
			}
			for retry := 0; retry < 5; retry++ {
				gomote, err = s.p.Checkout()
				if err != nil {
					break
				}
				err = gomote.Ping()
				if err == nil {
					break
				}
				log.Printf("ping %s failed: %v", gomote.Buildlet.RemoteName(), err)
				gomote.Broken = true
				s.p.Checkin(gomote)
				gomote = nil
			}
			if err != nil {
				wc <- RepError{err.Error()}
				break
			}
			if s.setup == "" {
				// If there's no setup command, then
				// all buildlets are considered
				// initialized.
				gomote.Fresh = false
			}
			wc <- RepCheckout{gomote.Buildlet.RemoteName(), gomote.Fresh, s.setup}

		case ReqCheckin:
			if gomote == nil {
				wc <- RepError{"no gomote to check in"}
				break
			}
			gomote.Fresh = req.Fresh
			gomote.Broken = req.Broken
			s.p.Checkin(gomote)
			gomote = nil
			wc <- RepCheckin{}
		}
	}
	log.Print("client disconnected")

	// Clean up on client exit.
	if gomote != nil {
		if gomote.Fresh {
			// Client died during setup, so the gomote's
			// state is unclear. Tear it down just in case.
			//
			// TODO: If it's running a long command, it's
			// a little unfortunate that a failure during
			// the long command (after setup) also causes
			// this. Maybe it should report in when setup
			// is done.
			gomote.Broken = true
		}
		s.p.Checkin(gomote)
	}
}

type RepError struct {
	Msg string
}

type ReqCheckout struct{}

type RepCheckout struct {
	Name  string
	Fresh bool
	Setup string
}

type ReqCheckin struct {
	Fresh  bool
	Broken bool
}

type RepCheckin struct{}

func init() {
	gob.Register(RepError{})
	gob.Register(ReqCheckout{})
	gob.Register(RepCheckout{})
	gob.Register(ReqCheckin{})
	gob.Register(RepCheckin{})
}
