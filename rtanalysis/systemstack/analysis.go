package systemstack

import (
	"fmt"
	"go/ast"
	"reflect"

	"github.com/aclements/go-misc/rtanalysis/directives"
	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name:       "onsystemstack",
	Doc:        "determines functions that always run on the systemstack",
	Run:        run,
	ResultType: reflect.TypeOf(Result(nil)),
	Requires:   []*analysis.Analyzer{directives.Analyzer},
}

type Func struct {
	// Node is either a *ast.FuncDecl or an *ast.FuncLit.
	Node ast.Node
}

func (f Func) String() string {
	switch f := f.Node.(type) {
	case *ast.FuncDecl:
		return f.Name.String()
	case *ast.FuncLit:
		return fmt.Sprintf("func@%v", f.Pos())
	}
	return "<bad Func type>"
}

type Result map[Func]bool

func run(pass *analysis.Pass) (interface{}, error) {
	res := Result{}

	// Seed functions that always run on the system stack.
	directives := pass.ResultOf[directives.Analyzer].(directives.Result)
	for obj, dirs := range directives {
		for _, dir := range dirs {
			if dir == "//go:systemstack" {
				res[Func{obj}] = true
				break
			}
		}
	}

	// TODO: Derive all this through the call graph, then complain
	// if we find a non-systemstack path to a go:systemstack
	// function.

	// Collect call graph and entry points to the system stack.
	unknownCaller := Func{&ast.FuncDecl{}}
	systemstack := Func{&ast.FuncDecl{}}
	callers := map[Func][]Func{}
	var caller Func
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			var id *ast.Ident
			switch fun := call.Fun.(type) {
			case *ast.Ident:
				if fun.Name == "systemstack" || fun.Name == "mcall" {
					if len(call.Args) != 1 {
						pass.Reportf(call.Pos(), "wrong number of arguments to %s", fun.Name)
						return true
					}
					var target Func
					switch arg := call.Args[0].(type) {
					case *ast.Ident:
						t, ok := arg.Obj.Decl.(*ast.FuncDecl)
						if ok {
							target = Func{t}
						} else {
							pass.Reportf(call.Pos(), "%s argument isn't a static function", fun.Name)
						}
					case *ast.FuncLit:
						target = Func{arg}
					}
					fmt.Println("systemstack ->", target) // XXX
					callers[target] = append(callers[target], systemstack)
					// Don't descend into arguments.
					return false
				}
				id = fun
			case *ast.SelectorExpr:
				id = fun.Sel
			}
			if id != nil && !pass.TypesInfo.Types[id].IsType() && id.Obj != nil {
				t, ok := id.Obj.Decl.(*ast.FuncDecl)
				if ok {
					target := Func{t}
					fmt.Println(caller, "->", target) // XXX
					callers[target] = append(callers[target], caller)
					// Don't walk into call.Func.
					for _, n := range call.Args {
						ast.Inspect(n, visit)
					}
					return false
				}
			}
			pass.Reportf(call.Pos(), "unhandled call")
		} else if id, ok := n.(*ast.Ident); ok {
			fmt.Println("XXX", caller, id, id.Obj)
			if id.Obj != nil {
				if fun, ok := id.Obj.Decl.(*ast.FuncDecl); ok {
					// Bare call. We don't know how we enter it.
					fmt.Println("unknown ->", fun) // XXX
					callers[Func{fun}] = append(callers[Func{fun}], unknownCaller)
				}
			}
		}
		// XXX Walk into closures.
		return true
	}
	for _, f := range pass.Files {
		for _, decl := range f.Decls {
			fdecl, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			caller = Func{fdecl}
			fmt.Println("visit", caller) // XXX
			ast.Inspect(fdecl, visit)
			if fdecl.Name.IsExported() {
				// Exported functions have unknown callers.
				callers[Func{fdecl}] = append(callers[Func{fdecl}], unknownCaller)
			}
		}
	}

	fmt.Println(callers)
	return res, nil
}
