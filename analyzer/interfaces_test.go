package analyzer

import (
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGetInterfaceTopology_BatchedScenarios(t *testing.T) {
	dir := createTopologyTestModule(t, "ifacebatched", map[string]string{
		"iface.go": `package main

type Greeter interface { Greet() string }
type Writer interface { Write([]byte) (int, error) }
type Closer interface { Close() error }
type Worker interface { Work() }
type Pinger interface { Ping() error }
`,
		"impl.go": `package main

type EnglishGreeter struct{}
func (EnglishGreeter) Greet() string { return "hello" }
type SpanishGreeter struct{}
func (*SpanishGreeter) Greet() string { return "hola" }

type BaseWriter struct{}
func (BaseWriter) Write(p []byte) (int, error) { return len(p), nil }
type BufferedWriter struct { BaseWriter; buf []byte }

type BaseCloser struct{}
func (*BaseCloser) Close() error { return nil }
type FileCloser struct { *BaseCloser }

type Zed struct{}
func (Zed) Work() {}
type Alpha struct{}
func (Alpha) Work() {}
type A struct{}
func (A) Work() {}
type B struct{}
func (B) Work() {}
type C struct{}
func (C) Work() {}

type TCPPinger struct{}
func (TCPPinger) Ping() error { return nil }
`,
		"api/iface.go": "package api\n\ntype Reader interface { Read() string }\ntype Runner interface { Run() error }\n",
		"fq_impl.go":   "package main\n\ntype FileReader struct{}\nfunc (FileReader) Read() string { return \"ok\" }\n",
		"impl/job.go":  "package impl\n\ntype Job struct{}\nfunc (Job) Run() error { return nil }\n",
	})

	ws := newTestWorkspace(t)

	t.Run("direct implementors", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "Greeter", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Interface != "Greeter" {
			t.Errorf("expected interface Greeter, got %s", result.Interface)
		}
		names := implNames(result)
		if !names["EnglishGreeter"] || !names["SpanishGreeter"] {
			t.Fatalf("missing greeter implementors: %+v", result.Implementors)
		}
	})

	t.Run("embedding aware", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "Writer", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		names := implNames(result)
		if !names["BaseWriter"] || !names["BufferedWriter"] {
			t.Fatalf("missing writer implementors: %+v", result.Implementors)
		}
	})

	t.Run("pointer receiver embedding", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "Closer", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !implNames(result)["FileCloser"] {
			t.Fatalf("missing FileCloser via pointer embedding: %+v", result.Implementors)
		}
	})

	t.Run("interface not found", func(t *testing.T) {
		_, err := GetInterfaceTopology(ws, dir, "./...", "NonExistent", false)
		if err == nil {
			t.Error("expected error for non-existent interface")
		}
	})

	t.Run("requires interface name", func(t *testing.T) {
		_, err := GetInterfaceTopology(ws, dir, "./...", "", false)
		if err == nil {
			t.Fatal("expected error for empty interface name")
		}
	})

	t.Run("defaults empty pattern to all packages", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "", "Runner", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !implNames(result)["Job"] {
			t.Fatalf("missing Job when package pattern is empty: %+v", result.Implementors)
		}
	})

	t.Run("fully qualified interface name", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "ifacebatched/api.Reader", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !implNames(result)["FileReader"] {
			t.Fatalf("missing FileReader for fully qualified interface name: %+v", result.Implementors)
		}
	})

	t.Run("deterministic sorted implementors", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "Worker", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		got := make([]string, 0, len(result.Implementors))
		for _, impl := range result.Implementors {
			got = append(got, impl.Struct)
		}
		want := append([]string(nil), got...)
		sort.Strings(want)
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("implementors are not sorted: got %v, want %v", got, want)
			}
		}
	})

	t.Run("options apply limit offset and summary", func(t *testing.T) {
		result, err := GetInterfaceTopologyWithOptions(ws, dir, "./...", "Worker", false, QueryOptions{
			Limit:   1,
			Offset:  1,
			Summary: true,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.TotalBeforeTruncate != 5 {
			t.Fatalf("expected 5 total implementors, got %d", result.TotalBeforeTruncate)
		}
		if result.Summary == nil || result.Summary.TotalImplementors != 5 {
			t.Fatalf("expected summary with 5 total implementors, got %#v", result.Summary)
		}
		if len(result.Implementors) != 1 {
			t.Fatalf("expected 1 implementor due to limit, got %d", len(result.Implementors))
		}
		if result.Implementors[0].Struct != "Alpha" {
			t.Fatalf("expected implementor Alpha at offset 1, got %s", result.Implementors[0].Struct)
		}
		if !result.Truncated {
			t.Fatal("expected truncated to be true")
		}
	})

	t.Run("includes file line and anchor", func(t *testing.T) {
		result, err := GetInterfaceTopology(ws, dir, "./...", "Pinger", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Implementors) != 1 {
			t.Fatalf("expected 1 implementor, got %d", len(result.Implementors))
		}
		impl := result.Implementors[0]
		if impl.File == "" {
			t.Error("expected non-empty file path")
		}
		if impl.Line == 0 {
			t.Error("expected non-zero line number")
		}
		if impl.Anchor == "" {
			t.Fatal("expected non-empty context anchor")
		}
	})
}

func TestGetInterfaceTopology_FindsFullyQualifiedInterfaceNameWithDotsInPackagePath(t *testing.T) {
	dir := createTopologyTestModuleWithModulePath(t, "example.com/qualifieddot", map[string]string{
		"api/iface.go": "package api\n\ntype Reader interface {\n\tRead() string\n}\n",
		"impl.go":      "package main\n\ntype FileReader struct{}\n\nfunc (FileReader) Read() string { return \"ok\" }\n",
	})

	ws := newTestWorkspace(t)
	result, err := GetInterfaceTopology(ws, dir, "./...", "example.com/qualifieddot/api.Reader", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := implNames(result)
	if !names["FileReader"] {
		t.Fatal("missing FileReader for fully qualified interface name with dotted package path")
	}
}

func TestGetInterfaceTopology_FallbackResolvesDependencyInterfaceWithNarrowPattern(t *testing.T) {
	dir := createTopologyTestModule(t, "ifacefallback", map[string]string{
		"api/iface.go": "package api\n\ntype Worker interface {\n\tWork()\n}\n",
		"impl/impl.go": "package impl\n\nimport \"ifacefallback/api\"\n\ntype Job struct{}\n\nfunc (Job) Work() {}\n\nvar _ api.Worker = Job{}\n",
		"main/main.go": "package main\n\nfunc main() {}\n",
	})

	ws := newTestWorkspace(t)
	// Narrow pattern intentionally excludes api package where interface is declared.
	result, err := GetInterfaceTopology(ws, dir, "./impl", "ifacefallback/api.Worker", false)
	if err != nil {
		t.Fatalf("expected fallback to resolve interface from dependency package, got error: %v", err)
	}
	names := implNames(result)
	if !names["Job"] {
		t.Fatal("missing Job implementor after fallback lookup")
	}
}

func TestImplementsInterface_AcceptsAssignableInterfaceValues(t *testing.T) {
	iface := types.NewInterfaceType(nil, nil).Complete()
	if !implementsInterface(types.Typ[types.Int], iface) {
		t.Fatal("expected int to be assignable to empty interface")
	}
}

func implNames(r *TopologyResult) map[string]bool {
	m := make(map[string]bool, len(r.Implementors))
	for _, impl := range r.Implementors {
		m[impl.Struct] = true
	}
	return m
}

func createTopologyTestModule(t *testing.T, name string, files map[string]string) string {
	return createTopologyTestModuleWithModulePath(t, name, files)
}

func createTopologyTestModuleWithModulePath(t *testing.T, modulePath string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), filepath.Base(modulePath))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + modulePath + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
