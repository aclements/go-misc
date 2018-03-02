This package is a prototype implementation of split (or "sharded")
values for Go. This is a possible solution to
https://github.com/golang/go/issues/18802.

[![](https://godoc.org/github.com/aclements/go-misc/split?status.svg)](https://godoc.org/github.com/aclements/go-misc/split)

This prototype is very dependent on Go runtime internals. As is, it
does not depend on any *modifications* to the Go runtime; however,
there is an optional runtime modification that shaves about 4ns off
the cost of `Value.Get`. See that method for details.

Benchmarks
----------

With the runtime modification, the single-core overhead of the split
value compared to a single atomic counter is about 2 ns, and compared
to a non-atomic counter is about 6 ns:

```
BenchmarkCounterSplit          	200000000	         8.15 ns/op
BenchmarkCounterShared         	300000000	         5.96 ns/op
BenchmarkCounterSequential     	1000000000	         2.14 ns/op
BenchmarkLazyAggregationSplit  	100000000	        23.9 ns/op
BenchmarkLazyAggregationShared 	100000000	        23.1 ns/op
```

The scaling of the split values to 24 cores is nearly perfect (real
cores, no hyperthreads), while the shared values collapse as you'd
expect:

```
BenchmarkCounterSplit-24               	2000000000	         0.35 ns/op      8.40 cpu-ns/op
BenchmarkCounterShared-24            	50000000	        24.7 ns/op     593    cpu-ns/op
BenchmarkLazyAggregationSplit-24       	2000000000	         1.03 ns/op     24.7  cpu-ns/op
BenchmarkLazyAggregationShared-24    	10000000	       174 ns/op      4176    cpu-ns/op
```

Without the runtime modification, there's a little more overhead in
the sequential case, but the scaling isn't affected:

```
BenchmarkCounterSplit          	100000000	        12.3 ns/op
BenchmarkCounterShared         	300000000	         5.97 ns/op
BenchmarkCounterSequential     	1000000000	         2.28 ns/op
BenchmarkLazyAggregationSplit  	50000000	        25.2 ns/op
BenchmarkLazyAggregationShared 	100000000	        23.5 ns/op
```
