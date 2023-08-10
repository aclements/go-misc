package main

import (
	"fmt"
	"sort"
	"strings"
)

type Dist struct {
	vals   []int
	sorted bool
}

func (d *Dist) Add(val int) {
	d.vals = append(d.vals, val)
	d.sorted = false
}

func (d *Dist) Quantile(q float64) int {
	if !d.sorted {
		sort.Ints(d.vals)
	}
	i := int((q * float64(len(d.vals)-1)) + 0.5)
	return d.vals[i]
}

func (d *Dist) StringSummary() string {
	const qs = 10
	var out strings.Builder
	for i := 0; i <= qs; i++ {
		fmt.Fprintf(&out, " %7s", fmt.Sprintf("p%d", i*100/qs))
	}
	out.WriteByte('\n')
	for i := 0; i <= qs; i++ {
		v := d.Quantile(float64(i) / qs)
		fmt.Fprintf(&out, " %7d", v)
	}
	fmt.Fprintf(&out, " N=%d", len(d.vals))
	return out.String()
}
