package analyzer

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

// HTTPRoute describes a discovered HTTP route registration.
type HTTPRoute struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Handler   string `json:"handler"`
	Framework string `json:"framework"`
	File      string `json:"file,omitempty"`
	Line      int    `json:"line,omitempty"`
	Anchor    string `json:"context_anchor,omitempty"`
}

// HTTPRoutesResult is returned by ListHTTPRoutes.
type HTTPRoutesResult struct {
	Routes              []HTTPRoute `json:"routes"`
	Total               int         `json:"total"`
	Offset              int         `json:"offset,omitempty"`
	Limit               int         `json:"limit,omitempty"`
	MaxItems            int         `json:"max_items,omitempty"`
	ChunkSize           int         `json:"chunk_size,omitempty"`
	NextCursor          string      `json:"next_cursor,omitempty"`
	HasMore             bool        `json:"has_more,omitempty"`
	TotalBeforeTruncate int         `json:"total_before_truncate"`
	Truncated           bool        `json:"truncated"`
}

// routeMethod maps known router method names to HTTP method strings.
// "ANY" means the registration accepts all methods (e.g. http.HandleFunc).
var routeMethod = map[string]string{
	// net/http and gorilla/mux
	"HandleFunc": "ANY",
	"Handle":     "ANY",
	// gin-gonic and fasthttp/router: uppercase method names
	"GET":     "GET",
	"POST":    "POST",
	"PUT":     "PUT",
	"PATCH":   "PATCH",
	"DELETE":  "DELETE",
	"HEAD":    "HEAD",
	"OPTIONS": "OPTIONS",
	"CONNECT": "CONNECT",
	"TRACE":   "TRACE",
	"ANY":     "ANY", // fasthttp/router
	// chi / echo / fiber: title-case method names
	"Get":     "GET",
	"Post":    "POST",
	"Put":     "PUT",
	"Patch":   "PATCH",
	"Delete":  "DELETE",
	"Head":    "HEAD",
	"Options": "OPTIONS",
	"Connect": "CONNECT", // echfiber, fasthttp/router, and gorilla/mux router
	// APIs.Trace":   "TRACE",   // echo / fiber
	"Any": "ANY", // echo / gin
	"All": "ANY", // fiber
}

// ListHTTPRoutes scans loaded packages for HTTP route registrations from
// net/http, gin, chi, echo, gorilla/mux, and fibre-style router APIs.
// Route paths must be string literals; dynamic paths are skipped.
func ListHTTPRoutes(ws *Workspace, dir, pattern string) (*HTTPRoutesResult, error) {
	return ListHTTPRoutesWithOptions(ws, dir, pattern, QueryOptions{})
}

func ListHTTPRoutesWithOptions(ws *Workspace, dir, pattern string, opts QueryOptions) (*HTTPRoutesResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	result := &HTTPRoutesResult{
		Routes: []HTTPRoute{},
	}

	// Use routes cached during load (extracted before pkg.Syntax was cleared).
	routes := prog.httpRoutes
	if routes == nil {
		routes = []HTTPRoute{}
	}

	sort.Slice(routes, func(i, j int) bool {
		a, b := routes[i], routes[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.File < b.File
	})

	result.Total = len(routes)
	result.TotalBeforeTruncate = result.Total

	// Apply MaxItems first as a global safety cap on the addressable dataset
	// so streaming and pagination operate over the same bounded slice.
	if opts.MaxItems > 0 && len(routes) > opts.MaxItems {
		routes = routes[:opts.MaxItems]
	}

	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
		firstKey, lastKey := httpRouteBoundaryKeys(routes)
		chunk, _, nextCursor, hasMore, serr := applyStreamWindow(routes, "http_routes:"+dir+"|"+pattern, firstKey, lastKey, StreamOptions{Cursor: opts.Cursor, ChunkSize: opts.ChunkSize})
		if serr != nil {
			return nil, serr
		}
		result.Routes = chunk
		result.NextCursor = nextCursor
		result.HasMore = hasMore
		result.Truncated = hasMore || (opts.Cursor != "")
		return result, nil
	}

	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems
	result.Routes, _, result.Truncated = applyQueryWindow(routes, opts)

	return result, nil
}

func httpRouteBoundaryKeys(routes []HTTPRoute) (firstKey, lastKey string) {
	if len(routes) == 0 {
		return "", ""
	}
	return httpRouteKey(routes[0]), httpRouteKey(routes[len(routes)-1])
}

func httpRouteKey(r HTTPRoute) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s:%d", r.Method, r.Path, r.Framework, r.Handler, r.File, r.Line)
}

// extractRoutesFromSyntax walks pkg.Syntax for every package to collect HTTP
// route registrations. Called once during loadProgram before pkg.Syntax is
// cleared, so no source re-parsing is needed on subsequent ListHTTPRoutes calls.
func extractRoutesFromSyntax(pkgs []*packages.Package) []HTTPRoute {
	var routes []HTTPRoute
	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 {
			continue
		}
		info := pkg.TypesInfo
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if route := extractHTTPRoute(call, pkg.Fset, info); route != nil {
					routes = append(routes, *route)
				}
				return true
			})
		}
	}
	return routes
}

// extractHTTPRoute attempts to parse a *ast.CallExpr as a route registration.
// Returns nil when the expression does not match a known router pattern.
func extractHTTPRoute(call *ast.CallExpr, fset *token.FileSet, info *types.Info) *HTTPRoute {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}

	method, known := routeMethod[sel.Sel.Name]
	if !known {
		return nil
	}

	// Route registrations require at least 2 arguments: path, handler.
	if len(call.Args) < 2 {
		return nil
	}

	// Path argument must be a string literal.
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil
	}
	routePath := strings.Trim(lit.Value, `"`)

	handler := handlerExprName(call.Args[1])
	framework := inferFramework(sel, info)

	pos := fset.Position(call.Pos())
	return &HTTPRoute{
		Method:    method,
		Path:      routePath,
		Handler:   handler,
		Framework: framework,
		File:      pos.Filename,
		Line:      pos.Line,
		Anchor:    contextAnchor(pos.Filename, pos.Line, handler),
	}
}

// handlerExprName returns a human-readable name for an AST expression used as
// a handler argument (identifier, selector, or inline function literal).
func handlerExprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if recv, ok := e.X.(*ast.Ident); ok {
			return recv.Name + "." + e.Sel.Name
		}
		return e.Sel.Name
	case *ast.FuncLit:
		return "<inline>"
	default:
		return "<unknown>"
	}
}

// inferFramework guesses which HTTP framework is being used. When type
// information is available the receiver type's package path is the most
// reliable signal; otherwise it falls back to method-name and receiver-name
// heuristics so test fixtures (which mock router types in package main)
// still resolve to a sensible framework.
func inferFramework(sel *ast.SelectorExpr, info *types.Info) string {
	if fw := frameworkFromTypes(sel, info); fw != "" {
		return fw
	}
	return inferFrameworkHeuristic(sel)
}

// frameworkFromTypes resolves the receiver expression's type and matches it
// against well-known framework package paths. Returns "" when the receiver
// type cannot be resolved or is not a recognized framework type.
func frameworkFromTypes(sel *ast.SelectorExpr, info *types.Info) string {
	if info == nil {
		return ""
	}
	tv, ok := info.Types[sel.X]
	if !ok || tv.Type == nil {
		return ""
	}
	t := tv.Type
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	obj := named.Obj()
	if obj == nil {
		return ""
	}
	pkgPath := ""
	if obj.Pkg() != nil {
		pkgPath = obj.Pkg().Path()
	}
	switch {
	case pkgPath == "net/http",
		strings.HasPrefix(pkgPath, "net/http/"):
		return "net/http"
	case strings.HasPrefix(pkgPath, "github.com/labstack/echo"):
		return "echo"
	case strings.HasPrefix(pkgPath, "github.com/gofiber/fiber"):
		return "fiber"
	case strings.HasPrefix(pkgPath, "github.com/valyala/fasthttp"),
		strings.HasPrefix(pkgPath, "github.com/fasthttp/router"):
		return "fasthttp"
	case strings.HasPrefix(pkgPath, "github.com/go-chi/chi"):
		return "chi"
	case strings.HasPrefix(pkgPath, "github.com/gin-gonic/gin"):
		return "gin"
	case strings.HasPrefix(pkgPath, "github.com/gorilla/mux"):
		return "gorilla/mux"
	}
	return ""
}

// inferFrameworkHeuristic guesses a framework based on method spelling and
// receiver identifier when no type information is available.
func inferFrameworkHeuristic(sel *ast.SelectorExpr) string {
	method := sel.Sel.Name
	switch method {
	case "HandleFunc", "Handle":
		// Distinguish net/http from gorilla/mux by looking at the receiver name.
		if ident, ok := sel.X.(*ast.Ident); ok {
			name := strings.ToLower(ident.Name)
			if name == "http" {
				return "net/http"
			}
			if strings.Contains(name, "mux") || strings.Contains(name, "router") {
				return "gorilla/mux"
			}
		}
		return "net/http"
	case "All":
		return "fiber"
	case "ANY":
		return "fasthttp"
	case "Any":
		return "echo"
	case "Connect", "Trace":
		return "echo"
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CONNECT", "TRACE":
		return "gin"
	case "Get", "Post", "Put", "Patch", "Delete", "Head", "Options":
		return "chi"
	default:
		return "unknown"
	}
}
