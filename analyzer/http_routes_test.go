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
