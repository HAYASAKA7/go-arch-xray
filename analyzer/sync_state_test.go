package analyzer

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistedStateLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	state := &PersistedState{
		Version:  1,
		LastSync: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC),
		LastHash: "abc123",
		Files: map[string]FileHash{
			"main.go": {Hash: "hash1", Size: 42, ModifiedAt: time.Date(2026, 5, 19, 11, 0, 0, 0, time.UTC)},
		},
		Status: SyncStatus{State: StateIdle, Progress: 1},
	}
	if err := state.Save(path); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	got, err := LoadState(path)
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	if got.Version != 1 || got.LastHash != "abc123" || got.Files["main.go"].Hash != "hash1" {
		t.Fatalf("unexpected state: %#v", got)
	}
}

func TestScanHashesSkipsGeneratedAndVendorFiles(t *testing.T) {
	dir := t.TempDir()
	writeConfigTestFile(t, dir, "go.mod", "module example.com/root\n\ngo 1.23\n")
	writeConfigTestFile(t, dir, "keep.go", "package main\n")
	writeConfigTestFile(t, dir, "keep_test.go", "package main\n")
	writeConfigTestFile(t, dir, "vendor/skip.go", "package vendor\n")
	writeConfigTestFile(t, dir, "generated.pb.go", "package main\n")

	hashes, err := scanHashes(dir, NewSourceFilter(nil, []string{"*_test.go", "vendor/", "*.pb.go"}))
	if err != nil {
		t.Fatalf("scanHashes failed: %v", err)
	}
	if len(hashes) != 1 {
		t.Fatalf("expected only keep.go, got %#v", hashes)
	}
	if _, ok := hashes[filepath.ToSlash(filepath.Join(dir, "keep.go"))]; !ok {
		t.Fatalf("expected keep.go hash, got %#v", hashes)
	}
}

func TestHashCheckerDetectChanges(t *testing.T) {
	store := openTestWorkspaceStore(t)
	filePath := filepath.Join(store.Layout.Root, "main.go")
	if err := store.StoreFileMeta(FileMeta{Path: filePath, Hash: "old"}); err != nil {
		t.Fatalf("StoreFileMeta failed: %v", err)
	}
	checker := NewHashChecker(store)

	changed, deleted, err := checker.DetectChanges(map[string]string{
		filePath: "new",
	})
	if err != nil {
		t.Fatalf("DetectChanges failed: %v", err)
	}
	if len(changed) != 1 || changed[0] != filePath {
		t.Fatalf("expected changed file, got %#v", changed)
	}
	if len(deleted) != 0 {
		t.Fatalf("expected no deleted files, got %#v", deleted)
	}
}
