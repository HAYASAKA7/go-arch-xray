package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTraceStructLifecycle_RecordsInstantiation(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifeinstantiate", map[string]string{
		"main.go": `package main

type User struct {
	Name string
}

func NewUser() *User {
	return &User{Name: "a"}
}
`,
	})

	ws := NewWorkspace()
	result, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasLifecycleHop(result, "Instantiate", "", "NewUser") {
		t.Fatalf("missing Instantiate hop: %#v", result)
	}
}

func TestTraceStructLifecycle_RecordsFieldMutation(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifemutate", map[string]string{
		"main.go": `package main

type User struct {
	Name string
}

func (u *User) Rename(name string) {
	u.Name = name
}
`,
	})

	ws := NewWorkspace()
	result, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasLifecycleHop(result, "FieldMutation", "Name", "Rename") {
		t.Fatalf("missing Name FieldMutation hop: %#v", result)
	}
}

func TestTraceStructLifecycle_RecordsInterfaceHandoff(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifehandoff", map[string]string{
		"main.go": `package main

type User struct{}

func Save(v any) {}

func Run() {
	Save(&User{})
}
`,
	})

	ws := NewWorkspace()
	result, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasLifecycleHop(result, "InterfaceHandoff", "", "Run") {
		t.Fatalf("missing InterfaceHandoff hop: %#v", result)
	}
}

func TestTraceStructLifecycle_AppliesDedupeAndSummary(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifededupe", map[string]string{
		"main.go": `package main

type User struct{ Name string }

func Run(u *User, name string) {
	u.Name = name
	u.Name = name + "x"
}
`,
	})

	ws := NewWorkspace()
	result, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{
		DedupeMode: "function_kind_field",
		Summary:    true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Summary == nil {
		t.Fatal("expected summary in lifecycle result")
	}
	if result.Summary.TotalByField["Name"] != 1 {
		t.Fatalf("expected deduped Name mutation count 1, got %#v", result.Summary.TotalByField)
	}
}

func TestTraceStructLifecycle_AppliesMaxHopsTruncation(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifetruncate", map[string]string{
		"main.go": `package main

type User struct{ Name string }

func Run(u *User) {
	u.Name = "a"
	u.Name = "b"
	u.Name = "c"
}
`,
	})

	ws := NewWorkspace()
	result, err := TraceStructLifecycle(ws, dir, "./...", "User", LifecycleOptions{
		DedupeMode: "none",
		MaxHops:    1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Truncated {
		t.Fatal("expected truncated lifecycle result")
	}
	if result.TotalBeforeTruncate <= 1 {
		t.Fatalf("expected total_before_truncate > 1, got %d", result.TotalBeforeTruncate)
	}
	if len(result.Hops) != 1 {
		t.Fatalf("expected 1 hop after truncation, got %d", len(result.Hops))
	}
}

func hasLifecycleHop(r *StructLifecycleResult, kind, field, function string) bool {
	for _, hop := range r.Hops {
		if hop.Kind != kind {
			continue
		}
		if field != "" && hop.Field != field {
			continue
		}
		if function != "" && shortFuncName(hop.Function) != function {
			continue
		}
		if hop.File == "" || hop.Line == 0 {
			continue
		}
		return true
	}
	return false
}

func createLifecycleTestModule(t *testing.T, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + name + "\n\ngo 1.23\n"
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
