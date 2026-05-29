package analyzer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceLayout_UsesProjectLocalGAXPaths(t *testing.T) {
	layout := WorkspaceLayoutFor(filepath.Join("..", "project"))

	if layout.Root != filepath.Clean(filepath.Join("..", "project")) {
		t.Fatalf("unexpected root: %q", layout.Root)
	}
	if layout.GAXPath != filepath.Join(layout.Root, ".gax") {
		t.Fatalf("unexpected gax path: %q", layout.GAXPath)
	}
	if layout.DBPath != filepath.Join(layout.GAXPath, "cache.db") {
		t.Fatalf("unexpected db path: %q", layout.DBPath)
	}
	if layout.ConfigPath != filepath.Join(layout.GAXPath, "config.yml") {
		t.Fatalf("unexpected config path: %q", layout.ConfigPath)
	}
	if layout.StatePath != filepath.Join(layout.GAXPath, "state.json") {
		t.Fatalf("unexpected state path: %q", layout.StatePath)
	}
}

func TestWorkspaceLayout_EnsureExistsCreatesGAXDirectory(t *testing.T) {
	root := t.TempDir()
	layout := WorkspaceLayoutFor(root)

	if err := layout.EnsureExists(); err != nil {
		t.Fatalf("EnsureExists failed: %v", err)
	}

	info, err := os.Stat(layout.GAXPath)
	if err != nil {
		t.Fatalf("expected .gax directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected .gax path to be a directory, got mode %v", info.Mode())
	}
}

func TestWorkspaceLayout_DBPathUsesCacheDBUnderGAX(t *testing.T) {
	layout := WorkspaceLayoutFor(t.TempDir())
	if filepath.Base(layout.DBPath) != "cache.db" {
		t.Fatalf("expected cache.db filename, got %q", layout.DBPath)
	}
	if filepath.Dir(layout.DBPath) != layout.GAXPath {
		t.Fatalf("expected db path under gax dir, got %q", layout.DBPath)
	}
}
