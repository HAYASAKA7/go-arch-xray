package analyzer

import (
	"strings"
	"testing"
)

func TestGetPackageDependencies_ExportOptions(t *testing.T) {
	dir := createDependencyTestModule(t, "depexport", map[string]string{
		"app/app.go":  "package app\n\nimport \"depexport/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
	})

	ws := newTestWorkspace(t)

	t.Run("mermaid", func(t *testing.T) {
		result, err := GetPackageDependenciesWithOptions(ws, dir, "./...", false, QueryOptions{
			Export: ExportMermaid,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Diagram == "" {
			t.Fatal("expected non-empty diagram for ExportMermaid")
		}
		if !strings.Contains(result.Diagram, "graph LR") {
			t.Errorf("expected mermaid LR header in diagram:\n%s", result.Diagram)
		}
		if !strings.Contains(result.Diagram, `"depexport/app"`) {
			t.Errorf("expected app node label in diagram:\n%s", result.Diagram)
		}
		if !strings.Contains(result.Diagram, `"depexport/domain"`) {
			t.Errorf("expected domain node label in diagram:\n%s", result.Diagram)
		}
	})

	t.Run("none", func(t *testing.T) {
		result, err := GetPackageDependencies(ws, dir, "./...", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.Diagram != "" {
			t.Errorf("expected empty diagram by default, got %q", result.Diagram)
		}
	})

	t.Run("dot", func(t *testing.T) {
		result, err := GetPackageDependenciesWithOptions(ws, dir, "./...", false, QueryOptions{Export: ExportDOT})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(result.Diagram, "digraph ") {
			t.Errorf("expected DOT diagram, got:\n%s", result.Diagram)
		}
		if !strings.Contains(result.Diagram, "rankdir=LR;") {
			t.Errorf("expected DOT rankdir, got:\n%s", result.Diagram)
		}
	})
}
