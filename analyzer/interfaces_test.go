package analyzer

import (
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGetInterfaceTopology_DirectImplementor(t *testing.T) {
	dir := createTopologyTestModule(t, "direct", map[string]string{
		"iface.go": "package main\n\ntype Greeter interface {\n\tGreet() string\n}\n",
		"impl.go":  "package main\n\ntype EnglishGreeter struct{}\n\nfunc (e EnglishGreeter) Greet() string { return \"hello\" }\n\ntype SpanishGreeter struct{}\n\nfunc (s *SpanishGreeter) Greet() string { return \"hola\" }\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "./...", "Greeter", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Interface != "Greeter" {
		t.Errorf("expected interface Greeter, got %s", result.Interface)
	}
	if len(result.Implementors) != 2 {
		t.Fatalf("expected 2 implementors, got %d", len(result.Implementors))
	}
	names := implNames(result)
	if !names["EnglishGreeter"] {
		t.Error("missing EnglishGreeter")
	}
	if !names["SpanishGreeter"] {
		t.Error("missing SpanishGreeter")
	}
}

func TestGetInterfaceTopology_EmbeddingAware(t *testing.T) {
	dir := createTopologyTestModule(t, "embedtest", map[string]string{
		"iface.go":   "package main\n\ntype Writer interface {\n\tWrite([]byte) (int, error)\n}\n",
		"base.go":    "package main\n\ntype BaseWriter struct{}\n\nfunc (b BaseWriter) Write(p []byte) (int, error) { return len(p), nil }\n",
		"derived.go": "package main\n\ntype BufferedWriter struct {\n\tBaseWriter\n\tbuf []byte\n}\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "./...", "Writer", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := implNames(result)
	if !names["BaseWriter"] {
		t.Error("missing BaseWriter (direct implementor)")
	}
	if !names["BufferedWriter"] {
		t.Error("missing BufferedWriter (via embedding)")
	}
}

func TestGetInterfaceTopology_PointerReceiverEmbedding(t *testing.T) {
	dir := createTopologyTestModule(t, "ptrembed", map[string]string{
		"iface.go":   "package main\n\ntype Closer interface {\n\tClose() error\n}\n",
		"base.go":    "package main\n\ntype BaseCloser struct{}\n\nfunc (b *BaseCloser) Close() error { return nil }\n",
		"derived.go": "package main\n\ntype FileCloser struct {\n\t*BaseCloser\n}\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "./...", "Closer", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := implNames(result)
	if !names["FileCloser"] {
		t.Error("missing FileCloser (via pointer embedding)")
	}
}

func TestGetInterfaceTopology_InterfaceNotFound(t *testing.T) {
	dir := createTopologyTestModule(t, "notfound", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	ws := NewWorkspace()
	_, err := GetInterfaceTopology(ws, dir, "./...", "NonExistent", false)
	if err == nil {
		t.Error("expected error for non-existent interface")
	}
}

func TestGetInterfaceTopology_RequiresInterfaceName(t *testing.T) {
	dir := createTopologyTestModule(t, "emptyiface", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	ws := NewWorkspace()
	_, err := GetInterfaceTopology(ws, dir, "./...", "", false)
	if err == nil {
		t.Fatal("expected error for empty interface name")
	}
}

func TestGetInterfaceTopology_DefaultsEmptyPatternToAllPackages(t *testing.T) {
	dir := createTopologyTestModule(t, "defaultpattern", map[string]string{
		"api/iface.go":  "package api\n\ntype Runner interface {\n\tRun() error\n}\n",
		"impl/impl.go":  "package impl\n\ntype Job struct{}\n\nfunc (Job) Run() error { return nil }\n",
		"impl/extra.go": "package impl\n\ntype Other struct{}\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "", "Runner", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := implNames(result)
	if !names["Job"] {
		t.Fatal("missing Job when package pattern is empty")
	}
}

func TestGetInterfaceTopology_FindsFullyQualifiedInterfaceName(t *testing.T) {
	dir := createTopologyTestModule(t, "qualified", map[string]string{
		"api/iface.go": "package api\n\ntype Reader interface {\n\tRead() string\n}\n",
		"impl.go":      "package main\n\ntype FileReader struct{}\n\nfunc (FileReader) Read() string { return \"ok\" }\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "./...", "qualified/api.Reader", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := implNames(result)
	if !names["FileReader"] {
		t.Fatal("missing FileReader for fully qualified interface name")
	}
}

func TestGetInterfaceTopology_FindsFullyQualifiedInterfaceNameWithDotsInPackagePath(t *testing.T) {
	dir := createTopologyTestModuleWithModulePath(t, "example.com/qualifieddot", map[string]string{
		"api/iface.go": "package api\n\ntype Reader interface {\n\tRead() string\n}\n",
		"impl.go":      "package main\n\ntype FileReader struct{}\n\nfunc (FileReader) Read() string { return \"ok\" }\n",
	})

	ws := NewWorkspace()
	result, err := GetInterfaceTopology(ws, dir, "./...", "example.com/qualifieddot/api.Reader", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	names := implNames(result)
	if !names["FileReader"] {
		t.Fatal("missing FileReader for fully qualified interface name with dotted package path")
	}
}

func TestGetInterfaceTopology_ReturnsDeterministicSortedImplementors(t *testing.T) {
	dir := createTopologyTestModule(t, "sorted", map[string]string{
		"iface.go": "package main\n\ntype Worker interface {\n\tWork()\n}\n",
		"z.go":     "package main\n\ntype Zed struct{}\n\nfunc (Zed) Work() {}\n",
		"a.go":     "package main\n\ntype Alpha struct{}\n\nfunc (Alpha) Work() {}\n",
	})

	ws := NewWorkspace()
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
}

func TestGetInterfaceTopologyWithOptions_AppliesLimitOffsetAndSummary(t *testing.T) {
	dir := createTopologyTestModule(t, "ifaceopts", map[string]string{
		"iface.go": "package main\n\ntype Worker interface {\n\tWork()\n}\n",
		"a.go":     "package main\n\ntype A struct{}\nfunc (A) Work() {}\n",
		"b.go":     "package main\n\ntype B struct{}\nfunc (B) Work() {}\n",
		"c.go":     "package main\n\ntype C struct{}\nfunc (C) Work() {}\n",
	})

	ws := NewWorkspace()

	// Get offset 1, limit 1 with summary
	result, err := GetInterfaceTopologyWithOptions(ws, dir, "./...", "Worker", false, QueryOptions{
		Limit:   1,
		Offset:  1,
		Summary: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalBeforeTruncate != 3 {
		t.Fatalf("expected 3 total implementors, got %d", result.TotalBeforeTruncate)
	}
	if result.Summary == nil || result.Summary.TotalImplementors != 3 {
		t.Fatalf("expected summary with 3 total implementors, got %#v", result.Summary)
	}
	if len(result.Implementors) != 1 {
		t.Fatalf("expected 1 implementor due to limit, got %d", len(result.Implementors))
	}
	if result.Implementors[0].Struct != "B" {
		t.Fatalf("expected implementor B at offset 1, got %s", result.Implementors[0].Struct)
	}
	if !result.Truncated {
		t.Fatal("expected truncated to be true")
	}
}

func TestGetInterfaceTopology_IncludesFileAndLine(t *testing.T) {
	dir := createTopologyTestModule(t, "location", map[string]string{
		"iface.go": "package main\n\ntype Pinger interface {\n\tPing() error\n}\n",
		"impl.go":  "package main\n\ntype TCPPinger struct{}\n\nfunc (t TCPPinger) Ping() error { return nil }\n",
	})

	ws := NewWorkspace()
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
