// Package auditcheck holds structural consistency checks that keep two sources
// of truth in agreement where they share no representation. It follows the
// shape of internal/skillcheck: a small pure package plus a test that wires the
// real paths, so the invariant runs as part of `go test ./...`.
//
// The first check here guards the public media kind enum. api declares it once
// (api.KindSong and friends); client surfaces must reference those constants
// instead of re-spelling the values, otherwise a rename or a typo compiles and
// fails silently at the default branch of a switch.
package auditcheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Violation is a bare literal that repeats a public kind value where the api
// constant belongs. Path and Offset locate the literal exactly, so a caller can
// rewrite it in place; Line is for the report.
type Violation struct {
	Path     string
	Line     int
	Offset   int
	Length   int
	Literal  string
	Constant string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s:%d: %q should be api.%s", filepath.ToSlash(v.Path), v.Line, v.Literal, v.Constant)
}

// PublicKinds reads the string constants named Kind* declared in apiDir and
// returns value -> constant name. Deriving the set from the declaration keeps
// the check current when a kind is added or renamed.
func PublicKinds(apiDir string) (map[string]string, error) {
	files, err := goFiles(apiDir)
	if err != nil {
		return nil, err
	}
	kinds := map[string]string{}
	for _, path := range files {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, name := range value.Names {
					if i >= len(value.Values) || !strings.HasPrefix(name.Name, "Kind") {
						continue
					}
					lit, ok := value.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					text, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					kinds[text] = name.Name
				}
			}
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no Kind* constants found in %s", apiDir)
	}
	return kinds, nil
}

// BareKindLiterals scans every non-test Go file directly inside dir and reports
// string literals that repeat a public kind value in a kind position: a `Kind:`
// field, a `.Kind ==`/`!=` comparison, or a `case` of a switch on `.Kind` or a
// variable named `kind`. Other uses of the same words (messages, filter axes,
// client-only node kinds) are not reported.
func BareKindLiterals(dir string, kinds map[string]string) ([]Violation, error) {
	files, err := goFiles(dir)
	if err != nil {
		return nil, err
	}
	var found []Violation
	for _, path := range files {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CompositeLit:
				for _, elt := range n.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok || fieldName(kv.Key) != "Kind" {
						continue
					}
					found = append(found, checkLiteral(fset, path, kv.Value, kinds)...)
				}
			case *ast.BinaryExpr:
				if n.Op != token.EQL && n.Op != token.NEQ {
					return true
				}
				if isKindSelector(n.X) {
					found = append(found, checkLiteral(fset, path, n.Y, kinds)...)
				}
				if isKindSelector(n.Y) {
					found = append(found, checkLiteral(fset, path, n.X, kinds)...)
				}
			case *ast.SwitchStmt:
				if !isKindTag(n.Tag) {
					return true
				}
				for _, stmt := range n.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						found = append(found, checkLiteral(fset, path, expr, kinds)...)
					}
				}
			}
			return true
		})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].Path != found[j].Path {
			return found[i].Path < found[j].Path
		}
		return found[i].Offset < found[j].Offset
	})
	return found, nil
}

func checkLiteral(fset *token.FileSet, path string, expr ast.Expr, kinds map[string]string) []Violation {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	text, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil
	}
	name, ok := kinds[text]
	if !ok {
		return nil
	}
	pos := fset.Position(lit.Pos())
	return []Violation{{Path: path, Line: pos.Line, Offset: pos.Offset, Length: len(lit.Value), Literal: text, Constant: name}}
}

// fieldName returns the name of a struct-literal key, whether it is written as
// an identifier or as a qualified selector.
func fieldName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return n.Sel.Name
	}
	return ""
}

func isKindSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "Kind"
}

func isKindTag(expr ast.Expr) bool {
	switch n := expr.(type) {
	case *ast.SelectorExpr:
		return n.Sel.Name == "Kind"
	case *ast.Ident:
		return n.Name == "kind" || n.Name == "Kind"
	}
	return false
}

func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}
