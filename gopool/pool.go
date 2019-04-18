// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"sync"

	"golang.org/x/build/buildlet"
)

func NewBuildletPool(kind string, limit int) *BuildletPool {
	// TODO: Check that kind is valid.

	coord, err := buildlet.NewCoordinatorClientFromFlags()
	if err != nil {
		log.Fatalf("error connecting to coordinator: %v", err)
	}

	return &BuildletPool{
		kind:  kind,
		limit: make(chan struct{}, limit),
		coord: coord,
	}
}

type BuildletPool struct {
	kind  string
	limit chan struct{} // One token per Gomote in pool

	coord *buildlet.CoordinatorClient

	lock sync.Mutex
	pool []*Gomote
}

type Gomote struct {
	Buildlet   *buildlet.Client
	checkedOut bool
	Fresh      bool
	Broken     bool
}

func (p *BuildletPool) Checkout() (*Gomote, error) {
	get1 := func() *Gomote {
		p.lock.Lock()
		defer p.lock.Unlock()

		for _, g := range p.pool {
			if !g.checkedOut {
				g.checkedOut = true
				return g
			}
		}
		return nil
	}
	create1 := func() (*Gomote, error) {
		// Create a new builder.
		created := false
		p.limit <- struct{}{}
		defer func() {
			// Creation failed. Release limit.
			if !created {
				<-p.limit
			}
		}()

		log.Printf("creating %s buildlet", p.kind)
		client, err := p.coord.CreateBuildlet(p.kind)
		if err != nil {
			return nil, fmt.Errorf("error creating buildlet: %s", err)
		}
		defer func() {
			// Creation failed. Tear down buildlet.
			if !created {
				client.Close()
			}
		}()
		log.Printf("created buildlet %s", client.RemoteName())

		g := &Gomote{client, true, true, false}
		p.lock.Lock()
		defer p.lock.Unlock()
		p.pool = append(p.pool, g)
		created = true
		return g, nil
	}

	g := get1()
	if g == nil {
		var err error
		g, err = create1()
		if err != nil {
			return nil, err
		}
	}

	return g, nil
}

func (p *BuildletPool) Checkin(g *Gomote) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for i, g2 := range p.pool {
		if g == g2 {
			if !g.checkedOut {
				panic("checkin of already checked-in buildlet")
			}
			g.checkedOut = false
			if g.Broken {
				name := g.Buildlet.RemoteName()
				log.Printf("destroying broken buildlet %s", name)
				// Remove from the pool
				copy(p.pool[i:], p.pool[i+1:])
				p.pool = p.pool[:len(p.pool)-1]
				// Destroy
				if err := g.Buildlet.Close(); err != nil {
					panic(fmt.Errorf("failed to destroy buildlet %s: %v", name, err))
				}
				<-p.limit
			}
			return
		}
	}
	panic("checkin of unknown buildlet")
}

func (p *BuildletPool) Shutdown() {
	p.lock.Lock()
	defer p.lock.Unlock()
	for _, g := range p.pool {
		select {
		case <-p.limit:
		default:
		}

		name := g.Buildlet.RemoteName()
		log.Printf("destroying buildlet %s", name)
		if err := g.Buildlet.Close(); err != nil {
			log.Printf("failed to destroy buildlet %s: %v", name, err)
		}
	}
	p.pool = nil
}

func (g *Gomote) Ping() error {
	any := false
	err := g.Buildlet.ListDir(".", buildlet.ListDirOpts{}, func(buildlet.DirEntry) {
		any = true
	})
	if err != nil {
		return err
	}
	if !any {
		return fmt.Errorf("ListDir failed: no entries returned")
	}
	return nil
}
