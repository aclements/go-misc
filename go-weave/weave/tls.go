// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package weave

type TLS struct {
	_ byte
}

func NewTLS() *TLS {
	return &TLS{}
}

func (v *TLS) Get() interface{} {
	return globalSched.curThread.tls[v]
}

func (v *TLS) Set(val interface{}) {
	m := globalSched.curThread.tls
	if m == nil {
		m = make(map[*TLS]interface{})
		globalSched.curThread.tls = m
	}
	m[v] = val
}
