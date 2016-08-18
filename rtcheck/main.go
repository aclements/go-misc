// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command rtcheck performs static analysis of the Go runtime.
//
// Note: Currently this requires a small modification to
// golang.org/x/tools/go/pointer:
//
//     --- a/go/pointer/intrinsics.go
//     +++ b/go/pointer/intrinsics.go
//     @@ -180,7 +180,6 @@ func (a *analysis) findIntrinsic(fn *ssa.Function) intrinsic {
//      			// Ignore "runtime" (except SetFinalizer):
//      			// it has few interesting effects on aliasing
//      			// and is full of unsafe code we can't analyze.
//     -			impl = ext۰NoEffect
//      		}
//
//      		a.intrinsics[fn] = impl
//
// rtcheck currently implements one analysis:
//
// Deadlock detection
//
// Static deadlock detection constructs a lock graph and reports
// cycles in that lock graph. These cycles indicate code paths with
// the potential for deadlock.
//
// The report from the deadlock detector indicates all discovered
// cycles in the lock graph and, for each edge L1 -> L2, shows the
// code paths that acquire L2 while holding L1. In the simplest case
// where L1 and L2 are the same lock, this cycle represents a
// potential for self-deadlock within a single thread. More generally,
// it means that if all of the code paths in the cycle execute
// concurrently, the system may deadlock. If one of the edges in a
// cycle is represented by significantly fewer code paths than the
// other edges, fixing those code paths is likely the easiest way to
// fix the deadlock.
//
// This uses an inter-procedural, path-sensitive, and partially
// value-sensitive analysis based on Engler and Ashcroft, "RacerX:
// Effective, static detection of race conditions and deadlocks", SOSP
// 2003. It works by exploring possible code paths and finding paths
// on which two or more locks are held simultaneously. Any such path
// produces one or more edges in the lock graph indicating the order
// in which those locks were acquired.
//
// Like many static analyses, this has limitations. First, it doesn't
// reason about individual locks, but about lock *classes*, which are
// modeled as sets of locks that may alias each other. As a result, if
// the code acquires multiple locks from the same lock class
// simultaneously (such as locks from different instances of the same
// structure), but is careful to ensure a consistent order between
// those locks at runtime (e.g., by sorting them), this analysis will
// consider that a potential deadlock, even though it will not
// deadlock at runtime.
//
// Second, it may explore code paths that are impossible at runtime.
// The analysis performs very simple intra-procedural value
// propagation to eliminate obviously impossible code paths, but this
// is easily fooled. Consider
//
//     if complex condition 1 {
//         lock(&x)
//     }
//     ...
//     if complex condition 2 {
//         unlock(&x)
//     }
//
// where complex conditions 1 and 2 are equivalent, but beyond the
// reach of the simple value propagation. The analysis will see *four*
// distinct code paths here, rather than the two that are actually
// possible, and think that x can still be held after the second if.
// Similarly,
//
//     lock(&x)
//     ensure !c
//     ...
//     if c {
//         lock(&x)
//     }
//
// If c can't be deduced by value propagation, this will appear as a
// potential self-deadlock. Of course, if it requires complex dynamic
// reasoning to show that a deadlock cannot occur at runtime, it may
// be a good idea to simplify the code anyway.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/build"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"io"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"

	"golang.org/x/tools/go/buildutil"
	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/loader"
	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// debugFunctions is a set of functions to enable extra debugging
// tracing for. Each function in debugFunctions will generate a dot
// file containing the block exploration graph of that function.
var debugFunctions = map[string]bool{}

func main() {
	var (
		outLockGraph string
		outCallGraph string
		outHTML      string
		debugFuncs   string
	)
	flag.StringVar(&outLockGraph, "lockgraph", "", "write lock graph in dot to `file`")
	flag.StringVar(&outCallGraph, "callgraph", "", "write call graph in dot to `file`")
	flag.StringVar(&outHTML, "html", "", "write HTML deadlock report to `file`")
	flag.StringVar(&debugFuncs, "debugfuncs", "", "write debug graphs for `funcs` (comma-separated list)")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	for _, name := range strings.Split(debugFuncs, ",") {
		debugFunctions[name] = true
	}

	roots := getDefaultRoots()

	var conf loader.Config

	// TODO: Check all reasonable arch/OS combos.

	// TODO: This would be so much easier and nicer if I could
	// just plug (path, AST)s into the loader, or at least slip in
	// between when the loader has parsed everything and when it
	// type-checks everything. Currently it's only possible to
	// provide ASTs for non-importable packages to the
	// loader.Config.

	newSources := make(map[string][]byte)
	for _, pkgName := range []string{"runtime", "runtime/internal/atomic"} {
		buildPkg, err := build.Import(pkgName, "", 0)
		if err != nil {
			log.Fatal(err)
		}
		var pkgRoots []string
		if pkgName == "runtime" {
			pkgRoots = roots
		}
		rewriteSources(buildPkg, pkgRoots, newSources)
	}

	ctxt := &build.Default
	ctxt = buildutil.OverlayContext(ctxt, newSources)

	conf.Build = ctxt
	conf.Import("runtime")

	lprog, err := conf.Load()
	if err != nil {
		log.Fatal("loading runtime: ", err)
	}
	fset := lprog.Fset

	prog := ssautil.CreateProgram(lprog, 0)
	prog.Build()
	runtimePkg := prog.ImportedPackage("runtime")
	lookupMembers(runtimePkg, runtimeFns)

	// TODO: Teach it that you can jump to sigprof at any point?
	//
	// TODO: Teach it about implicit write barriers?

	// Prepare for pointer analysis.
	ptrConfig := pointer.Config{
		Mains:          []*ssa.Package{runtimePkg},
		BuildCallGraph: true,
		//Log:            os.Stderr,
	}

	// Run pointer analysis.
	pta, err := pointer.Analyze(&ptrConfig)
	if err != nil {
		log.Fatal(err)
	}
	cg := pta.CallGraph

	cg.DeleteSyntheticNodes() // ?

	// Output call graph if requested.
	if outCallGraph != "" {
		withWriter(outCallGraph, func(w io.Writer) {
			type edge struct{ a, b *callgraph.Node }
			have := make(map[edge]struct{})
			fmt.Fprintln(w, "digraph callgraph {")
			callgraph.GraphVisitEdges(pta.CallGraph, func(e *callgraph.Edge) error {
				if _, ok := have[edge{e.Caller, e.Callee}]; ok {
					return nil
				}
				have[edge{e.Caller, e.Callee}] = struct{}{}
				fmt.Fprintf(w, "%q -> %q;\n", e.Caller.Func, e.Callee.Func)
				return nil
			})
			fmt.Fprintln(w, "}")
		})
	}

	s := state{
		fset: fset,
		cg:   cg,
		pta:  pta,
		fns:  make(map[*ssa.Function]*funcInfo),

		lockOrder: NewLockOrder(fset),

		roots:   nil,
		rootSet: make(map[*ssa.Function]struct{}),
	}
	s.gscanLock = s.lca.NewLockClass("_Gscan", false)

	// Create heap objects we care about.
	//
	// TODO: Also track m.preemptoff.
	s.heap.curG = NewHeapObject("curG")
	userG := NewHeapObject("userG")
	userG_m := NewHeapObject("userG.m")
	s.heap.g0 = NewHeapObject("g0")
	g0_m := NewHeapObject("g0.m")
	s.heap.curM = NewHeapObject("curM")
	curM_g0 := NewHeapObject("curM.g0")
	curM_curg := NewHeapObject("curM.curg")
	s.heap.curM_locks = NewHeapObject("curM.locks")
	curM_printlock := NewHeapObject("curM.printlock")

	// Add roots to state.
	for _, name := range roots {
		m, ok := runtimePkg.Members[name].(*ssa.Function)
		if !ok {
			log.Fatalf("unknown root: %s", name)
		}
		s.addRoot(m)
	}

	// Analyze each root. Analysis may add more roots.
	for i := 0; i < len(s.roots); i++ {
		root := s.roots[i]

		// Create initial heap state for entering from user space.
		var vs ValState
		vs = vs.ExtendHeap(s.heap.curG, DynHeapPtr{userG})
		vs = vs.ExtendHeap(userG, DynStruct{"m": userG_m})
		vs = vs.ExtendHeap(userG_m, DynHeapPtr{s.heap.curM})
		vs = vs.ExtendHeap(s.heap.g0, DynStruct{"m": g0_m})
		vs = vs.ExtendHeap(g0_m, DynHeapPtr{s.heap.curM})
		vs = vs.ExtendHeap(s.heap.curM, DynStruct{"curg": curM_curg, "g0": curM_g0, "locks": s.heap.curM_locks, "printlock": curM_printlock})
		vs = vs.ExtendHeap(curM_g0, DynHeapPtr{s.heap.g0})
		// Initially we're on the user stack.
		vs = vs.ExtendHeap(curM_curg, DynHeapPtr{userG})
		// And hold no locks.
		vs = vs.ExtendHeap(s.heap.curM_locks, DynConst{constant.MakeInt64(0)})
		vs = vs.ExtendHeap(curM_printlock, DynConst{constant.MakeInt64(0)})

		// Create the initial PathState.
		ps := PathState{
			lockSet: NewLockSet(),
			vs:      vs,
		}

		// Walk the function.
		exitStates := s.walkFunction(root, ps)

		// Warn if any locks are held at return.
		exitStates.ForEach(func(ps PathState) {
			if len(ps.lockSet.stacks) == 0 {
				return
			}
			s.warnl(root.Pos(), "locks at return from root %s: %s", root, ps.lockSet)
			s.warnl(root.Pos(), "\t(likely analysis failed to match control flow for unlock)\n")
		})
	}

	// Dump debug trees.
	if s.debugTree != nil {
		withWriter("debug-functions.dot", s.debugTree.WriteToDot)
	}
	for fn, fInfo := range s.fns {
		if fInfo.debugTree == nil {
			continue
		}
		withWriter(fmt.Sprintf("debug-%s.dot", fn), fInfo.debugTree.WriteToDot)
	}

	// Output lock graph.
	if outLockGraph != "" {
		withWriter(outLockGraph, s.lockOrder.WriteToDot)
	}

	// Output HTML report.
	if outHTML != "" {
		withWriter(outHTML, s.lockOrder.WriteToHTML)
	}

	// Output text lock cycle report.
	fmt.Println()
	fmt.Print("roots:")
	for _, fn := range s.roots {
		fmt.Printf(" %s", fn)
	}
	fmt.Print("\n")
	fmt.Printf("number of lock cycles: %d\n\n", len(s.lockOrder.FindCycles()))
	s.lockOrder.Check(os.Stdout)
}

// withWriter creates path and calls f with the file.
func withWriter(path string, f func(w io.Writer)) {
	file, err := os.Create(path)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	f(file)
}

// getDefaultRoots returns a list of functions in the runtime package
// to use as roots.
//
// It parses $GOROOT/src/cmd/compile/internal/gc/builtin/runtime.go to
// get this list, since these are the functions the compiler can
// generate calls to.
func getDefaultRoots() []string {
	path := filepath.Join(runtime.GOROOT(), "src/cmd/compile/internal/gc/builtin/runtime.go")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		log.Fatalf("%s: %s", path, err)
	}

	var roots []string
	for _, decl := range f.Decls {
		decl, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		switch decl.Name.Name {
		case "cmpstring", "eqstring",
			"int64div", "uint64div", "int64mod", "uint64mod",
			"float64toint64", "float64touint64",
			"int64tofloat64", "uint64tofloat64":
			// These are declared only in assembly.
			continue
		}
		if strings.HasPrefix(decl.Name.Name, "race") {
			// These functions are declared by runtime.go,
			// but only exist in race mode.
			continue
		}
		roots = append(roots, decl.Name.Name)
	}
	return roots
}

// rewriteSources rewrites all of the Go files in pkg to eliminate
// runtime-isms, make them easier for go/ssa to process, to add stubs
// for internal functions, and to generate init-time calls to analysis
// root functions. It fills rewritten with path -> new source
// mappings.
func rewriteSources(pkg *build.Package, roots []string, rewritten map[string][]byte) {
	rootSet := make(map[string]struct{})
	for _, root := range roots {
		rootSet[root] = struct{}{}
	}

	for _, fname := range pkg.GoFiles {
		path := filepath.Join(pkg.Dir, fname)

		// Parse source.
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			log.Fatalf("%s: %s", path, err)
		}

		isNosplit := map[ast.Decl]bool{}
		rewriteStubs(f, isNosplit)
		if pkg.Name == "runtime" {
			addRootCalls(f, rootSet)
			rewriteRuntime(f, isNosplit)
		}

		// Back to source.
		var buf bytes.Buffer
		if err := (&printer.Config{Mode: printer.SourcePos, Tabwidth: 8}).Fprint(&buf, fset, f); err != nil {
			log.Fatalf("outputting replacement %s: %s", path, err)
		}

		if pkg.Name == "runtime" && fname == "stubs.go" {
			// Declare functions used during rewriting.
			buf.Write([]byte(`
// systemstack is transformed into a call to presystemstack, then
// the operation, then postsystemstack. These functions are handled
// specially.
func rtcheck۰presystemstack() *g { return nil }
func rtcheck۰postsystemstack(*g) { }
`))
		}

		rewritten[path] = buf.Bytes()
	}

	// Check that we found all of the roots.
	if len(rootSet) > 0 {
		fmt.Fprintf(os.Stderr, "unknown roots:")
		for root := range rootSet {
			fmt.Fprintf(os.Stderr, " %s", root)
		}
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}
}

var newStubs = make(map[string]map[string]*ast.FuncDecl)

func init() {
	// TODO: Perhaps I should do most of these as "special"
	// functions, and do the few that affect pointers (like
	// noescape) as call rewrites.

	// Stubs provide implementations for assembly functions that
	// are not declared in the Go source code. All of these are
	// automatically marked go:nosplit.
	var runtimeStubs = `
package runtime

// stubs.go
// getg is handled specially.
// mcall and systemstack are eliminated during rewriting.
func memclr() { }
func memmove() { }
func fastrand1() uint32 { return 0 }
func memequal() bool { return false }
func noescape(p unsafe.Pointer) unsafe.Pointer { return p }
func cgocallback() { }
func gogo() { for { } }
func gosave() { }
func mincore() int32 { return 0 }
func jmpdefer() { for { } }
func exit1() { for { } }
func setg() { }
func breakpoint() { }
func reflectcall() { }
func procyield() { }
func cgocallback_gofunc() { }
func publicationBarrier() { }
func setcallerpc() { }
func getcallerpc() uintptr { return 0 }
func getcallersp() uintptr { return 0 }
func asmcgocall() int32 { return 0 }
// morestack is handled specially.
func time_now() (int64, int32) { return 0, 0 }

// os_linux.go
func futex() int32 { return 0 }
func clone() int32 { return 0 }
func gettid() uint32 { return 0 }
func sigreturn() { for { } }
func rt_sigaction() int32 { return 0 }
func sigaltstack() { }
func setitimer() { }
func rtsigprocmask() { }
func getrlimit() int32 { return 0 }
func raise() { for { } }
func raiseproc() { for { } }
func sched_getaffinity() int32 { return 0 }
func osyield() { }

// stubs2.go
func read() { return 0 }
func closefd() { return 0 }
func exit() { for {} }
func nanotime() { return 0 }
func usleep() {}
func munmap() {}
func write() int32 { return 0 }
func open() int32 { return 0 }
func madvise() {}

// cputicks.go
func cputicks() { return 0 }

// cgo_mmap.go
func sysMmap() unsafe.Pointer { return nil }
func callCgoMmap() uintptr { return 0 }

// alg.go
func aeshash(p unsafe.Pointer, h, s uintptr) uintptr { return 0 }
func aeshash32(p unsafe.Pointer, h uintptr) uintptr { return 0 }
func aeshash64(p unsafe.Pointer, h uintptr) uintptr { return 0 }
func aeshashstr(p unsafe.Pointer, h uintptr) uintptr { return 0 }

// netpoll_epoll.go
func epollcreate(size int32) int32 { return 0 }
func epollcreate1(flags int32) int32 { return 0 }
func epollctl(epfd, op, fd int32, ev *epollevent) int32 { return 0 }
func epollwait(epfd int32, ev *epollevent, nev, timeout int32) int32 { return 0 }
func closeonexec(fd int32) {}
`
	var atomicStubs = `
package atomic

// stubs.go
func Cas(ptr *uint32, old, new uint32) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Casp1(ptr *unsafe.Pointer, old, new unsafe.Pointer) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Casuintptr(ptr *uintptr, old, new uintptr) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Storeuintptr(ptr *uintptr, new uintptr) { *ptr = new }
func Loaduintptr(ptr *uintptr) uintptr { return *ptr }
func Loaduint(ptr *uint) uint { return *ptr }
func Loadint64(ptr *int64) int64 { return *ptr }
func Xaddint64(ptr *int64, delta int64) int64 {
	*ptr += delta
	return *ptr
}

// atomic_*.go
func Load(ptr *uint32) uint32 { return *ptr }
func Loadp(ptr unsafe.Pointer) unsafe.Pointer { return *(*unsafe.Pointer)(ptr) }
func Load64(ptr *uint64) uint64 { return *ptr }
func Xadd(ptr *uint32, delta int32) uint32 {
	*ptr += uint32(delta)
	return *ptr
}
func Xadd64(ptr *uint64, delta int64) uint64 {
	*ptr += uint64(delta)
	return *ptr
}
func Xadduintptr(ptr *uintptr, delta uintptr) uintptr {
	*ptr += delta
	return *ptr
}
func Xchg(ptr *uint32, new uint32) uint32 {
	old := *ptr
	*ptr = new
	return old
}
func Xchg64(ptr *uint64, new uint64) uint64 {
	old := *ptr
	*ptr = new
	return old
}
func Xchguintptr(ptr *uintptr, new uintptr) uintptr {
	old := *ptr
	*ptr = new
	return old
}
func And8(ptr *uint8, val uint8) { *ptr &= val }
func Or8(ptr *uint8, val uint8) { *ptr |= val }
func Cas64(ptr *uint64, old, new uint64) bool {
	if *ptr == old { *ptr = new; return true }
	return false
}
func Store(ptr *uint32, val uint32) { *ptr = val }
func Store64(ptr *uint64, val uint64) { *ptr = val }
func StorepNoWB(ptr unsafe.Pointer, val unsafe.Pointer) {
	*(*unsafe.Pointer)(ptr) = val
}
`

	for _, stubs := range []string{runtimeStubs, atomicStubs} {
		f, err := parser.ParseFile(token.NewFileSet(), "<newStubs>", stubs, 0)
		if err != nil {
			log.Fatal("parsing replacement stubs: ", err)
		}

		// Strip token.Pos information from stubs. It confuses
		// the printer, which winds up producing invalid Go code.
		ast.Inspect(f, func(n ast.Node) bool {
			if n == nil {
				return true
			}
			rn := reflect.ValueOf(n).Elem()
			for i := 0; i < rn.NumField(); i++ {
				f := rn.Field(i)
				if _, ok := f.Interface().(token.Pos); ok {
					f.Set(reflect.Zero(f.Type()))
				}
			}
			return true
		})

		newMap := make(map[string]*ast.FuncDecl)
		for _, decl := range f.Decls {
			newMap[decl.(*ast.FuncDecl).Name.Name] = decl.(*ast.FuncDecl)
		}
		newStubs[f.Name.Name] = newMap
	}
}

func rewriteStubs(f *ast.File, isNosplit map[ast.Decl]bool) {
	// Replace declaration bodies.
	for _, decl := range f.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body != nil {
				continue
			}
			newDecl, ok := newStubs[f.Name.Name][decl.Name.Name]
			if !ok {
				continue
			}
			decl.Body = newDecl.Body
			isNosplit[decl] = true
		}
	}
}

func addRootCalls(f *ast.File, rootSet map[string]struct{}) {
	var body []ast.Stmt
	for _, decl := range f.Decls {
		decl, ok := decl.(*ast.FuncDecl)
		if !ok || decl.Recv != nil {
			continue
		}
		if _, ok := rootSet[decl.Name.Name]; !ok {
			continue
		}
		delete(rootSet, decl.Name.Name)

		// Construct a valid call.
		args := []ast.Expr{}
		for _, aspec := range decl.Type.Params.List {
			n := len(aspec.Names)
			if aspec.Names == nil {
				n = 1
			}
			for i := 0; i < n; i++ {
				switch atype := aspec.Type.(type) {
				case *ast.ChanType, *ast.FuncType,
					*ast.InterfaceType, *ast.MapType,
					*ast.StarExpr:
					args = append(args, &ast.Ident{Name: "nil"})
				case *ast.StructType:
					log.Fatal("not implemented: struct args")
				case *ast.ArrayType, *ast.Ident, *ast.SelectorExpr:
					name := fmt.Sprintf("x%d", len(body))
					adecl := &ast.DeclStmt{
						&ast.GenDecl{
							Tok: token.VAR,
							Specs: []ast.Spec{
								&ast.ValueSpec{
									Names: []*ast.Ident{{Name: name}},
									Type:  atype,
								},
							},
						},
					}
					body = append(body, adecl)
					args = append(args, &ast.Ident{Name: name})
				default:
					log.Fatalf("unexpected function argument type: %s", aspec)
				}
			}
		}
		body = append(body, &ast.ExprStmt{&ast.CallExpr{
			Fun:  &ast.Ident{Name: decl.Name.Name},
			Args: args,
		}})
	}
	if len(body) > 0 {
		f.Decls = append(f.Decls,
			&ast.FuncDecl{
				Name: &ast.Ident{Name: "init"},
				Type: &ast.FuncType{Params: &ast.FieldList{}},
				Body: &ast.BlockStmt{List: body},
			})
	}
}

func rewriteRuntime(f *ast.File, isNosplit map[ast.Decl]bool) {
	// Attach go:nosplit directives to top-level declarations. We
	// have to do this before the Rewrite walk because go/ast
	// drops comments separated by newlines from the AST, leaving
	// them only in File.Comments. But to agree with the
	// compiler's interpretation of these comments, we need all of
	// the comments.
	cgs := f.Comments
	for _, decl := range f.Decls {
		// Process comments before decl.
		for len(cgs) > 0 && cgs[0].Pos() < decl.Pos() {
			for _, c := range cgs[0].List {
				if c.Text == "//go:nosplit" {
					isNosplit[decl] = true
				}
			}
			cgs = cgs[1:]
		}
		// Ignore comments in decl.
		for len(cgs) > 0 && cgs[0].Pos() < decl.End() {
			cgs = cgs[1:]
		}
	}

	// TODO: Do identifier resolution so I know I'm actually
	// getting the runtime globals.
	id := func(name string) *ast.Ident {
		return &ast.Ident{Name: name}
	}
	Rewrite(func(node ast.Node) ast.Node {
		switch node := node.(type) {
		case *ast.CallExpr:
			id, ok := node.Fun.(*ast.Ident)
			if !ok {
				break
			}
			switch id.Name {
			case "systemstack":
				log.Fatal("systemstack not at statement level")
			case "mcall":
				// mcall(f) -> f(nil)
				return &ast.CallExpr{Fun: node.Args[0], Args: []ast.Expr{&ast.Ident{Name: "nil"}}}
			case "gopark":
				if cb, ok := node.Args[0].(*ast.Ident); ok && cb.Name == "nil" {
					break
				}
				// gopark(fn, arg, ...) -> fn(nil, arg)
				return &ast.CallExpr{
					Fun: node.Args[0],
					Args: []ast.Expr{
						&ast.Ident{Name: "nil"},
						node.Args[1],
					},
				}
			case "goparkunlock":
				// goparkunlock(x, ...) -> unlock(x)
				return &ast.CallExpr{
					Fun:  &ast.Ident{Name: "unlock"},
					Args: []ast.Expr{node.Args[0]},
				}
			}

		case *ast.ExprStmt:
			// Rewrite:
			//   systemstack(f) -> {g := presystemstack(); f(); postsystemstack(g) }
			//   systemstack(func() { x }) -> {g := presystemstack(); x; postsystemstack(g) }
			expr, ok := node.X.(*ast.CallExpr)
			if !ok {
				break
			}
			fnid, ok := expr.Fun.(*ast.Ident)
			if !ok || fnid.Name != "systemstack" {
				break
			}
			var x ast.Stmt
			if arg, ok := expr.Args[0].(*ast.FuncLit); ok {
				x = arg.Body
			} else {
				x = &ast.ExprStmt{&ast.CallExpr{Fun: expr.Args[0]}}
			}
			pre := &ast.AssignStmt{
				Lhs: []ast.Expr{id("rtcheck۰g")},
				Tok: token.DEFINE,
				Rhs: []ast.Expr{&ast.CallExpr{Fun: id("rtcheck۰presystemstack")}},
			}
			post := &ast.ExprStmt{&ast.CallExpr{Fun: id("rtcheck۰postsystemstack"), Args: []ast.Expr{id("rtcheck۰g")}}}
			return &ast.BlockStmt{List: []ast.Stmt{pre, x, post}}

		case *ast.FuncDecl:
			// TODO: Some functions are just too hairy for
			// the analysis right now.
			switch node.Name.Name {
			case "throw":
				node.Body = &ast.BlockStmt{
					List: []ast.Stmt{
						&ast.ForStmt{
							Body: &ast.BlockStmt{},
						},
					},
				}

			case "traceEvent", "cgoContextPCs", "callCgoSymbolizer":
				// TODO: If we handle traceEvent, we
				// still can't handle inter-procedural
				// correlated control flow between
				// traceAcquireBuffer and
				// traceReleaseBuffer, so hard-code
				// that traceReleaseBuffer releases
				// runtime.trace.bufLock.
				//
				// TODO: A bunch of false positives
				// come from callCgoSymbolizer and
				// cgoContextPCs, which dynamically
				// call either cgocall or asmcgocall
				// depending on whether we're on the
				// system stack. We don't flow enough
				// information through to tell, so we
				// assume it can always call cgocall,
				// which leads to all sorts of bad
				// lock edges.
				node.Body = &ast.BlockStmt{}
			}

			// Insert morestack() prologue.
			//
			// TODO: This only happens in the runtime
			// package right now. It should happen in all
			// packages.
			if node.Body == nil || len(node.Body.List) == 0 || isNosplit[node] {
				break
			}
			call := &ast.ExprStmt{&ast.CallExpr{Fun: id("morestack"), Args: []ast.Expr{}, Lparen: node.Body.Pos()}}
			node.Body.List = append([]ast.Stmt{call}, node.Body.List...)
		}
		return node
	}, f)
}

var fns struct {
	// Locking functions.
	lock, unlock *ssa.Function

	// Allocation functions.
	newobject, newarray, makemap, makechan *ssa.Function

	// Slice functions.
	growslice, slicecopy, slicestringcopy *ssa.Function

	// Map functions.
	mapaccess1, mapaccess2, mapassign1, mapdelete *ssa.Function

	// Channel functions.
	chansend1, closechan *ssa.Function

	// Misc.
	gopanic *ssa.Function
}

var runtimeFns = map[string]interface{}{
	"lock": &fns.lock, "unlock": &fns.unlock,
	"newobject": &fns.newobject, "newarray": &fns.newarray,
	"makemap": &fns.makemap, "makechan": &fns.makechan,
	"growslice": &fns.growslice, "slicecopy": &fns.slicecopy,
	"slicestringcopy": &fns.slicestringcopy,
	"mapaccess1":      &fns.mapaccess1, "mapaccess2": &fns.mapaccess2,
	"mapassign1": &fns.mapassign1, "mapdelete": &fns.mapdelete,
	"chansend1": &fns.chansend1, "closechan": &fns.closechan,
	"gopanic": &fns.gopanic,
}

func lookupMembers(pkg *ssa.Package, out map[string]interface{}) {
	for name, ptr := range out {
		member, ok := pkg.Members[name]
		if !ok {
			log.Fatal("%s.%s not found", pkg, name)
		}
		reflect.ValueOf(ptr).Elem().Set(reflect.ValueOf(member))
	}
}

// StringSpace interns strings into small integers.
type StringSpace struct {
	m map[string]int
	s []string
}

// NewStringSpace returns a new, empty StringSpace.
func NewStringSpace() *StringSpace {
	return &StringSpace{m: make(map[string]int)}
}

// Intern turns str into a small integer where Intern(x) == Intern(y)
// iff x == y.
func (sp *StringSpace) Intern(str string) int {
	if id, ok := sp.m[str]; ok {
		return id
	}
	id := len(sp.s)
	sp.s = append(sp.s, str)
	sp.m[str] = id
	return id
}

// TryIntern interns str if it has been interned before. Otherwise, it
// does not intern the string and returns 0, false.
func (sp *StringSpace) TryIntern(str string) (int, bool) {
	id, ok := sp.m[str]
	return id, ok
}

// LockSet represents a set of locks and where they were acquired.
type LockSet struct {
	lca    *LockClassAnalysis
	bits   big.Int
	stacks map[int]*StackFrame
}

type LockSetKey string

func NewLockSet() *LockSet {
	return &LockSet{}
}

func (set *LockSet) clone() *LockSet {
	out := &LockSet{lca: set.lca, stacks: map[int]*StackFrame{}}
	out.bits.Set(&set.bits)
	for k, v := range set.stacks {
		out.stacks[k] = v
	}
	return out
}

func (set *LockSet) withLCA(lca *LockClassAnalysis) *LockSet {
	if set.lca == nil {
		set.lca = lca
	} else if set.lca != lca {
		panic("cannot mix locks from different LockClassAnalyses")
	}
	return set
}

// Key returns a string such that two LockSet's Keys are == iff both
// LockSets have the same locks acquired at the same stacks.
func (set *LockSet) Key() LockSetKey {
	// TODO: This is complex enough now that maybe I just want a
	// hash function and an equality function.
	k := set.bits.Text(16)
	for i := 0; i < set.bits.BitLen(); i++ {
		if set.bits.Bit(i) != 0 {
			k += ":"
			for sf := set.stacks[i]; sf != nil; sf = sf.parent {
				k += fmt.Sprintf("%v,", sf.call.Pos())
			}
		}
	}
	return LockSetKey(k)
}

// HashKey returns a key such that set1.Equal(set2) implies
// set1.HashKey() == set2.HashKey().
func (set *LockSet) HashKey() string {
	return set.bits.Text(16)
}

// Equal returns whether set and set2 contain the same locks acquired
// at the same stacks.
func (set *LockSet) Equal(set2 *LockSet) bool {
	if set.lca != set2.lca {
		return false
	}
	if set.bits.Cmp(&set2.bits) != 0 {
		return false
	}
	for k, v := range set.stacks {
		if set2.stacks[k] != v {
			return false
		}
	}
	return true
}

// Contains returns true if set contains lock class lc.
func (set *LockSet) Contains(lc *LockClass) bool {
	return set.lca == lc.Analysis() && set.bits.Bit(lc.Id()) != 0
}

// Plus returns a LockSet that extends set with lock class lc,
// acquired at stack. If lc is already in set, it does not get
// re-added and Plus returns set.
func (set *LockSet) Plus(lc *LockClass, stack *StackFrame) *LockSet {
	if set.bits.Bit(lc.Id()) != 0 {
		return set
	}
	out := set.clone().withLCA(lc.Analysis())
	out.bits.SetBit(&out.bits, lc.Id(), 1)
	out.stacks[lc.Id()] = stack
	return out
}

// Union returns a LockSet that is the union of set and o. If both set
// and o contain the same lock, the stack from set is preferred.
func (set *LockSet) Union(o *LockSet) *LockSet {
	var new big.Int
	new.AndNot(&o.bits, &set.bits)
	if new.Sign() == 0 {
		// Nothing to add.
		return set
	}

	out := set.clone().withLCA(o.lca)
	out.bits.Or(&out.bits, &o.bits)
	for k, v := range o.stacks {
		if out.stacks[k] == nil {
			out.stacks[k] = v
		}
	}
	return out
}

// Minus returns a LockSet that is like set, but does not contain lock
// class lc.
func (set *LockSet) Minus(lc *LockClass) *LockSet {
	if set.bits.Bit(lc.Id()) == 0 {
		return set
	}
	out := set.clone().withLCA(lc.Analysis())
	out.bits.SetBit(&out.bits, lc.Id(), 0)
	delete(out.stacks, lc.Id())
	return out
}

func (set *LockSet) String() string {
	b := []byte("{")
	first := true
	for i := 0; i < set.bits.BitLen(); i++ {
		if set.bits.Bit(i) != 0 {
			if !first {
				b = append(b, ',')
			}
			first = false
			b = append(b, set.lca.Lookup(i).String()...)
		}
	}
	return string(append(b, '}'))
}

// A LockSetSet is a set of LockSets.
type LockSetSet struct {
	M map[LockSetKey]*LockSet
}

func NewLockSetSet() *LockSetSet {
	return &LockSetSet{make(map[LockSetKey]*LockSet)}
}

func (lss *LockSetSet) Add(ss *LockSet) {
	lss.M[ss.Key()] = ss
}

func (lss *LockSetSet) Union(lss2 *LockSetSet) {
	if lss2 == nil {
		return
	}
	for k, ss := range lss2.M {
		lss.M[k] = ss
	}
}

func (lss *LockSetSet) ToSlice() []*LockSet {
	// TODO: Make deterministic?
	slice := make([]*LockSet, 0, len(lss.M))
	for _, ss := range lss.M {
		slice = append(slice, ss)
	}
	return slice
}

func (lss *LockSetSet) String() string {
	b := []byte("{")
	first := true
	for _, ss := range lss.M {
		if !first {
			b = append(b, ',')
		}
		first = false
		b = append(b, ss.String()...)
	}
	return string(append(b, '}'))
}

// funcInfo contains analysis state for a single function.
type funcInfo struct {
	// exitStates is a memoization cache that maps from the enter
	// PathState of state.walkFunction to its exit *PathStateSet.
	exitStates *PathStateMap

	// ifDeps records the set of control-flow dependencies for
	// each ssa.BasicBlock of this function. These are the values
	// at entry to each block that may affect future control flow
	// decisions.
	ifDeps []map[ssa.Instruction]struct{}

	// debugTree is the block trace debug tree for this function.
	// If nil, this function is not being debug traced.
	debugTree *DebugTree
}

// StackFrame is a stack of call sites. A nil *StackFrame represents
// an empty stack.
type StackFrame struct {
	parent *StackFrame
	call   ssa.Instruction
}

var internedStackFrames = make(map[StackFrame]*StackFrame)

// Flatten turns sf into a list of calls where the outer-most call is
// first.
func (sf *StackFrame) Flatten(into []ssa.Instruction) []ssa.Instruction {
	if sf == nil {
		if into == nil {
			return nil
		}
		return into[:0]
	}
	return append(sf.parent.Flatten(into), sf.call)
}

// Extend returns a new StackFrame that extends sf with call. call is
// typically an *ssa.Call, but other instructions can invoke runtime
// function calls as well.
func (sf *StackFrame) Extend(call ssa.Instruction) *StackFrame {
	return &StackFrame{sf, call}
}

// Intern returns a canonical *StackFrame such that a.Intern() ==
// b.Intern() iff a and b have the same sequence of calls.
func (sf *StackFrame) Intern() *StackFrame {
	if sf == nil {
		return nil
	}
	if sf, ok := internedStackFrames[*sf]; ok {
		return sf
	}
	nsf := sf.parent.Intern().Extend(sf.call)
	if nsf, ok := internedStackFrames[*nsf]; ok {
		return nsf
	}
	internedStackFrames[*nsf] = nsf
	return nsf
}

// TrimCommonPrefix eliminates the outermost frames that sf and other
// have in common and returns their distinct suffixes.
func (sf *StackFrame) TrimCommonPrefix(other *StackFrame, minLen int) (*StackFrame, *StackFrame) {
	var buf [64]ssa.Instruction
	f1 := sf.Flatten(buf[:])
	f2 := other.Flatten(f1[len(f1):cap(f1)])

	// Find the common prefix.
	var common int
	for common < len(f1)-minLen && common < len(f2)-minLen && f1[common] == f2[common] {
		common++
	}

	// Reconstitute.
	if common == 0 {
		return sf, other
	}
	var nsf1, nsf2 *StackFrame
	for _, call := range f1[common:] {
		nsf1 = nsf1.Extend(call)
	}
	for _, call := range f2[common:] {
		nsf2 = nsf2.Extend(call)
	}
	return nsf1, nsf2
}

type state struct {
	fset  *token.FileSet
	cg    *callgraph.Graph
	pta   *pointer.Result
	fns   map[*ssa.Function]*funcInfo
	stack *StackFrame

	// heap contains handles to heap objects that are needed by
	// specially handled functions.
	heap struct {
		curG       *HeapObject
		g0         *HeapObject
		curM       *HeapObject
		curM_locks *HeapObject
	}

	lca       LockClassAnalysis
	gscanLock *LockClass

	lockOrder *LockOrder

	// roots is the list of root functions to visit.
	roots   []*ssa.Function
	rootSet map[*ssa.Function]struct{}

	// debugTree, if non-nil is the function CFG debug tree.
	debugTree *DebugTree
	// debugging indicates that we're debugging this subgraph of
	// the CFG.
	debugging bool
}

func (s *state) warnl(pos token.Pos, format string, args ...interface{}) {
	// TODO: Suppress duplicate warnings.
	//
	// TODO: Have a different message for path terminating conditions.
	if pos.IsValid() {
		fmt.Printf("%s: ", s.fset.Position(pos))
	}
	fmt.Printf(format+"\n", args...)
}

func (s *state) warnp(pos token.Pos, format string, args ...interface{}) {
	s.warnl(pos, format+" at", args...)
	for stack := s.stack; stack != nil; stack = stack.parent {
		fmt.Printf("    %s\n", stack.call.Parent().String())
		fmt.Printf("        %s\n", s.fset.Position(stack.call.Pos()))
	}
}

// addRoot adds fn as a root of the control flow graph to visit.
func (s *state) addRoot(fn *ssa.Function) {
	if _, ok := s.rootSet[fn]; ok {
		return
	}
	s.roots = append(s.roots, fn)
	s.rootSet[fn] = struct{}{}
}

// callees returns the set of functions that call could possibly
// invoke. It returns nil for built-in functions or if pointer
// analysis failed.
func (s *state) callees(call ssa.CallInstruction) []*ssa.Function {
	if builtin, ok := call.Common().Value.(*ssa.Builtin); ok {
		// TODO: cap, len for map and channel
		switch builtin.Name() {
		case "append":
			return []*ssa.Function{fns.growslice}
		case "close":
			return []*ssa.Function{fns.closechan}
		case "copy":
			arg0 := builtin.Type().(*types.Signature).Params().At(0).Type().Underlying()
			if b, ok := arg0.(*types.Basic); ok && b.Kind() == types.String {
				return []*ssa.Function{fns.slicestringcopy}
			}
			return []*ssa.Function{fns.slicecopy}
		case "delete":
			return []*ssa.Function{fns.mapdelete}
		}

		// Ignore others.
		return nil
	}

	if fn := call.Common().StaticCallee(); fn != nil {
		return []*ssa.Function{fn}
	} else if cnode := s.cg.Nodes[call.Parent()]; cnode != nil {
		var callees []*ssa.Function
		// TODO: Build an index in walkFunction?
		for _, o := range cnode.Out {
			if o.Site != call {
				continue
			}
			callees = append(callees, o.Callee.Func)
		}
		return callees
	}

	s.warnl(call.Pos(), "no call graph for %v", call)
	return nil
}

// walkFunction explores f, starting at the given path state. It
// returns the set of path states possible on exit from f.
//
// ps should have block and mask set to nil, and ps.vs should be
// restricted to just heap values.
//
// Path states returned from walkFunction will likewise have block and
// mask set to nil and ps.vs restricted to just heap values.
//
// This implements the lockset algorithm from Engler and Ashcroft,
// SOSP 2003, plus simple path sensitivity to reduce mistakes from
// correlated control flow.
//
// TODO: This totally fails with multi-use higher-order functions,
// since the flow computed by the pointer analysis is not segregated
// by PathState.
//
// TODO: A lot of call trees simply don't take locks. We could record
// that fact and fast-path the entry locks to the exit locks.
func (s *state) walkFunction(f *ssa.Function, ps PathState) *PathStateSet {
	fInfo := s.fns[f]
	if fInfo == nil {
		// First visit of this function.

		// Compute control-flow dependencies.
		//
		// TODO: Figure out which control flow decisions
		// actually affect locking and only track those. Right
		// now we hit a lot of simple increment loops that
		// cause path aborts, but don't involve any locking.
		// Find all of the branches that could lead to a
		// lock/unlock (the may-precede set) and eliminate
		// those where both directions will always lead to the
		// lock/unlock anyway (where the lock/unlock is in the
		// must-succeed set). This can be answered with the
		// post-dominator tree. This is basically the same
		// computation we need to propagate liveness over
		// control flow.
		var ifInstrs []ssa.Instruction
		for _, b := range f.Blocks {
			if len(b.Instrs) == 0 {
				continue
			}
			instr, ok := b.Instrs[len(b.Instrs)-1].(*ssa.If)
			if !ok {
				continue
			}
			ifInstrs = append(ifInstrs, instr)
		}
		ifDeps := livenessFor(f, ifInstrs)
		if debugFunctions[f.String()] {
			f.WriteTo(os.Stderr)
			fmt.Fprintf(os.Stderr, "if deps:\n")
			for bid, vals := range ifDeps {
				fmt.Fprintf(os.Stderr, "  %d: ", bid)
				for dep := range vals {
					fmt.Fprintf(os.Stderr, " %s", dep.(ssa.Value).Name())
				}
				fmt.Fprintf(os.Stderr, "\n")
			}
		}

		fInfo = &funcInfo{
			exitStates: NewPathStateMap(),
			ifDeps:     ifDeps,
		}
		s.fns[f] = fInfo

		if f.Blocks == nil {
			s.warnl(f.Pos(), "external function %s", f)
		}

		if debugFunctions[f.String()] {
			fInfo.debugTree = new(DebugTree)
		}
	}

	if f.Blocks == nil {
		// External function. Assume it doesn't affect locks
		// or heap state.
		pss1 := NewPathStateSet()
		pss1.Add(ps)
		return pss1
	}

	if debugFunctions[f.String()] && s.debugging == false {
		// Turn on debugging of this subtree.
		if s.debugTree == nil {
			s.debugTree = new(DebugTree)
		}
		s.debugging = true
		defer func() { s.debugging = false }()
	}

	if s.debugging {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%s\n- enter -\n", f)
		ps.WriteTo(&buf)
		s.debugTree.Push(buf.String())
		defer s.debugTree.Pop()
	}

	// Check memoization cache.
	//
	// TODO: Our lockset can differ from a cached lockset by only
	// the stacks of the locks. Can we do something smarter than
	// recomputing the entire sub-graph in that situation? It's
	// rather complex because we may alter the lock order graph
	// with new stacks in the process. One could imagine tracking
	// a "predicate" and a compressed "delta" for the computation
	// and caching that.
	if memo := fInfo.exitStates.Get(ps); memo != nil {
		if s.debugging {
			s.debugTree.Appendf("\n- cached exit -\n%v", memo)
		}
		return memo.(*PathStateSet)
	}

	if fInfo.debugTree != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "%s\n- enter -\n", f)
		ps.WriteTo(&buf)
		fInfo.debugTree.Push(buf.String())
		defer fInfo.debugTree.Pop()
	}

	// Resolve function cycles by returning an empty set of
	// locksets, which terminates this code path.
	//
	// TODO: RacerX detects cycles *without* regard to the entry
	// lock set. We could do that, but it doesn't seem to be an
	// issue to include the lock set. However, since we have the
	// lock set, maybe if we have a cycle with a non-empty lock
	// set we should report a self-deadlock.
	fInfo.exitStates.Set(ps, emptyPathStateSet)

	blockCache := NewPathStateSet()
	enterPathState := PathState{f.Blocks[0], ps.lockSet, ps.vs, nil}
	exitStates := NewPathStateSet()
	s.walkBlock(blockCache, enterPathState, exitStates)
	fInfo.exitStates.Set(ps, exitStates)
	//log.Printf("%s: %s -> %s", f.Name(), locks, exitStates)
	if s.debugging {
		s.debugTree.Appendf("\n- exit -\n%v", exitStates)
	}
	return exitStates
}

// PathState is the state during execution of a particular function.
type PathState struct {
	block   *ssa.BasicBlock
	lockSet *LockSet
	vs      ValState
	mask    map[ssa.Instruction]struct{}
}

type pathStateKey struct {
	block   *ssa.BasicBlock
	lockSet string
}

// HashKey returns a key such that ps1.Equal(ps2) implies
// ps1.HashKey() == ps2.HashKey().
func (ps *PathState) HashKey() pathStateKey {
	// Note that PathStateSet.Contains depends on this capturing
	// everything except the stacks and value state.
	return pathStateKey{ps.block, ps.lockSet.HashKey()}
}

// Equal returns whether ps and ps2 have represent the same program
// state.
func (ps *PathState) Equal(ps2 *PathState) bool {
	// ps.block == ps2.block implies ps.mask == ps2.mask, so this
	// is symmetric. Maybe we should just keep pre-masked
	// ValStates.
	return ps.block == ps2.block && ps.lockSet.Equal(ps2.lockSet) && ps.vs.EqualAt(ps2.vs, ps.mask)
}

// ExitState returns ps narrowed to the path state tracked across a
// function return.
func (ps *PathState) ExitState() PathState {
	return PathState{
		lockSet: ps.lockSet,
		vs:      ps.vs.LimitToHeap(),
	}
}

func (ps *PathState) WriteTo(w io.Writer) {
	if ps.block == nil {
		fmt.Fprintf(w, "PathState for function:\n")
	} else {
		fmt.Fprintf(w, "PathState for %s block %d:\n", ps.block.Parent(), ps.block.Index)
	}
	fmt.Fprintf(w, "  locks: %v\n", ps.lockSet)
	fmt.Fprintf(w, "  values:\n")
	ps.vs.WriteTo(&IndentWriter{W: w, Indent: []byte("    ")})
}

// PathStateSet is a mutable set of PathStates.
type PathStateSet struct {
	m map[pathStateKey][]PathState
}

// NewPathStateSet returns a new, empty PathStateSet.
func NewPathStateSet() *PathStateSet {
	return &PathStateSet{make(map[pathStateKey][]PathState)}
}

var emptyPathStateSet = NewPathStateSet()

func (set *PathStateSet) Empty() bool {
	return len(set.m) == 0
}

// Add adds PathState ps to set.
func (set *PathStateSet) Add(ps PathState) {
	key := ps.HashKey()
	slice := set.m[key]
	for i := range slice {
		if slice[i].Equal(&ps) {
			return
		}
	}
	set.m[key] = append(slice, ps)
}

// Contains returns whether set contains ps and the number of
// PathStates that differ only in value state and lock stacks.
func (set *PathStateSet) Contains(ps PathState) (bool, int) {
	// The "similar" count depends on the implementation of
	// PathState.HashKey.
	key := ps.HashKey()
	slice := set.m[key]
	for i := range slice {
		if slice[i].Equal(&ps) {
			return true, len(slice)
		}
	}
	return false, len(slice)
}

// MapInPlace applies f to each PathState in set and replaces that
// PathState with f's result. This is optimized for the case where f
// returns the same PathState.
func (set *PathStateSet) MapInPlace(f func(ps PathState) PathState) {
	var toAdd []PathState
	for hashKey, slice := range set.m {
		for i := 0; i < len(slice); i++ {
			ps2 := f(slice[i])
			if slice[i].Equal(&ps2) {
				continue
			}
			// Remove ps from the set and queue ps2 to add.
			slice[i] = slice[len(slice)-1]
			slice = slice[:len(slice)-1]
			if len(slice) == 0 {
				delete(set.m, hashKey)
			} else {
				set.m[hashKey] = slice
			}
			toAdd = append(toAdd, ps2)
		}
	}
	for _, ps := range toAdd {
		set.Add(ps)
	}
}

// ForEach applies f to each PathState in set.
func (set *PathStateSet) ForEach(f func(ps PathState)) {
	for _, slice := range set.m {
		for i := range slice {
			f(slice[i])
		}
	}
}

// FlatMap applies f to each PathState in set and returns a new
// PathStateSet consisting of the union of f's results. f may use
// scratch as temporary space and may return it; this will always be a
// slice with length 0.
func (set *PathStateSet) FlatMap(f func(ps PathState, scatch []PathState) []PathState) *PathStateSet {
	var scratch [16]PathState
	out := NewPathStateSet()
	for _, slice := range set.m {
		for _, ps := range slice {
			for _, nps := range f(ps, scratch[:0]) {
				out.Add(nps)
			}
		}
	}
	return out
}

// PathStateMap is a mutable map keyed by PathState.
type PathStateMap struct {
	m map[pathStateKey][]pathStateMapEntry
}

type pathStateMapEntry struct {
	ps  PathState
	val interface{}
}

// NewPathStateMap returns a new empty PathStateMap.
func NewPathStateMap() *PathStateMap {
	return &PathStateMap{make(map[pathStateKey][]pathStateMapEntry)}
}

// Set sets the value associated with ps to val in psm.
func (psm *PathStateMap) Set(ps PathState, val interface{}) {
	key := ps.HashKey()
	slice := psm.m[key]
	for i := range slice {
		if slice[i].ps.Equal(&ps) {
			slice[i].val = val
			return
		}
	}
	psm.m[key] = append(slice, pathStateMapEntry{ps, val})
}

// Get returns the value associated with ps in psm.
func (psm *PathStateMap) Get(ps PathState) interface{} {
	slice := psm.m[ps.HashKey()]
	for i := range slice {
		if slice[i].ps.Equal(&ps) {
			return slice[i].val
		}
	}
	return nil
}

// walkBlock visits a block and all blocks reachable from it, starting
// from the path state enterPathState. When walkBlock reaches the
// return point of the function, it adds the possible path states at
// that point to exitStates. blockCache is the set of already
// visited path states within this function as of the beginning of
// visited blocks.
func (s *state) walkBlock(blockCache *PathStateSet, enterPathState PathState, exitStates *PathStateSet) {
	b := enterPathState.block
	f := b.Parent()
	// Check the values that are live at this
	// block. Note that the live set includes phis
	// at the beginning of this block if they
	// participate in control flow decisions, so
	// we'll pick up any phi values assigned by
	// our called.
	enterPathState.mask = s.fns[f].ifDeps[b.Index]

	debugTree := s.fns[f].debugTree
	if debugTree != nil {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "block %v\n", b.Index)
		enterPathState.WriteTo(&buf)
		debugTree.Push(buf.String())
		defer debugTree.Pop()
	}

	if cached, similar := blockCache.Contains(enterPathState); cached {
		// Terminate recursion. Some other path has already
		// visited here with this lock set and value state.
		if debugTree != nil {
			debugTree.Leaf("cached")
		}
		return
	} else if similar > 10 {
		s.warnl(blockPos(b), "too many states, trimming path (block %d)", b.Index)
		if debugTree != nil {
			debugTree.Leaf("too many states")
		}
		return
	}
	blockCache.Add(enterPathState)

	// Upon block entry there's just the one entry path state.
	pathStates := NewPathStateSet()
	pathStates.Add(enterPathState)

	doCall := func(instr ssa.Instruction, fns []*ssa.Function) {
		s.stack = s.stack.Extend(instr)
		pathStates = pathStates.FlatMap(func(ps PathState, newps []PathState) []PathState {
			psEntry := PathState{
				lockSet: ps.lockSet,
				vs:      ps.vs.LimitToHeap(),
			}
			for _, fn := range fns {
				if handler, ok := callHandlers[fn.String()]; ok {
					// TODO: Instead of using
					// FlatMap, I could just pass
					// the PathStateSet to add new
					// states to.
					newps = handler(s, ps, instr, newps)
				} else {
					s.walkFunction(fn, psEntry).ForEach(func(ps2 PathState) {
						ps.lockSet = ps2.lockSet
						ps.vs.heap = ps2.vs.heap
						newps = append(newps, ps)
					})
				}
			}
			return newps
		})
		s.stack = s.stack.parent
	}

	// For each instruction, compute the effect of that
	// instruction on all possible path states at that point.
	var ifCond ssa.Value
	for _, instr := range b.Instrs {
		// Update value state with the effect of this
		// instruction.
		pathStates.MapInPlace(func(ps PathState) PathState {
			ps.vs = ps.vs.Do(instr)
			return ps
		})

		switch instr := instr.(type) {
		case *ssa.If:
			// We'll bind ifCond to true or false when we
			// visit successors.
			ifCond = instr.Cond

		case *ssa.Call:
			// TODO: There are other types of
			// ssa.CallInstructions, but they have different
			// control flow.
			outs := s.callees(instr)
			if len(outs) == 0 {
				// This is a built-in like print or
				// len. Assume it doesn't affect the
				// locksets.
				break
			}
			doCall(instr, outs)

		// TODO: runtime calls for ssa.ChangeInterface,
		// ssa.Convert, ssa.Defer, ssa.MakeInterface,
		// ssa.Next, ssa.Range, ssa.Select, ssa.TypeAssert.

		// Unfortunately, we can't turn ssa.Alloc into a
		// newobject call because ssa turns any variable
		// captured by a closure into an Alloc. There's no way
		// to tell if it was actually a new() expression or
		// not.
		// case *ssa.Alloc:
		// 	if instr.Heap {
		// 		doCall(instr, []*ssa.Function{fns.newobject})
		// 	}

		case *ssa.Lookup:
			if _, ok := instr.X.Type().Underlying().(*types.Map); !ok {
				break
			}
			if instr.CommaOk {
				doCall(instr, []*ssa.Function{fns.mapaccess2})
			} else {
				doCall(instr, []*ssa.Function{fns.mapaccess1})
			}

		case *ssa.MakeChan:
			doCall(instr, []*ssa.Function{fns.makechan})

		case *ssa.MakeMap:
			doCall(instr, []*ssa.Function{fns.makemap})

		case *ssa.MakeSlice:
			doCall(instr, []*ssa.Function{fns.newarray})

		case *ssa.MapUpdate:
			doCall(instr, []*ssa.Function{fns.mapassign1})

		case *ssa.Panic:
			doCall(instr, []*ssa.Function{fns.gopanic})

		case *ssa.Send:
			doCall(instr, []*ssa.Function{fns.chansend1})

		case *ssa.Go:
			for _, o := range s.callees(instr) {
				//log.Printf("found go %s; adding to roots", o)
				s.addRoot(o)
			}

		case *ssa.Return:
			// We've reached function exit. Add the
			// current lock sets to exitLockSets.
			//
			// TODO: Handle defers.

			pathStates.ForEach(func(ps PathState) {
				exitStates.Add(ps.ExitState())
				if debugTree != nil {
					var buf bytes.Buffer
					ps.WriteTo(&buf)
					debugTree.Leaff("exit:\n%s", buf)
				}
			})
		}
	}

	// Annoyingly, the last instruction in an ssa.BasicBlock
	// doesn't have a location, even if it obviously corresponds
	// to a source statement. exitPos guesses one.
	exitPos := func(b *ssa.BasicBlock) token.Pos {
		for b != nil {
			for i := len(b.Instrs) - 1; i >= 0; i-- {
				if pos := b.Instrs[i].Pos(); pos != 0 {
					return pos
				}
			}
			if len(b.Preds) == 0 {
				break
			}
			b = b.Preds[0]
		}
		return 0
	}
	_ = exitPos

	if len(pathStates.m) == 0 && debugTree != nil {
		// This happens after functions that don't return.
		debugTree.Leaf("no path states")
	}

	// Process successor blocks.
	pathStates.ForEach(func(ps PathState) {
		// If this is an "if", see if we have enough
		// information to determine its direction.
		succs := b.Succs
		if ifCond != nil {
			x := ps.vs.Get(ifCond)
			if x != nil {
				//log.Printf("determined control flow at %s: %v", s.fset.Position(exitPos(b)), x)
				if constant.BoolVal(x.(DynConst).c) {
					// Take true path.
					succs = succs[:1]
				} else {
					// Take false path.
					succs = succs[1:]
				}
			}
		}

		// Process block successors.
		for i, b2 := range succs {
			ps2 := ps
			ps2.block = b2
			if ifCond != nil {
				// TODO: We could back-propagate this
				// in simple cases, like when ifCond
				// is a == BinOp. (And we could
				// forward-propagate that! Hmm.)
				ps2.vs = ps2.vs.Extend(ifCond, DynConst{constant.MakeBool(i == 0)})
			}

			// Propagate values over phis at the beginning
			// of b2.
			for _, instr := range b2.Instrs {
				instr, ok := instr.(*ssa.Phi)
				if !ok {
					break
				}
				for i, inval := range instr.Edges {
					if b2.Preds[i] == b {
						x := ps2.vs.Get(inval)
						if x != nil {
							ps2.vs = ps2.vs.Extend(instr, x)
						}
					}
				}
			}

			if debugTree != nil && len(b.Succs) > 1 {
				if b2 == b.Succs[0] {
					debugTree.SetEdge("T")
				} else if b2 == b.Succs[1] {
					debugTree.SetEdge("F")
				}
			}
			s.walkBlock(blockCache, ps2, exitStates)
		}
	})
}

// blockPos returns the best position it can for b.
func blockPos(b *ssa.BasicBlock) token.Pos {
	var visited []bool
	for {
		if visited != nil {
			if visited[b.Index] {
				// Give up.
				return b.Parent().Pos()
			}
			visited[b.Index] = true
		}
		// Phis have useless line numbers. Find the first
		// "real" instruction.
		for _, i := range b.Instrs {
			if _, ok := i.(*ssa.Phi); ok || !i.Pos().IsValid() {
				continue
			}
			return i.Pos()
		}
		if len(b.Preds) == 0 {
			return b.Parent().Pos()
		}
		// Try b's predecessor.
		if visited == nil {
			// Delayed allocation of visited.
			visited = make([]bool, len(b.Parent().Blocks))
			visited[b.Index] = true
		}
		b = b.Preds[0]
	}
}
