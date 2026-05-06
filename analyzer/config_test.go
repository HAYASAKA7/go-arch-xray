package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv(userConfigEnv, "off")
	os.Exit(m.Run())
}

func TestSuggestWorkspaceConfig_GoWorkModules(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.work", "go 1.23\n\nuse (\n\t./services/api\n\t./libs/shared\n)\n")
	writeConfigTestFile(t, dir, "services/api/go.mod", "module example.com/api\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, "libs/shared/go.mod", "module example.com/shared\n\ngo 1.23\n")

	config, err := SuggestWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("suggest config: %v", err)
	}
	if config.Workspace.Mode != "go_work" || config.Workspace.File != "go.work" {
		t.Fatalf("expected go.work workspace, got %+v", config.Workspace)
	}
	expectedPatterns := []string{"./services/api/...", "./libs/shared/..."}
	if !equalStringSlices(config.PackagePatterns, expectedPatterns) {
		t.Fatalf("expected package patterns %v, got %v", expectedPatterns, config.PackagePatterns)
	}
	if len(config.Modules) != 2 {
		t.Fatalf("expected two modules, got %+v", config.Modules)
	}
	if config.Modules[0].ModulePath != "example.com/api" || config.Modules[1].ModulePath != "example.com/shared" {
		t.Fatalf("expected parsed module paths, got %+v", config.Modules)
	}
}

func TestEffectiveWorkspaceConfig_RepoConfigOverridesDetectedPatterns(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\npackage_patterns:\n  - ./custom/...\nboundaries:\n  - type: forbid\n    from: example.com/root/app\n    to: example.com/root/db\ncomplexity:\n  min_cognitive: 12\n  sort_by: maintainability\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if !equalStringSlices(config.PackagePatterns, []string{"./custom/..."}) {
		t.Fatalf("expected configured package patterns, got %v", config.PackagePatterns)
	}
	if len(config.Boundaries) != 1 || config.Boundaries[0].Type != RuleForbid {
		t.Fatalf("expected configured boundary rule, got %+v", config.Boundaries)
	}
	if config.Complexity.MinCognitive != 12 || config.Complexity.SortBy != "maintainability" {
		t.Fatalf("expected configured complexity defaults, got %+v", config.Complexity)
	}
}

func TestInspectWorkspaceConfig_RecommendedNextStep(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")

	inspection, err := InspectWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("inspect config without repo config: %v", err)
	}
	if inspection.RecommendedNextStep == "" || !strings.Contains(inspection.RecommendedNextStep, "init_workspace_config") {
		t.Fatalf("expected init recommendation when config is missing, got %q", inspection.RecommendedNextStep)
	}

	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\npackage_patterns:\n  - ./...\n")
	inspection, err = InspectWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("inspect config with repo config: %v", err)
	}
	if inspection.RecommendedNextStep == "" || !strings.Contains(inspection.RecommendedNextStep, "effective_config") {
		t.Fatalf("expected effective config recommendation when config exists, got %q", inspection.RecommendedNextStep)
	}
}

func TestInitWorkspaceConfig_DoesNotOverwriteExistingConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\npackage_patterns:\n  - ./manual/...\n")

	_, err := InitWorkspaceConfig(dir, false)
	if err == nil {
		t.Fatal("expected no-overwrite error")
	}

	data, readErr := os.ReadFile(filepath.Join(dir, WorkspaceConfigFile))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "version: 1\npackage_patterns:\n  - ./manual/...\n" {
		t.Fatalf("config was overwritten: %q", string(data))
	}
}

func writeConfigTestFile(t testing.TB, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
