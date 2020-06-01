// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// There are several complications here:
//
// * The process may start subprocesses. It may exit before its
// subprocesses. Still-running subprocesses may keep the stdout/stderr
// pipe open and continue writing to it. If we kill the command (e.g.,
// after a timeout), we want to try to kill the whole subprocess tree.
//
// * The stress process itself may get killed in a way it can or can't
// catch. If possible, it shouldn't leave behind processes that it
// started. In POSIX, there's no way to do this for signals we can't
// catch.
//
// TODO(test): Test these situations.

type Command struct {
	// Output contains the process output after the process is done.
	Output []byte
	// Status contains the process exit status after the process is done.
	Status *os.ProcessState

	// waitChan is closed when the command exits and the status
	// fields above are filled in.
	waitChan chan struct{}

	// readDone is closed when the reader is no longer reading
	// output from the command.
	readDone chan struct{}

	mu      sync.Mutex // Protects fields below
	cmd     *exec.Cmd
	sigProc *os.Process
	outBuf  bytes.Buffer
}

func StartCommand(args []string) (*Command, error) {
	cmd := exec.Command(args[0], args[1:]...)

	// Put cmd in a process group so we can signal the whole
	// process group.
	//
	// This has the downside that the usual terminal signals
	// (notably SIGINT from Ctrl-C) won't automatically get
	// delivered to this new process group. Hence, we're
	// responsible for catching and forwarding them on.
	//
	// For other signals, there's simply not much we can do about
	// cleaning up children.
	//
	// TODO: On Linux, use PID namespaces if possible plus a
	// custom init process that exits if stress exits so we really
	// can reliably clean things up.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Create a pipe. We don't use a bytes.Buffer directly because
	// we may need to snapshot this before the write side is
	// closed and a bytes.Buffer would race.
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer func() {
		w.Close()
		if r != nil {
			r.Close()
		}
	}()
	cmd.Stdout = w
	cmd.Stderr = w

	// Start process.
	err = cmd.Start()
	if err != nil {
		return nil, err
	}

	// Create a fake os.Process for signaling the process group.
	sigProc, err := os.FindProcess(-cmd.Process.Pid)
	if err != nil {
		// Just signal the process.
		sigProc = cmd.Process
	}

	c := &Command{waitChan: make(chan struct{}), cmd: cmd, sigProc: sigProc}

	// Start output reader.
	c.readDone = make(chan struct{})
	go c.reader(r)
	r = nil

	// Start waiter.
	go c.waiter()

	return c, nil
}

func (c *Command) reader(f *os.File) {
	buf := make([]byte, 512)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			c.mu.Lock()
			// The command can exit while sub-processes
			// are still writing to stdout. We may have
			// exposed the buffer, so stop adding to the
			// buffer if that's happened.
			if c.cmd != nil {
				c.outBuf.Write(buf[:n])
			}
			c.mu.Unlock()
		}
		if err != nil {
			if err != io.EOF {
				log.Printf("reading from subprocess: %v", err)
			}
			break
		}
	}
	f.Close()
	close(c.readDone)
}

func (c *Command) waiter() {
	err := c.cmd.Wait()
	switch err.(type) {
	case nil:
	case *exec.ExitError:
		// Ignore. We'll pick up the process state below.
	default:
		// This indicates a bug, so panic.
		panic(fmt.Sprintf("wait %d failed: %s", c.cmd.Process.Pid, err))
	}

	// Clean up the process group. If everything in the process
	// group has already exited, this will fail, so we ignore any
	// errors. We do this as soon as possible after waiting so the
	// PGID won't get recycled.
	c.mu.Lock()
	c.sigProc.Signal(os.Kill)
	c.sigProc = nil
	c.mu.Unlock()

	// Wait a little bit for the output reader to catch up. Don't
	// wait too long because there could still be subprocesses
	// writing to the stdout pipe. But we need to wait a little
	// because even if there aren't, the pipe is asynchronous so
	// we could still be reading output from it.
	select {
	case <-c.readDone:
	case <-time.After(1 * time.Second):
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Gather the command's output.
	c.Output = c.outBuf.Bytes()
	c.Status = c.cmd.ProcessState

	// Signal that command has exited.
	c.cmd = nil
	close(c.waitChan)
}

// Kill kills the process, first gracefully then aggressively, and
// attempts to kill all of its sub-processes.
func (c *Command) Kill() {
	for _, sig := range []os.Signal{traceSignal, os.Interrupt, os.Kill} {
		if sig == nil {
			continue
		}

		if func() bool {
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.sigProc == nil {
				return true
			}
			c.sigProc.Signal(sig)
			return false
		}() {
			return
		}

		// Wait for a few seconds or for it to exit.
		select {
		case <-c.waitChan:
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// Done returns a channel that will be closed when the command exits
// and its output and status are ready.
func (c *Command) Done() <-chan struct{} {
	return c.waitChan
}
