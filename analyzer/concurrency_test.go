package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectConcurrencyRisks_FlagsUnprotectedGoroutineFieldMutation(t *testing.T) {
	dir := createConcurrencyTestModule(t, "riskunprotected", map[string]string{
		"main.go": `package main

type State struct {
	Count int
}

func Run(s *State) {
	go func() {
		s.Count++
	}()
}
`,
	})

	ws := NewWorkspace()
	result, err := DetectConcurrencyRisks(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !hasConcurrencyRisk(result, "High", "State", "Count") {
		t.Fatalf("missing high risk for State.Count: %#v", result)
	}
}

func TestDetectConcurrencyRisks_DoesNotFlagMutexProtectedMutation(t *testing.T) {
	dir := createConcurrencyTestModule(t, "riskprotected", map[string]string{
		"main.go": `package main

import "sync"

type State struct {
	mu sync.Mutex
	Count int
}

func Run(s *State) {
	go func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.Count++
	}()
}
`,
	})

	ws := NewWorkspace()
	result, err := DetectConcurrencyRisks(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if hasConcurrencyRisk(result, "High", "State", "Count") {
		t.Fatalf("did not expect high risk for mutex-protected State.Count: %#v", result)
	}
}

func hasConcurrencyRisk(r *ConcurrencyRiskResult, level, structName, field string) bool {
	for _, risk := range r.Risks {
		if risk.RiskLevel == level && risk.Struct == structName && risk.Field == field {
			return true
		}
	}
	return false
}

func createConcurrencyTestModule(t *testing.T, name string, files map[string]string) string {
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
