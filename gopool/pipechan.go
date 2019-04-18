// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/gob"
	"io"
	"log"
	"net"
	"strings"
)

type PipeChanError struct {
	Err error
}

type closerError interface {
	io.Closer
	CloseWithError(err error) error
}

func NewPipeChan(r io.Reader, w io.WriteCloser) (rc <-chan interface{}, wc chan<- interface{}) {
	// TODO: Since these are completely independent, maybe they
	// should be separate functions. Or maybe take a
	// ReadWriteCloser so we know the full effect of closing it.

	// TODO: Maybe the caller should pass in the channel so it can
	// size it.

	// Read side.
	rChan := make(chan interface{}, 1)
	go func() {
		d := gob.NewDecoder(r)
		for {
			var v interface{}
			if err := d.Decode(&v); err != nil {
				if err != io.EOF {
					rChan <- PipeChanError{err}
				}
				close(rChan)
				break
			}
			rChan <- v
		}
	}()

	// Write side.
	wChan := make(chan interface{}, 1)
	go func() {
		e := gob.NewEncoder(w)
		for {
			v, ok := <-wChan
			if !ok {
				w.Close()
				break
			}
			if err := e.Encode(&v); err != nil {
				// TODO: An I/O error should shut down
				// the read side.
				if w, ok := w.(closerError); ok {
					w.CloseWithError(err)
				} else {
					w.Close()
				}
				break
			}
		}
	}()

	return rChan, wChan
}

func Accept(lis net.Listener, conn func(rc <-chan interface{}, wc chan<- interface{})) {
	for {
		c, err := lis.Accept()
		if err != nil {
			// Ignore shutdown from lis.Close. This is
			// poll.ErrNetClosing, but that's in an
			// internal package. :(
			if !strings.Contains(err.Error(), "use of closed network connection") {
				log.Printf("accept: %s", err)
			}
			return
		}

		rc, wc := NewPipeChan(c, c)
		go conn(rc, wc)
	}
}
