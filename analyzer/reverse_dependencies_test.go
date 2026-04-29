package analyzer

import (
	"testing"
)

func TestFindReverseDependencies_DirectDependents(t *testing.T) {
	dir := createDependencyTestModule(t, "revdeps_direct", map[string]string{
		"go.mod":         "module revdeps_direct\n\ngo 1.23\n",
		"core/core.go":   "package core\n\nfunc Value() int { return 1 }\n",
		"app/app.go":     "package app\n\nimport \"revdeps_direct/core\"\n\nfunc Run() int { return core.Value() }\n",
		"util/util.go":   "package util\n\nimport \"revdeps_direct/core\"\n\nfunc Wrap() int { return core.Value() + 1 }\n",
		"other/other.go": "package other\n\nfunc Other() int { return 42 }\n",
	})

	ws := NewWorkspace()
	result, err := FindReverseDependencies(ws, dir, "./...", "revdeps_direct/core", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectCount != 2 {
		t.Fatalf("expected 2 direct dependents, got %d: %v", result.DirectCount, result.DirectDependents)
	}

	depPkgs := make(map[string]bool)
	for _, d := range result.DirectDependents {
		depPkgs[d.Package] = true
	}
	if !depPkgs["revdeps_direct/app"] {
		t.Fatal("expected app to be a direct dependent")
	}
	if !depPkgs["revdeps_direct/util"] {
		t.Fatal("expected util to be a direct dependent")
	}
	if depPkgs["revdeps_direct/other"] {
		t.Fatal("did not expect other to be a dependent of core")
	}
}

func TestFindReverseDependencies_TransitiveDependents(t *testing.T) {
	dir := createDependencyTestModule(t, "revdeps_trans", map[string]string{
		"go.mod":       "module revdeps_trans\n\ngo 1.23\n",
		"core/core.go": "package core\n\nfunc Value() int { return 1 }\n",
		"mid/mid.go":   "package mid\n\nimport \"revdeps_trans/core\"\n\nfunc Mid() int { return core.Value() }\n",
		"top/top.go":   "package top\n\nimport \"revdeps_trans/mid\"\n\nfunc Top() int { return mid.Mid() }\n",
	})

	ws := NewWorkspace()
	result, err := FindReverseDependencies(ws, dir, "./...", "revdeps_trans/core", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.DirectCount != 1 {
		t.Fatalf("expected 1 direct dependent, got %d", result.DirectCount)
	}
	if result.DirectDependents[0].Package != "revdeps_trans/mid" {
		t.Fatalf("expected mid as direct dependent, got %s", result.DirectDependents[0].Package)
	}

	found := false
	for _, pkg := range result.TransitiveDependents {
		if pkg == "revdeps_trans/top" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected top in transitive dependents, got: %v", result.TransitiveDependents)
	}
}

func TestFindReverseDependencies_UnknownPackage(t *testing.T) {
	dir := createDependencyTestModule(t, "revdeps_unknown", map[string]string{
		"go.mod":       "module revdeps_unknown\n\ngo 1.23\n",
		"core/core.go": "package core\n\nfunc Value() int { return 1 }\n",
	})

	ws := NewWorkspace()
	result, err := FindReverseDependencies(ws, dir, "./...", "revdeps_unknown/nonexistent", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.DirectCount != 0 {
		t.Fatalf("expected 0 direct dependents for unknown package, got %d", result.DirectCount)
	}
}

func TestFindReverseDependenciesWithOptions_AppliesLimitOffset(t *testing.T) {
	dir := createDependencyTestModule(t, "revdeps_opts", map[string]string{
		"go.mod":       "module revdeps_opts\n\ngo 1.23\n",
		"core/core.go": "package core\n\nfunc Value() int { return 1 }\n",
		"a/a.go":       "package a\n\nimport \"revdeps_opts/core\"\n\nfunc Run() int { return core.Value() }\n",
		"b/b.go":       "package b\n\nimport \"revdeps_opts/core\"\n\nfunc Run() int { return core.Value() }\n",
		"c/c.go":       "package c\n\nimport \"revdeps_opts/core\"\n\nfunc Run() int { return core.Value() }\n",
	})

	ws := NewWorkspace()
	result, err := FindReverseDependenciesWithOptions(ws, dir, "./...", "revdeps_opts/core", false, QueryOptions{
		Limit:  1,
		Offset: 1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.TotalBeforeTruncate != 3 {
		t.Fatalf("expected 3 total direct dependents before truncate, got %d", result.TotalBeforeTruncate)
	}
	if len(result.DirectDependents) != 1 {
		t.Fatalf("expected 1 direct dependent due to limit, got %d", len(result.DirectDependents))
	}
	if !result.Truncated {
		t.Fatal("expected truncated to be true")
	}
	if result.DirectDependents[0].Package != "revdeps_opts/b" {
		t.Fatalf("expected revdeps_opts/b dependent at offset 1, got %s", result.DirectDependents[0].Package)
	}
}
