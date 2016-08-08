// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"go/ast"
)

func rewriteIdentList(v func(ast.Node) ast.Node, list []*ast.Ident) {
	for i, x := range list {
		list[i] = Rewrite(v, x).(*ast.Ident)
	}
}

func rewriteExprList(v func(ast.Node) ast.Node, list []ast.Expr) {
	for i, x := range list {
		list[i] = Rewrite(v, x).(ast.Expr)
	}
}

func rewriteStmtList(v func(ast.Node) ast.Node, list []ast.Stmt) {
	for i, x := range list {
		list[i] = Rewrite(v, x).(ast.Stmt)
	}
}

func rewriteDeclList(v func(ast.Node) ast.Node, list []ast.Decl) {
	for i, x := range list {
		list[i] = Rewrite(v, x).(ast.Decl)
	}
}

func Rewrite(v func(ast.Node) ast.Node, node ast.Node) ast.Node {
	node = v(node)

	// rewrite children
	// (the order of the cases matches the order
	// of the corresponding node types in ast.go)
	switch n := node.(type) {
	// Comments and fields
	case *ast.Comment:
		// nothing to do

	case *ast.CommentGroup:
		for i, c := range n.List {
			n.List[i] = Rewrite(v, c).(*ast.Comment)
		}

	case *ast.Field:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		rewriteIdentList(v, n.Names)
		n.Type = Rewrite(v, n.Type).(ast.Expr)
		if n.Tag != nil {
			n.Tag = Rewrite(v, n.Tag).(*ast.BasicLit)
		}
		if n.Comment != nil {
			n.Comment = Rewrite(v, n.Comment).(*ast.CommentGroup)
		}

	case *ast.FieldList:
		for i, f := range n.List {
			n.List[i] = Rewrite(v, f).(*ast.Field)
		}

	// Expressions
	case *ast.BadExpr, *ast.Ident, *ast.BasicLit:
		// nothing to do

	case *ast.Ellipsis:
		if n.Elt != nil {
			n.Elt = Rewrite(v, n.Elt).(ast.Expr)
		}

	case *ast.FuncLit:
		n.Type = Rewrite(v, n.Type).(*ast.FuncType)
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	case *ast.CompositeLit:
		if n.Type != nil {
			n.Type = Rewrite(v, n.Type).(ast.Expr)
		}
		rewriteExprList(v, n.Elts)

	case *ast.ParenExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)

	case *ast.SelectorExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)
		n.Sel = Rewrite(v, n.Sel).(*ast.Ident)

	case *ast.IndexExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)
		n.Index = Rewrite(v, n.Index).(ast.Expr)

	case *ast.SliceExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)
		if n.Low != nil {
			n.Low = Rewrite(v, n.Low).(ast.Expr)
		}
		if n.High != nil {
			n.High = Rewrite(v, n.High).(ast.Expr)
		}
		if n.Max != nil {
			n.Max = Rewrite(v, n.Max).(ast.Expr)
		}

	case *ast.TypeAssertExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)
		if n.Type != nil {
			n.Type = Rewrite(v, n.Type).(ast.Expr)
		}

	case *ast.CallExpr:
		n.Fun = Rewrite(v, n.Fun).(ast.Expr)
		rewriteExprList(v, n.Args)

	case *ast.StarExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)

	case *ast.UnaryExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)

	case *ast.BinaryExpr:
		n.X = Rewrite(v, n.X).(ast.Expr)
		n.Y = Rewrite(v, n.Y).(ast.Expr)

	case *ast.KeyValueExpr:
		n.Key = Rewrite(v, n.Key).(ast.Expr)
		n.Value = Rewrite(v, n.Value).(ast.Expr)

	// Types
	case *ast.ArrayType:
		if n.Len != nil {
			n.Len = Rewrite(v, n.Len).(ast.Expr)
		}
		n.Elt = Rewrite(v, n.Elt).(ast.Expr)

	case *ast.StructType:
		n.Fields = Rewrite(v, n.Fields).(*ast.FieldList)

	case *ast.FuncType:
		if n.Params != nil {
			n.Params = Rewrite(v, n.Params).(*ast.FieldList)
		}
		if n.Results != nil {
			n.Results = Rewrite(v, n.Results).(*ast.FieldList)
		}

	case *ast.InterfaceType:
		n.Methods = Rewrite(v, n.Methods).(*ast.FieldList)

	case *ast.MapType:
		n.Key = Rewrite(v, n.Key).(ast.Expr)
		n.Value = Rewrite(v, n.Value).(ast.Expr)

	case *ast.ChanType:
		n.Value = Rewrite(v, n.Value).(ast.Expr)

	// Statements
	case *ast.BadStmt:
		// nothing to do

	case *ast.DeclStmt:
		n.Decl = Rewrite(v, n.Decl).(ast.Decl)

	case *ast.EmptyStmt:
		// nothing to do

	case *ast.LabeledStmt:
		n.Label = Rewrite(v, n.Label).(*ast.Ident)
		n.Stmt = Rewrite(v, n.Stmt).(ast.Stmt)

	case *ast.ExprStmt:
		n.X = Rewrite(v, n.X).(ast.Expr)

	case *ast.SendStmt:
		n.Chan = Rewrite(v, n.Chan).(ast.Expr)
		n.Value = Rewrite(v, n.Value).(ast.Expr)

	case *ast.IncDecStmt:
		n.X = Rewrite(v, n.X).(ast.Expr)

	case *ast.AssignStmt:
		rewriteExprList(v, n.Lhs)
		rewriteExprList(v, n.Rhs)

	case *ast.GoStmt:
		n.Call = Rewrite(v, n.Call).(*ast.CallExpr)

	case *ast.DeferStmt:
		n.Call = Rewrite(v, n.Call).(*ast.CallExpr)

	case *ast.ReturnStmt:
		rewriteExprList(v, n.Results)

	case *ast.BranchStmt:
		if n.Label != nil {
			n.Label = Rewrite(v, n.Label).(*ast.Ident)
		}

	case *ast.BlockStmt:
		rewriteStmtList(v, n.List)

	case *ast.IfStmt:
		if n.Init != nil {
			n.Init = Rewrite(v, n.Init).(ast.Stmt)
		}
		n.Cond = Rewrite(v, n.Cond).(ast.Expr)
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)
		if n.Else != nil {
			n.Else = Rewrite(v, n.Else).(ast.Stmt)
		}

	case *ast.CaseClause:
		rewriteExprList(v, n.List)
		rewriteStmtList(v, n.Body)

	case *ast.SwitchStmt:
		if n.Init != nil {
			n.Init = Rewrite(v, n.Init).(ast.Stmt)
		}
		if n.Tag != nil {
			n.Tag = Rewrite(v, n.Tag).(ast.Expr)
		}
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	case *ast.TypeSwitchStmt:
		if n.Init != nil {
			n.Init = Rewrite(v, n.Init).(ast.Stmt)
		}
		n.Assign = Rewrite(v, n.Assign).(ast.Stmt)
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	case *ast.CommClause:
		if n.Comm != nil {
			n.Comm = Rewrite(v, n.Comm).(ast.Stmt)
		}
		rewriteStmtList(v, n.Body)

	case *ast.SelectStmt:
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	case *ast.ForStmt:
		if n.Init != nil {
			n.Init = Rewrite(v, n.Init).(ast.Stmt)
		}
		if n.Cond != nil {
			n.Cond = Rewrite(v, n.Cond).(ast.Expr)
		}
		if n.Post != nil {
			n.Post = Rewrite(v, n.Post).(ast.Stmt)
		}
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	case *ast.RangeStmt:
		if n.Key != nil {
			n.Key = Rewrite(v, n.Key).(ast.Expr)
		}
		if n.Value != nil {
			n.Value = Rewrite(v, n.Value).(ast.Expr)
		}
		n.X = Rewrite(v, n.X).(ast.Expr)
		n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)

	// Declarations
	case *ast.ImportSpec:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		if n.Name != nil {
			n.Name = Rewrite(v, n.Name).(*ast.Ident)
		}
		n.Path = Rewrite(v, n.Path).(*ast.BasicLit)
		if n.Comment != nil {
			n.Comment = Rewrite(v, n.Comment).(*ast.CommentGroup)
		}

	case *ast.ValueSpec:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		rewriteIdentList(v, n.Names)
		if n.Type != nil {
			n.Type = Rewrite(v, n.Type).(ast.Expr)
		}
		rewriteExprList(v, n.Values)
		if n.Comment != nil {
			n.Comment = Rewrite(v, n.Comment).(*ast.CommentGroup)
		}

	case *ast.TypeSpec:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		n.Name = Rewrite(v, n.Name).(*ast.Ident)
		n.Type = Rewrite(v, n.Type).(ast.Expr)
		if n.Comment != nil {
			n.Comment = Rewrite(v, n.Comment).(*ast.CommentGroup)
		}

	case *ast.BadDecl:
		// nothing to do

	case *ast.GenDecl:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		for i, s := range n.Specs {
			n.Specs[i] = Rewrite(v, s).(ast.Spec)
		}

	case *ast.FuncDecl:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		if n.Recv != nil {
			n.Recv = Rewrite(v, n.Recv).(*ast.FieldList)
		}
		n.Name = Rewrite(v, n.Name).(*ast.Ident)
		n.Type = Rewrite(v, n.Type).(*ast.FuncType)
		if n.Body != nil {
			n.Body = Rewrite(v, n.Body).(*ast.BlockStmt)
		}

	// Files and packages
	case *ast.File:
		if n.Doc != nil {
			n.Doc = Rewrite(v, n.Doc).(*ast.CommentGroup)
		}
		n.Name = Rewrite(v, n.Name).(*ast.Ident)
		rewriteDeclList(v, n.Decls)
		// don't rewrite n.Comments - they have been
		// visited already through the individual
		// nodes

	case *ast.Package:
		for i, f := range n.Files {
			n.Files[i] = Rewrite(v, f).(*ast.File)
		}

	default:
		panic(fmt.Sprintf("rewrite: unexpected node type %T", n))
	}

	return node
}
