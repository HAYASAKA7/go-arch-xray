package analyzer

import "testing"

func TestFindReverseDependencies_BatchedScenarios(t *testing.T) {
	dir := createDependencyTestModule(t, "revdeps", map[string]string{
		"core/core.go":   "package core\n\nfunc Value() int { return 1 }\n",
		"app/app.go":     "package app\n\nimport \"revdeps/core\"\n\nfunc Run() int { return core.Value() }\n",
		"util/util.go":   "package util\n\nimport \"revdeps/core\"\n\nfunc Wrap() int { return core.Value() + 1 }\n",
		"other/other.go": "package other\n\nfunc Other() int { return 42 }\n",
		"mid/mid.go":     "package mid\n\nimport \"revdeps/core\"\n\nfunc Mid() int { return core.Value() }\n",
		"top/top.go":     "package top\n\nimport \"revdeps/mid\"\n\nfunc Top() int { return mid.Mid() }\n",
		"a/a.go":         "package a\n\nimport \"revdeps/core\"\n\nfunc Run() int { return core.Value() }\n",
		"b/b.go":         "package b\n\nimport \"revdeps/core\"\n\nfunc Run() int { return core.Value() }\n",
		"c/c.go":         "package c\n\nimport \"revdeps/core\"\n\nfunc Run() int { return core.Value() }\n",
	})

	ws := newTestWorkspace(t)

	t.Run("direct dependents", func(t *testing.T) {
		result, err := FindReverseDependencies(ws, dir, "./...", "revdeps/core", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if result.DirectCount != 6 {
			t.Fatalf("expected 6 direct dependents, got %d: %v", result.DirectCount, result.DirectDependents)
		}
		for _, pkg := range []string{"revdeps/app", "revdeps/util", "revdeps/mid"} {
			if !hasReverseDependent(result, pkg) {
				t.Fatalf("expected %s to be a direct dependent", pkg)
			}
		}
		if hasReverseDependent(result, "revdeps/other") {
			t.Fatal("did not expect other to be a dependent of core")
		}
	})

	t.Run("transitive dependents", func(t *testing.T) {
		result, err := FindReverseDependencies(ws, dir, "./...", "revdeps/core", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !hasReverseDependent(result, "revdeps/mid") {
			t.Fatal("expected mid as direct dependent")
		}
		found := false
		for _, pkg := range result.TransitiveDependents {
			if pkg == "revdeps/top" {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected top in transitive dependents, got: %v", result.TransitiveDependents)
		}
	})

	t.Run("unknown package", func(t *testing.T) {
		result, err := FindReverseDependencies(ws, dir, "./...", "revdeps/nonexistent", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.DirectCount != 0 {
			t.Fatalf("expected 0 direct dependents for unknown package, got %d", result.DirectCount)
		}
	})

	t.Run("limit offset", func(t *testing.T) {
		result, err := FindReverseDependenciesWithOptions(ws, dir, "./...", "revdeps/core", false, QueryOptions{
			Limit:  1,
			Offset: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.TotalBeforeTruncate != 6 {
			t.Fatalf("expected 6 total direct dependents before truncate, got %d", result.TotalBeforeTruncate)
		}
		if len(result.DirectDependents) != 1 {
			t.Fatalf("expected 1 direct dependent due to limit, got %d", len(result.DirectDependents))
		}
		if !result.Truncated {
			t.Fatal("expected truncated to be true")
		}
		if result.DirectDependents[0].Package != "revdeps/app" {
			t.Fatalf("expected revdeps/app dependent at offset 1, got %s", result.DirectDependents[0].Package)
		}
	})
}

func hasReverseDependent(result *ReverseDependenciesResult, pkg string) bool {
	for _, d := range result.DirectDependents {
		if d.Package == pkg {
			return true
		}
	}
	return false
}
