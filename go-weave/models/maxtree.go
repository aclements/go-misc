// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// +build ignore

// maxtree is a model for a concurrent max-tree.
package main

import (
	"fmt"

	"github.com/aclements/go-misc/go-weave/amb"
	"github.com/aclements/go-misc/go-weave/weave"
)

var sched = weave.Scheduler{Strategy: &amb.StrategyRandom{}}

// DFS doesn't work because there are some infinite schedules from CAS
// retries.
//
//var sched = weave.Scheduler{Strategy: &amb.StrategyDFS{}}

const Depth = 3
const Degree = 2

type Node struct {
	Name string

	Parent   *Node
	PSlot    int
	Children [Degree]*Node

	Lock weave.Mutex
	Vals [Degree + 1]int
}

type State struct {
	Root *Node
}

func main() {
	var s State
	leaves := s.Init()
	sched.Run(func() {
		sched.Trace("resetting")
		s.Reset()
		sched.Trace("reset")

		for times := 0; times < 2; times++ {
			var wg weave.WaitGroup
			for i := 0; i < 2; i++ {
				i := i
				wg.Add(1)
				sched.Go(func() {
					s.worker(leaves[i])
					wg.Done()
				})
			}

			sched.Trace("waiting")
			wg.Wait()
		}

		sched.Trace("checking")
		s.Root.Check()
		//fmt.Println(s.Root.Vals)
	})
}

func (s *State) Init() (leaves []*Node) {
	var rec func(d int, name string) *Node
	rec = func(d int, name string) *Node {
		n := &Node{}
		n.Name = name
		if d == 1 {
			leaves = append(leaves, n)
			return n
		}
		for i := range n.Children {
			child := rec(d-1, fmt.Sprintf("%s/%d", name, i))
			n.Children[i] = child
			child.Parent = n
			child.PSlot = i
		}
		return n
	}
	s.Root = rec(Depth, "root")
	return
}

func (s *State) Reset() {
	s.Root.Reset()
}

func (n *Node) Reset() {
	if n == nil {
		return
	}
	n.Vals = [Degree + 1]int{}
	for _, c := range n.Children {
		c.Reset()
	}
}

func (n *Node) Check() int {
	if n == nil {
		return 0
	}

	for i, c := range n.Children {
		cmax := c.Check()
		if n.Vals[i] != cmax {
			panic(fmt.Sprintf("child max %d != parent slot %d", cmax, n.Vals[i]))
		}
	}

	return n.maxNoSched()
}

func (s *State) worker(node *Node) {
	// Pick a node.
	// var pick func(n *Node) *Node
	// pick = func(n *Node) *Node {
	// 	if n.Children[0] == nil {
	// 		return n
	// 	}
	// 	idx := sched.Amb(len(n.Children) + 1)
	// 	if idx == 0 {
	// 		return n
	// 	}
	// 	return pick(n.Children[idx-1])
	// }
	// node := pick(s.Root)
	// sched.Trace("picked")
	//
	// Not necessary when workers are given different nodes.
	// node.Lock.Lock()
	// defer node.Lock.Unlock()

	// Set node's value to 0, 1, or 2 so we can both raise and
	// lower the max.
	node.Update(sched.Amb(3))
	sched.Trace("updated")
}

func (n *Node) Update(val int) {
	newMax, changed := n.set(Degree, val)
	if !changed {
		return
	}

	for n.Parent != nil {
	retry:
		pMax, pChanged := n.Parent.set(n.PSlot, newMax)
		if checkMax := n.max(); newMax != checkMax {
			sched.Tracef("retrying newMax=%d checkMax=%d", newMax, checkMax)
			newMax = checkMax
			goto retry
		}

		if !pChanged {
			break
		}

		n, newMax = n.Parent, pMax
	}
}

func (n *Node) set(slot, val int) (newMax int, changed bool) {
	sched.Tracef("%s[%d] = %d", n.Name, slot, val)
	oldMax := n.maxNoSched()
	n.Vals[slot] = val
	newMax = n.maxNoSched()
	sched.Sched()

	return newMax, newMax != oldMax
}

func (n *Node) max() int {
	max := n.maxNoSched()
	sched.Sched()
	return max
}

func (n *Node) maxNoSched() int {
	m := 0
	for _, v := range n.Vals {
		if v > m {
			m = v
		}
	}
	return m
}
