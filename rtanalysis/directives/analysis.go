package directives

import (
	"go/ast"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name:       "directives",
	Doc:        "collect //go:* directives for function declarations",
	Run:        run,
	ResultType: reflect.TypeOf(Result(nil)),
}

type Result map[*ast.FuncDecl][]string

func run(pass *analysis.Pass) (interface{}, error) {
	res := Result{}
	for _, f := range pass.Files {
		cgs := f.Comments
		for _, decl := range f.Decls {
			// Process comments before decl.
			var directives []string
			for len(cgs) > 0 && cgs[0].Pos() < decl.Pos() {
				for _, c := range cgs[0].List {
					if strings.HasPrefix(c.Text, "//go:") {
						directives = append(directives, strings.TrimSpace(c.Text))
					}
				}
				cgs = cgs[1:]
			}
			// Ignore comments in decl.
			for len(cgs) > 0 && cgs[0].Pos() < decl.End() {
				cgs = cgs[1:]
			}
			// Attach directives to decl.
			if len(directives) > 0 {
				switch decl := decl.(type) {
				case *ast.FuncDecl:
					res[decl] = directives
				}
			}
		}
	}
	return res, nil
}
