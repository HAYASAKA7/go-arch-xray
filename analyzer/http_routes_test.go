package analyzer

import (
	"testing"
)

func TestListHTTPRoutes_NetHTTPHandleFunc(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_std", map[string]string{
		"main.go": `package main

import "net/http"

func helloHandler(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/hello", helloHandler)
	http.HandleFunc("/world", helloHandler)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", result.Total, result.Routes)
	}
	for _, r := range result.Routes {
		if r.Method != "ANY" {
			t.Errorf("expected method=ANY for HandleFunc, got %s", r.Method)
		}
		if r.Framework != "net/http" {
			t.Errorf("expected framework=net/http, got %s", r.Framework)
		}
		if r.Handler != "helloHandler" {
			t.Errorf("expected handler=helloHandler, got %s", r.Handler)
		}
		if r.File == "" || r.Line == 0 {
			t.Errorf("expected file/line location, got file=%q line=%d", r.File, r.Line)
		}
	}
	paths := []string{result.Routes[0].Path, result.Routes[1].Path}
	if paths[0] != "/hello" || paths[1] != "/world" {
		t.Errorf("unexpected paths: %v", paths)
	}
}

func TestListHTTPRoutes_GinStyleRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_gin", map[string]string{
		"main.go": `package main

type Engine struct{}

func (e *Engine) GET(path string, handlers ...func()) {}
func (e *Engine) POST(path string, handlers ...func()) {}

func userHandler() {}
func createHandler() {}

func main() {
	r := &Engine{}
	r.GET("/users", userHandler)
	r.POST("/users", createHandler)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", result.Total, result.Routes)
	}

	methods := make(map[string]bool)
	for _, r := range result.Routes {
		methods[r.Method] = true
		if r.Framework != "gin" {
			t.Errorf("expected framework=gin, got %s", r.Framework)
		}
	}
	if !methods["GET"] || !methods["POST"] {
		t.Errorf("expected GET and POST methods, got %v", methods)
	}
}

func TestListHTTPRoutes_ChiStyleRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_chi", map[string]string{
		"main.go": `package main

type Router struct{}

func (rt *Router) Get(path string, handler func()) {}
func (rt *Router) Delete(path string, handler func()) {}

func getItem() {}
func deleteItem() {}

func main() {
	r := &Router{}
	r.Get("/items/{id}", getItem)
	r.Delete("/items/{id}", deleteItem)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", result.Total, result.Routes)
	}
	for _, r := range result.Routes {
		if r.Framework != "chi" {
			t.Errorf("expected framework=chi, got %s", r.Framework)
		}
	}
}

func TestListHTTPRoutes_InlineHandlerLabeled(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_inline", map[string]string{
		"main.go": `package main

import "net/http"

func main() {
	http.HandleFunc("/inline", func(w http.ResponseWriter, r *http.Request) {})
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 1 {
		t.Fatalf("expected 1 route, got %d", result.Total)
	}
	if result.Routes[0].Handler != "<inline>" {
		t.Errorf("expected handler=<inline>, got %s", result.Routes[0].Handler)
	}
}

func TestListHTTPRoutes_NoDynamicPaths(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_dynamic", map[string]string{
		"main.go": `package main

import "net/http"

func h(w http.ResponseWriter, r *http.Request) {}

func main() {
	prefix := "/api"
	http.HandleFunc(prefix+"/users", h) // dynamic path — should be skipped
	http.HandleFunc("/static", h)        // static path — should be included
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 1 {
		t.Fatalf("expected only the static route (1), got %d: %+v", result.Total, result.Routes)
	}
	if result.Routes[0].Path != "/static" {
		t.Errorf("expected path=/static, got %s", result.Routes[0].Path)
	}
}

func TestListHTTPRoutesWithOptions_AppliesLimitOffset(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_opts", map[string]string{
		"main.go": `package main

import "net/http"

func h(w http.ResponseWriter, r *http.Request) {}

func main() {
	http.HandleFunc("/a", h)
	http.HandleFunc("/b", h)
	http.HandleFunc("/c", h)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutesWithOptions(ws, dir, "./...", QueryOptions{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalBeforeTruncate != 3 {
		t.Fatalf("expected 3 total routes before truncate, got %d", result.TotalBeforeTruncate)
	}
	if len(result.Routes) != 1 {
		t.Fatalf("expected 1 route due to limit, got %d", len(result.Routes))
	}
	if !result.Truncated {
		t.Fatal("expected truncated to be true")
	}
	if result.Routes[0].Path != "/b" {
		t.Fatalf("expected route /b at offset 1, got %s", result.Routes[0].Path)
	}
}

func TestListHTTPRoutes_EchoStyleRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_echo", map[string]string{
		"main.go": `package main

type Echo struct{}

func (e *Echo) GET(path string, h func()) {}
func (e *Echo) POST(path string, h func()) {}
func (e *Echo) Any(path string, h func()) {}
func (e *Echo) CONNECT(path string, h func()) {}

func list() {}
func create() {}
func anything() {}
func tunnel() {}

func main() {
	e := &Echo{}
	e.GET("/items", list)
	e.POST("/items", create)
	e.Any("/proxy", anything)
	e.CONNECT("/tunnel", tunnel)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 4 {
		t.Fatalf("expected 4 routes, got %d: %+v", result.Total, result.Routes)
	}

	byPath := make(map[string]HTTPRoute, len(result.Routes))
	for _, r := range result.Routes {
		byPath[r.Path] = r
	}
	if r := byPath["/proxy"]; r.Method != "ANY" || r.Framework != "echo" {
		t.Errorf("expected /proxy method=ANY framework=echo, got method=%s framework=%s", r.Method, r.Framework)
	}
	if r := byPath["/tunnel"]; r.Method != "CONNECT" {
		t.Errorf("expected /tunnel method=CONNECT, got %s", r.Method)
	}
	// GET/POST on a fake Echo type are ambiguous to the heuristic (defaults to gin),
	// but type info should not be available in this fixture; ensure framework is at least set.
	if r := byPath["/items"]; r.Framework == "" {
		t.Errorf("expected non-empty framework for /items, got %+v", r)
	}
}

func TestListHTTPRoutes_FiberStyleRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_fiber", map[string]string{
		"main.go": `package main

type App struct{}

func (a *App) Get(path string, h func()) {}
func (a *App) All(path string, h func()) {}

func showItem() {}
func anyItem() {}

func main() {
	app := &App{}
	app.Get("/show", showItem)
	app.All("/any", anyItem)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", result.Total, result.Routes)
	}

	byPath := make(map[string]HTTPRoute, len(result.Routes))
	for _, r := range result.Routes {
		byPath[r.Path] = r
	}
	if r := byPath["/any"]; r.Method != "ANY" || r.Framework != "fiber" {
		t.Errorf("expected /any method=ANY framework=fiber, got method=%s framework=%s", r.Method, r.Framework)
	}
	// Get on a bare title-case method without type info defaults to chi.
	if r := byPath["/show"]; r.Method != "GET" {
		t.Errorf("expected /show method=GET, got %s", r.Method)
	}
}

func TestListHTTPRoutes_FastHTTPStyleRoutes(t *testing.T) {
	dir := createDependencyTestModule(t, "routes_fasthttp", map[string]string{
		"main.go": `package main

type Router struct{}

func (r *Router) GET(path string, h func()) {}
func (r *Router) ANY(path string, h func()) {}

func showItem() {}
func anyItem() {}

func main() {
	router := &Router{}
	router.GET("/show", showItem)
	router.ANY("/any", anyItem)
}
`,
	})

	ws := NewWorkspace()
	result, err := ListHTTPRoutes(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 {
		t.Fatalf("expected 2 routes, got %d: %+v", result.Total, result.Routes)
	}

	byPath := make(map[string]HTTPRoute, len(result.Routes))
	for _, r := range result.Routes {
		byPath[r.Path] = r
	}
	if r := byPath["/any"]; r.Method != "ANY" || r.Framework != "fasthttp" {
		t.Errorf("expected /any method=ANY framework=fasthttp, got method=%s framework=%s", r.Method, r.Framework)
	}
	if r := byPath["/show"]; r.Method != "GET" {
		t.Errorf("expected /show method=GET, got %s", r.Method)
	}
}
