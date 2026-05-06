package main

import (
	"context"
	"testing"

	"github.com/HAYASAKA7/go-arch-xray/analyzer"
)

// BenchmarkHandlePackageDependencies measures latency for the dependency analysis tool
// on a small synthetic module. Useful as a regression baseline.
func BenchmarkHandlePackageDependencies(b *testing.B) {
	dir := createMainTestModule(b, "benchdeps", map[string]string{
		"app/app.go":     "package app\n\nimport \"benchdeps/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go":    "package domain\n\nfunc Name() string { return \"domain\" }\n",
		"service/svc.go": "package service\n\nimport \"benchdeps/domain\"\n\nfunc Handle() string { return domain.Name() }\n",
	})

	// Warm-up: build the workspace once so cached runs reflect tool latency only.
	workspace = analyzer.NewWorkspace()
	if _, _, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{RootPath: dir}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{RootPath: dir})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleAnalyzeCallHierarchy measures CHA call-graph traversal latency.
func BenchmarkHandleAnalyzeCallHierarchy(b *testing.B) {
	dir := createMainTestModule(b, "benchcalls", map[string]string{
		"main.go": "package main\n\nfunc A() { B() }\nfunc B() { C() }\nfunc C() { D() }\nfunc D() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, _, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{RootPath: dir, FunctionName: "A"}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{RootPath: dir, FunctionName: "A"})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleCheckArchitectureBoundaries measures boundary-rule evaluation latency.
func BenchmarkHandleCheckArchitectureBoundaries(b *testing.B) {
	dir := createMainTestModule(b, "benchbounds", map[string]string{
		"app/app.go": "package app\n\nimport \"benchbounds/db\"\n\nfunc Run() string { return db.Query() }\n",
		"db/db.go":   "package db\n\nfunc Query() string { return \"\" }\n",
		"core/c.go":  "package core\n\nfunc Logic() {}\n",
		"infra/i.go": "package infra\n\nimport \"benchbounds/core\"\n\nfunc Impl() { core.Logic() }\n",
	})
	rules := []analyzer.BoundaryRule{
		{Type: analyzer.RuleForbid, From: "benchbounds/app", To: "benchbounds/db"},
	}

	workspace = analyzer.NewWorkspace()
	if _, _, err := handleCheckArchitectureBoundaries(context.Background(), nil, CheckArchitectureBoundariesInput{RootPath: dir, Rules: rules}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handleCheckArchitectureBoundaries(context.Background(), nil, CheckArchitectureBoundariesInput{RootPath: dir, Rules: rules})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleListEntrypoints measures entrypoint scan latency.
func BenchmarkHandleListEntrypoints(b *testing.B) {
	dir := createMainTestModule(b, "benchentry", map[string]string{
		"main.go": "package main\n\nfunc main() { go worker() }\nfunc worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, _, err := handleListEntrypoints(context.Background(), nil, ListEntrypointsInput{RootPath: dir}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handleListEntrypoints(context.Background(), nil, ListEntrypointsInput{RootPath: dir})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleListHTTPRoutes measures HTTP route scan latency.
func BenchmarkHandleListHTTPRoutes(b *testing.B) {
	dir := createMainTestModule(b, "benchroutes", map[string]string{
		"main.go": "package main\n\ntype Router struct{}\n\nfunc (r *Router) HandleFunc(path string, h func()) {}\nfunc (r *Router) Get(path string, h func()) {}\n\nfunc main() {\n\tr := &Router{}\n\tr.HandleFunc(\"/api/v1\", nil)\n\tr.Get(\"/api/users\", nil)\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, _, err := handleListHTTPRoutes(context.Background(), nil, ListHTTPRoutesInput{RootPath: dir}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handleListHTTPRoutes(context.Background(), nil, ListHTTPRoutesInput{RootPath: dir})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkHandleComputeComplexityMetrics measures cached complexity query latency.
func BenchmarkHandleComputeComplexityMetrics(b *testing.B) {
	dir := createMainTestModule(b, "benchcomplexity", map[string]string{
		"main.go": "package main\n\nfunc main() { complex(3) }\n\nfunc complex(x int) int {\n\ttotal := 0\n\tfor i := 0; i < x; i++ {\n\t\tif i%2 == 0 || i%3 == 0 {\n\t\t\ttotal += i\n\t\t}\n\t}\n\treturn total\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, _, err := handleComputeComplexityMetrics(context.Background(), nil, ComplexityMetricsInput{RootPath: dir}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := handleComputeComplexityMetrics(context.Background(), nil, ComplexityMetricsInput{RootPath: dir})
		if err != nil {
			b.Fatal(err)
		}
	}
}
