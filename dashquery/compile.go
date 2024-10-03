// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dashquery

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"time"
)

type compiler struct {
	names map[string]queryNode
}

func newCompiler(names map[string]queryNode) *compiler {
	return &compiler{names}
}

func (c *compiler) compile(expr string) (boolNode, error) {
	fset := token.NewFileSet()
	ast, err := parser.ParseExprFrom(fset, "", expr, 0)
	if err != nil {
		return nil, err
	}

	// Translate AST into nested closures and type-check.
	var fn boolNode
	func() {
		defer func() {
			err2 := recover()
			if err2, ok := err2.(*compileError); ok {
				err = err2
			} else if err2 != nil {
				panic(err2)
			}
		}()
		fn = c.bool(ast, c.expr(ast))
	}()
	if err != nil {
		return nil, err
	}
	return fn, nil
}

// bad panics with a compileError for the given message.
func (c *compiler) bad(ast ast.Node, format string, a ...interface{}) {
	// TODO: Report position information from ast.
	panic(&compileError{format, a})
}

type compileError struct {
	format string
	a      []interface{}
}

func (e *compileError) Error() string {
	return fmt.Sprintf(e.format, e.a...)
}

type queryNode interface {
	// typ returns the type of this node's result as a string.
	typ() string
	// cfunc returns an evaluation function that wraps its result
	// in constant.Value.
	cfunc() func(pathInfo) constant.Value
}

type (
	boolNode   func(pi pathInfo) bool
	numberNode func(pi pathInfo) constant.Value
	stringNode func(pi pathInfo) string
	timeNode   func(pi pathInfo) time.Time
)

func (boolNode) typ() string   { return "bool" }
func (numberNode) typ() string { return "number" }
func (stringNode) typ() string { return "string" }
func (timeNode) typ() string   { return "time" }

func (n boolNode) cfunc() func(pathInfo) constant.Value {
	return func(pi pathInfo) constant.Value {
		return constant.MakeBool(n(pi))
	}
}
func (n numberNode) cfunc() func(pathInfo) constant.Value {
	return (func(pathInfo) constant.Value)(n)
}
func (n stringNode) cfunc() func(pathInfo) constant.Value {
	return func(pi pathInfo) constant.Value {
		return constant.MakeString(n(pi))
	}
}
func (n timeNode) cfunc() func(pathInfo) constant.Value {
	// Should never happen.
	panic("timeNode.cfunc")
}

// bool returns n as a boolNode or panics with a type error.
func (c *compiler) bool(ast ast.Expr, n queryNode) boolNode {
	fn, ok := n.(boolNode)
	if !ok {
		c.bad(ast, "want bool, but %s has type %s", ast, n.typ())
	}
	return fn
}

// number returns n as a numberNode or panics with a type error.
func (c *compiler) number(ast ast.Expr, n queryNode) numberNode {
	fn, ok := n.(numberNode)
	if !ok {
		c.bad(ast, "want number, but %s has type %s", ast, n.typ())
	}
	return fn
}

// oneOf requires that x's type be one of typs.
func (c *compiler) oneOf(ast ast.Expr, x queryNode, typs ...string) {
	have := x.typ()
	for _, typ := range typs {
		if have == typ {
			return
		}
	}
	want := ""
	switch len(typs) {
	case 1:
		want = typs[0]
	case 2:
		want = typs[0] + " or " + typs[1]
	default:
		for i, typ := range typs {
			if i == len(typs)-1 {
				want += ", or"
			} else if i > 0 {
				want += ", "
			}
			want += typ
		}
	}
	c.bad(ast, "want %s, but %s has type %s", want, ast, x.typ())
}

// sameType requires that x and y have the same type and that both are
// one of typs.
func (c *compiler) sameType(ast *ast.BinaryExpr, x, y queryNode, typs ...string) {
	c.oneOf(ast.X, x, typs...)
	c.oneOf(ast.Y, y, typs...)
	if x.typ() != y.typ() {
		c.bad(ast, "operands of %s must have same type, not %s and %s", ast, x.typ(), y.typ())
	}
}

// expr type-checks and compiles expr to a queryNode.
func (c *compiler) expr(expr ast.Expr) queryNode {
	switch expr := expr.(type) {
	case *ast.BasicLit:
		v := constant.MakeFromLiteral(expr.Value, expr.Kind, 0)
		switch expr.Kind {
		case token.INT, token.FLOAT:
			return numberNode(func(pi pathInfo) constant.Value {
				return v
			})
		case token.STRING:
			str := constant.StringVal(v)
			return stringNode(func(pi pathInfo) string {
				return str
			})
		}

	case *ast.BinaryExpr:
		x, y := c.expr(expr.X), c.expr(expr.Y)
		switch expr.Op {
		case token.ADD:
			c.sameType(expr, x, y, "number", "string")
			switch x := x.(type) {
			case numberNode:
				y := y.(numberNode)
				return numberNode(func(pi pathInfo) constant.Value {
					return constant.BinaryOp(x(pi), expr.Op, y(pi))
				})
			case stringNode:
				y := y.(stringNode)
				return stringNode(func(pi pathInfo) string {
					return x(pi) + y(pi)
				})
			}
		case token.SUB, token.MUL, token.QUO, token.REM:
			x, y := c.number(expr.X, x), c.number(expr.Y, y)
			return numberNode(func(pi pathInfo) constant.Value {
				return constant.BinaryOp(x(pi), expr.Op, y(pi))
			})

			// TODO: AND, OR, XOR, SHL, SHR, AND_NOT

		case token.LAND:
			x, y := c.bool(expr.X, x), c.bool(expr.Y, y)
			return boolNode(func(pi pathInfo) bool {
				return x(pi) && y(pi)
			})
		case token.LOR:
			x, y := c.bool(expr.X, x), c.bool(expr.Y, y)
			return boolNode(func(pi pathInfo) bool {
				return x(pi) || y(pi)
			})

		case token.LSS, token.GTR, token.LEQ, token.GEQ:
			c.sameType(expr, x, y, "number", "string", "time")
			fallthrough
		case token.EQL, token.NEQ:
			c.sameType(expr, x, y, "bool", "number", "string", "time")
			if x, ok := x.(timeNode); ok {
				y := y.(timeNode)
				return boolNode(func(pi pathInfo) bool {
					xv := constant.MakeInt64(x(pi).UnixNano())
					yv := constant.MakeInt64(y(pi).UnixNano())
					return constant.Compare(xv, expr.Op, yv)
				})
			}
			x, y := x.cfunc(), y.cfunc()
			return boolNode(func(pi pathInfo) bool {
				return constant.Compare(x(pi), expr.Op, y(pi))
			})
		}

	case *ast.CallExpr:
		// TODO: This is awful. Have a real function node.
		id, ok := expr.Fun.(*ast.Ident)
		if !ok {
			c.bad(expr, "bad call %s", expr)
		}
		switch id.Name {
		case "date":
			// TODO: Parse date argument. Would be nice if
			// we could constant-fold.
		}
		c.bad(expr, "undefined: %s", id.Name)

	case *ast.Ident:
		if node, ok := c.names[expr.Name]; ok {
			return node
		}
		c.bad(expr, "undefined: %s", expr.Name)

		// TODO: IndexExpr? SliceExpr?

	case *ast.ParenExpr:
		return c.expr(expr.X)

	case *ast.UnaryExpr:
		x := c.expr(expr.X)
		switch expr.Op {
		case token.ADD, token.SUB:
			x := c.number(expr.X, x)
			return numberNode(func(pi pathInfo) constant.Value {
				return constant.UnaryOp(expr.Op, x(pi), 0)
			})
			// TODO: XOR
		case token.NOT:
			x := c.bool(expr.X, x)
			return boolNode(func(pi pathInfo) bool {
				return !x(pi)
			})
		}
	}

	c.bad(expr, "unsupported expression %s", expr)
	return nil
}
