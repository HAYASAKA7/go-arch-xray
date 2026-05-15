package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTraceStructLifecycle_BatchedScenarios(t *testing.T) {
	dir := createLifecycleTestModule(t, "lifebatched", map[string]string{
		"main.go": `package main

type InstantiateUser struct { Name string }
func NewInstantiateUser() *InstantiateUser {
	return &InstantiateUser{Name: "a"}
}

type MutateUser struct { Name string }
func (u *MutateUser) Rename(name string) { u.Name = name }

type HandoffUser struct{}
func Save(v any) {}
func RunHandoff() { Save(&HandoffUser{}) }

type DedupeUser struct{ Name string }
func RunDedupe(u *DedupeUser, name string) {
	u.Name = name
	u.Name = name + "x"
}

type TruncateUser struct{ Name string }
func RunTruncate(u *TruncateUser) {
	u.Name = "a"
	u.Name = "b"
	u.Name = "c"
}

type PageUser struct{ Name string }
func RunPage(u *PageUser) {
	u.Name = "a"
	u.Name = "b"
	u.Name = "c"
	u.Name = "d"
}

type ORMUser struct {
	Name string ` + "`gorm:\"primaryKey\"`" + `
}
func RunORM(u *ORMUser) { u.Name = "a" }
`,
	})

	ws := newTestWorkspace(t)

	t.Run("records instantiation", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "InstantiateUser", LifecycleOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasLifecycleHop(result, "Instantiate", "", "NewInstantiateUser") {
			t.Fatalf("missing Instantiate hop: %#v", result)
		}
	})

	t.Run("records field mutation", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "MutateUser", LifecycleOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasLifecycleHop(result, "FieldMutation", "Name", "Rename") {
			t.Fatalf("missing Name FieldMutation hop: %#v", result)
		}
	})

	t.Run("records interface handoff", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "HandoffUser", LifecycleOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasLifecycleHop(result, "InterfaceHandoff", "", "RunHandoff") {
			t.Fatalf("missing InterfaceHandoff hop: %#v", result)
		}
	})

	t.Run("applies dedupe and summary", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "DedupeUser", LifecycleOptions{
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
	})

	t.Run("applies max hops truncation", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "TruncateUser", LifecycleOptions{
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
	})

	t.Run("applies limit offset and max items", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "PageUser", LifecycleOptions{
			DedupeMode: "none",
			MaxHops:    100,
			Offset:     1,
			Limit:      2,
			MaxItems:   2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.TotalBeforeTruncate <= len(result.Hops) {
			t.Fatalf("expected query window to reduce hop count, total=%d window=%d", result.TotalBeforeTruncate, len(result.Hops))
		}
		if len(result.Hops) != 2 {
			t.Fatalf("expected exactly 2 hops after offset+limit, got %d", len(result.Hops))
		}
		if !result.Truncated {
			t.Fatal("expected truncated=true when offset/limit applied")
		}
	})

	t.Run("flags orm model", func(t *testing.T) {
		result, err := TraceStructLifecycle(ws, dir, "./...", "ORMUser", LifecycleOptions{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		foundNote := false
		for _, note := range result.Notes {
			if strings.Contains(note, "detected database model") {
				foundNote = true
				break
			}
		}
		if !foundNote {
			t.Fatalf("expected note about detected database model, got notes: %v", result.Notes)
		}
	})
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
