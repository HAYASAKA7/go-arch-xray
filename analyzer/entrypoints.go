package analyzer

import (
	"fmt"
	"go/ast"
	"sort"

	"golang.org/x/tools/go/packages"
)

// EntrypointKind classifies an entrypoint.
type EntrypointKind string

const (
	EntrypointMain      EntrypointKind = "main"
	EntrypointInit      EntrypointKind = "init"
	EntrypointGoroutine EntrypointKind = "goroutine_start"
)

// Entrypoint describes a detected program entrypoint.
type Entrypoint struct {
	Kind     EntrypointKind `json:"kind"`
	Function string         `json:"function"`
	Package  string         `json:"package"`
	File     string         `json:"file,omitempty"`
	Line     int            `json:"line,omitempty"`
	Anchor   string         `json:"context_anchor,omitempty"`
}

// EntrypointsResult is returned by ListEntrypoints.
type EntrypointsResult struct {
	Entrypoints         []Entrypoint `json:"entrypoints"`
	Total               int          `json:"total"`
	Offset              int          `json:"offset,omitempty"`
	Limit               int          `json:"limit,omitempty"`
	MaxItems            int          `json:"max_items,omitempty"`
	ChunkSize           int          `json:"chunk_size,omitempty"`
	NextCursor          string       `json:"next_cursor,omitempty"`
	HasMore             bool         `json:"has_more,omitempty"`
	TotalBeforeTruncate int          `json:"total_before_truncate"`
	Truncated           bool         `json:"truncated"`
}

// ListEntrypoints scans syntax for main functions, init functions, and
// goroutine-spawn sites (go statements), returning each as an Entrypoint with
// source location. It intentionally stays off SSA because entrypoint discovery
// only needs AST-level information.
func ListEntrypoints(ws *Workspace, dir, pattern string) (*EntrypointsResult, error) {
	return ListEntrypointsWithOptions(ws, dir, pattern, QueryOptions{})
}

func ListEntrypointsWithOptions(ws *Workspace, dir, pattern string, opts QueryOptions) (*EntrypointsResult, error) {
	prog, err := ws.GetOrLoadSyntaxOnly(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &EntrypointsResult{
		Entrypoints: append([]Entrypoint(nil), prog.entrypoints...),
	}

	sortEntrypoints(result.Entrypoints)

	result.Total = len(result.Entrypoints)
	result.TotalBeforeTruncate = result.Total

	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	var err2 error
	result.Entrypoints, _, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(result.Entrypoints, "entrypoints:"+dir+"|"+pattern, entrypointKey, opts)
	if err2 != nil {
		return nil, err2
	}
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	return result, nil
}

func extractEntrypointsFromSyntax(pkgs []*packages.Package) []Entrypoint {
	entrypoints := make([]Entrypoint, 0, 16)
	seenFunc := make(map[string]bool)
	seenGo := make(map[string]bool)
	for _, pkg := range pkgs {
		if pkg == nil || pkg.PkgPath == "" || pkg.Fset == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			collectSyntaxEntrypoints(pkg, file, seenFunc, seenGo, &entrypoints)
		}
	}
	sortEntrypoints(entrypoints)
	return entrypoints
}

func sortEntrypoints(entrypoints []Entrypoint) {
	sort.Slice(entrypoints, func(i, j int) bool {
		a, b := entrypoints[i], entrypoints[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		return a.Function < b.Function
	})
}

func entrypointKey(e Entrypoint) string {
	return string(e.Kind) + "|" + e.Package + "|" + e.Function + "|" + e.File + ":" + fmt.Sprintf("%d", e.Line)
}

func collectSyntaxEntrypoints(pkg *packages.Package, file *ast.File, seenFunc, seenGo map[string]bool, out *[]Entrypoint) {
	if file == nil {
		return
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		fnName := syntaxFunctionName(pkg, fn)
		var kind EntrypointKind
		switch {
		case fn.Name.Name == "main" && pkg.Name == "main":
			kind = EntrypointMain
		case fn.Name.Name == "init":
			kind = EntrypointInit
		default:
			kind = ""
		}
		if kind != "" {
			key := string(kind) + ":" + fnName
			if !seenFunc[key] {
				seenFunc[key] = true
				pos := pkg.Fset.Position(fn.Pos())
				*out = append(*out, Entrypoint{
					Kind:     kind,
					Function: fnName,
					Package:  pkg.PkgPath,
					File:     pos.Filename,
					Line:     pos.Line,
					Anchor:   contextAnchor(pos.Filename, pos.Line, fn.Name.Name),
				})
			}
		}
		if fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			goStmt, ok := n.(*ast.GoStmt)
			if !ok {
				return true
			}
			spawned := syntaxGoroutineTarget(pkg, goStmt, fn)
			pos := pkg.Fset.Position(goStmt.Pos())
			key := pkg.PkgPath + ":" + spawned + ":" + pos.Filename + ":" + fmt.Sprintf("%d", pos.Line)
			if seenGo[key] {
				return true
			}
			seenGo[key] = true
			*out = append(*out, Entrypoint{
				Kind:     EntrypointGoroutine,
				Function: spawned,
				Package:  pkg.PkgPath,
				File:     pos.Filename,
				Line:     pos.Line,
				Anchor:   contextAnchor(pos.Filename, pos.Line, spawned),
			})
			return true
		})
	}
}

func syntaxFunctionName(pkg *packages.Package, fn *ast.FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return ""
	}
	name := pkg.PkgPath + "."
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		name += receiverTypeExprName(fn.Recv.List[0].Type) + "."
	}
	return name + fn.Name.Name
}

func syntaxGoroutineTarget(pkg *packages.Package, goStmt *ast.GoStmt, enclosing *ast.FuncDecl) string {
	if goStmt == nil || goStmt.Call == nil {
		return "<dynamic in " + syntaxFunctionName(pkg, enclosing) + ">"
	}
	switch fun := goStmt.Call.Fun.(type) {
	case *ast.Ident:
		return pkg.PkgPath + "." + fun.Name
	case *ast.SelectorExpr:
		return selectorExprName(fun)
	case *ast.FuncLit:
		return "<closure in " + syntaxFunctionName(pkg, enclosing) + ">"
	default:
		return "<dynamic in " + syntaxFunctionName(pkg, enclosing) + ">"
	}
}

func selectorExprName(sel *ast.SelectorExpr) string {
	if sel == nil || sel.Sel == nil {
		return ""
	}
	if ident, ok := sel.X.(*ast.Ident); ok {
		return ident.Name + "." + sel.Sel.Name
	}
	return sel.Sel.Name
}

func receiverTypeExprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return receiverTypeExprName(e.X)
	case *ast.SelectorExpr:
		return selectorExprName(e)
	case *ast.IndexExpr:
		return receiverTypeExprName(e.X)
	case *ast.IndexListExpr:
		return receiverTypeExprName(e.X)
	default:
		return ""
	}
}
