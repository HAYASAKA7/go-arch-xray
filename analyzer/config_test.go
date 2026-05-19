package analyzer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	os.Setenv(userConfigEnv, "off")
	os.Setenv("GOTOOLCHAIN", "local")
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

func TestEffectiveWorkspaceConfig_ProjectLocalGAXOverridesRepoConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\npackage_patterns:\n  - ./repo/...\ncomplexity:\n  min_cognitive: 11\n")
	writeConfigTestFile(t, dir, filepath.Join(".gax", "config.yml"), "version: 1\npackage_patterns:\n  - ./gax/...\ncomplexity:\n  min_cognitive: 21\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if !equalStringSlices(config.PackagePatterns, []string{"./gax/..."}) {
		t.Fatalf("expected project-local package patterns, got %v", config.PackagePatterns)
	}
	if config.Complexity.MinCognitive != 21 {
		t.Fatalf("expected project-local complexity override, got %+v", config.Complexity)
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
	if inspection.ConfigPath != filepath.Join(dir, ".gax", "config.yml") {
		t.Fatalf("expected project-local config path, got %q", inspection.ConfigPath)
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

func TestEffectiveWorkspaceConfig_ORM(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\norm:\n  default_framework: bun\n  migration_dirs:\n    - db/migrations\n  table_inference: snake\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}

	if config.ORM.DefaultFramework != "bun" {
		t.Errorf("Expected default_framework to be bun, got %q", config.ORM.DefaultFramework)
	}
	if len(config.ORM.MigrationDirs) != 1 || config.ORM.MigrationDirs[0] != "db/migrations" {
		t.Errorf("Expected migration_dirs to be [db/migrations], got %v", config.ORM.MigrationDirs)
	}
	if config.ORM.TableInference != "snake" {
		t.Errorf("Expected table_inference to be snake, got %q", config.ORM.TableInference)
	}
}

func TestEffectiveWorkspaceConfig_SourcesFilters(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\nsources:\n  exclude:\n    - vendor/\n    - \"*_test.go\"\n  include:\n    - cmd/\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if len(config.Sources.Exclude) != 2 || config.Sources.Exclude[0] != "vendor/" {
		t.Fatalf("expected source excludes, got %+v", config.Sources)
	}
	if len(config.Sources.Include) != 1 || config.Sources.Include[0] != "cmd/" {
		t.Fatalf("expected source includes, got %+v", config.Sources)
	}
}

func TestEffectiveWorkspaceConfig_SyncSettings(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\nsync:\n  debounce: 1s\n  check_interval: 2m\n  auto_rebuild: false\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if config.Sync.Debounce.Duration() != time.Second {
		t.Fatalf("expected sync debounce 1s, got %s", config.Sync.Debounce.Duration())
	}
	if config.Sync.CheckInterval.Duration() != 2*time.Minute {
		t.Fatalf("expected sync check interval 2m, got %s", config.Sync.CheckInterval.Duration())
	}
	if config.Sync.AutoRebuild == nil || *config.Sync.AutoRebuild {
		t.Fatalf("expected auto_rebuild=false, got %#v", config.Sync.AutoRebuild)
	}
}

func TestEffectiveWorkspaceConfig_Embeddings(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, `version: 1
embeddings:
  provider: local
  local:
    endpoint: http://localhost:11434/api/embeddings
    model: bge-m3
    timeout: 31s
  api:
    base_url: https://api.openai.com/v1
    model: text-embedding-3-small
    api_key_env: OPENAI_API_KEY
    timeout: 32s
  batch_size: 25
  chunk_size: 700
  dimension: 1024
`)

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if config.Embeddings.Provider != "local" {
		t.Fatalf("expected local provider, got %+v", config.Embeddings)
	}
	if config.Embeddings.Local.Endpoint != "http://localhost:11434/api/embeddings" || config.Embeddings.Local.Model != "bge-m3" {
		t.Fatalf("expected local embedding config, got %+v", config.Embeddings.Local)
	}
	if config.Embeddings.Local.Timeout.Duration() != 31*time.Second {
		t.Fatalf("expected local timeout 31s, got %s", config.Embeddings.Local.Timeout.Duration())
	}
	if config.Embeddings.API.BaseURL != "https://api.openai.com/v1" || config.Embeddings.API.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("expected API embedding config, got %+v", config.Embeddings.API)
	}
	if config.Embeddings.API.Timeout.Duration() != 32*time.Second {
		t.Fatalf("expected API timeout 32s, got %s", config.Embeddings.API.Timeout.Duration())
	}
	if config.Embeddings.BatchSize != 25 || config.Embeddings.ChunkSize != 700 || config.Embeddings.Dimension != 1024 {
		t.Fatalf("expected embedding tuning to round trip, got %+v", config.Embeddings)
	}
}

func TestEffectiveWorkspaceConfig_ProjectLocalEmbeddingsOverrideRepoConfig(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, WorkspaceConfigFile, "version: 1\nembeddings:\n  provider: local\n  dimension: 16\n")
	writeConfigTestFile(t, dir, filepath.Join(".gax", "config.yml"), "version: 1\nembeddings:\n  provider: api\n  dimension: 1536\n")

	config, err := EffectiveWorkspaceConfig(dir)
	if err != nil {
		t.Fatalf("effective config: %v", err)
	}
	if config.Embeddings.Provider != "api" || config.Embeddings.Dimension != 1536 {
		t.Fatalf("expected project-local embedding override, got %+v", config.Embeddings)
	}
}
