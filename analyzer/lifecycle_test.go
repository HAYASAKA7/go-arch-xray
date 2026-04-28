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
	result, err := TraceStructLifecycle(ws, dir, "./...", "User")
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
	result, err := TraceStructLifecycle(ws, dir, "./...", "User")
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
	result, err := TraceStructLifecycle(ws, dir, "./...", "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasLifecycleHop(result, "InterfaceHandoff", "", "Run") {
		t.Fatalf("missing InterfaceHandoff hop: %#v", result)
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
