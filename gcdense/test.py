#!/usr/bin/python3
# -*- coding: utf-8 -*-

import math
import random
import collections

import numpy as np
import matplotlib.pyplot as plt

class Graph:
    def __init__(self, nnodes):
        self.nnodes = nnodes
        self.out = [set() for i in range(nnodes)]

    def pageOf(self, node):
        return self.address[node] // ADDRS_PER_PAGE

    def bucketOf(self, node):
        return self.address[node] // ADDRS_PER_BUCKET

ADDRS_PER_PAGE = 10
PAGES_PER_BUCKET = 10
ADDRS_PER_BUCKET = ADDRS_PER_PAGE * PAGES_PER_BUCKET

def addressGraph(g, density=0.7):
    """Assign an allocation address to each node."""
    addresses = list(range(int(math.ceil(g.nnodes / density))))
    random.shuffle(addresses)
    g.address = addresses[:g.nnodes]

#TLB_ENTRIES = 64 + 1024     # Haswell
TLB_ENTRIES = 64

class TLB:
    def __init__(self):
        self.cache = collections.OrderedDict()
        self.misses = 0

    def touch(self, obj):
        page = obj // ADDRS_PER_PAGE
        if page in self.cache:
            self.cache.move_to_end(page)
            return
        self.misses += 1
        if len(self.cache) >= TLB_ENTRIES:
            # Evict.
            self.cache.popitem(last=False)
        self.cache[page] = True

def genERGraph(n, p):
    """Generate an Erdős-Rényi random graph of n nodes."""

    g = Graph(n)
    for i in range(n):
        # for j in range(n):
        #     if random.random() < p:
        #         g.out[i].add(j)
        # Approximate binomial distribution.
        nout = int(0.5 + random.gauss(n * p, n * p * (1 - p)))
        out = g.out[i]
        while len(out) < nout:
            out.add(random.randrange(n))
    return g

def genDeBruijn(degree, power):
    n = degree ** power
    g = Graph(n)
    for i in range(n):
        nextnode = i * degree % n
        for digit in range(degree):
            g.out[i].add(nextnode + digit)
    return g

def costLinear(n):
    return n

def costSqrt(n):
    return n**0.5

def costAffine10(n):
    return 10 + n

costs = [
    ("linear", costLinear),
    #("sqrt", costSqrt),
    # Minimizing affine cost just means minimizing step count
    #("affine10", costAffine10),
]

def argmax(iterable):
    return 

def pickFullest(buckets):
    return max(enumerate(buckets), key=lambda x: len(x[1]))[0]

def pickEmptiest(buckets):
    minidx = None
    for i, b in enumerate(buckets):
        if b and (minidx is None or len(b) < len(buckets[minidx])):
            minidx = i
    return minidx

def pickRandom(buckets):
    nonempty = [i for i, b in enumerate(buckets) if b]
    return random.choice(nonempty)

def pickFirst(buckets):
    for i, b in enumerate(buckets):
        if b:
            return i

def pickQuantile(quantile):
    def pick(buckets):
        nonempty = [i for i, b in enumerate(buckets) if b]
        nonempty.sort(key=lambda i: len(buckets[i]))
        return nonempty[int(math.floor((len(nonempty) - 1) * quantile))]
    return pick

def pickAlternate10(buckets):
    fullest = pickFullest(buckets)
    emptiest = pickEmptiest(buckets)
    if len(buckets[fullest]) >= 10 * len(buckets[emptiest]):
        return fullest
    return emptiest

picks = [
    ("fullest", pickFullest),
    ("Q3", pickQuantile(0.75)),
    ("median", pickQuantile(0.5)),
    ("Q1", pickQuantile(0.25)),
    ("emptiest", pickEmptiest),
    ("random", pickRandom),
    ("first", pickFirst),
    #("alternate10", pickAlternate10), # Not very interesting.
]

REPROCESS = True

def run(g, nroots, pick, cost):
    visited = [False] * g.nnodes
    buckets = [[] for i in range(g.nnodes)]
    tlb = TLB()

    # Queue roots
    for node in range(nroots):
        buckets[g.bucketOf(node)].append(node)

    # Process
    scanCost, steps, capacity = 0, 0, []
    while any(buckets):
        bidx = pick(buckets)

        # Fetch and clear bucket, since we may add more pointers while
        # processing this bucket.
        nodes = buckets[bidx]
        buckets[bidx] = []

        # Process bucket
        for node in nodes:
            # Assume an edge queuing model
            tlb.touch(-g.address[node]/32 - 1)
            if visited[node]:
                continue
            visited[node] = True

            tlb.touch(g.address[node])
            for nextnode in g.out[node]:
                nextbucket = g.bucketOf(nextnode)
                if REPROCESS and nextbucket == bidx:
                    nodes.append(nextnode)
                else:
                    buckets[nextbucket].append(nextnode)

        scanCost += cost(len(nodes))
        steps += 1
        capacity.append(len(nodes))

    meanCapacity = sum(capacity) / len(capacity)
    return scanCost, steps, meanCapacity, capacity, tlb.misses

def runGlobalQueue(g, nroots):
    """Simulate the regular global work queue algorithm."""
    visited = [False] * g.nnodes
    queue = collections.deque(range(nroots))
    tlb = TLB()

    while len(queue):
        obj = queue.pop()
        #page = g.pageOf(obj)

        # Assume the mark bits cover 32X less than the objects.
        tlb.touch(-g.address[obj]/32 - 1)
        if visited[obj]:
            continue
        visited[obj] = True

        # Scan the object.
        tlb.touch(g.address[obj])
        for nextnode in g.out[obj]:
            queue.appendleft(nextnode)

    # steps is number of buckets, but misses is number of pages. Scale
    # misses so they're comparable.
    return tlb.misses // PAGES_PER_BUCKET, tlb.misses

def ecdf(data):
    yvals = (np.arange(len(data)) + 1) / len(data)
    plt.plot(np.sort(data), yvals, drawstyle='steps')

def main():
    NROOTS = 10

    graph = genERGraph(2000, 0.01)
    addressGraph(graph)

    globalMisses, _ = runGlobalQueue(graph, NROOTS)
    print("%s\t\t\t\t%s" % ("global", globalMisses))

    for (costName, cost) in costs:
        for (pickName, pick) in picks:
            scanCost, steps, meanCapacity, capacity, _ = run(graph, NROOTS, pick, cost)
            print("%s\t%-10s\t%g\t%s\t%g" % (costName, pickName, scanCost, steps, meanCapacity))
            ecdf(capacity)
    plt.show()

def curve():
    NROOTS = 10

    for nodes in range(1000, 10000+1000, 1000):
        graph = genERGraph(nodes, 0.001)
    # for power in range(8, 15):
    #     graph = genDeBruijn(2, power)
    # for power in range(4, 9):
    #     graph = genDeBruijn(4, power)
        addressGraph(graph)

        heapsize = sum(len(o) for o in graph.out)

        _, misses = runGlobalQueue(graph, NROOTS)
        print("%d,%d,global" % (heapsize, misses))
        _, _, _, _, misses = run(graph, NROOTS, pickFullest, costLinear)
        print("%d,%d,sharded" % (heapsize, misses))

main()
#curve()
