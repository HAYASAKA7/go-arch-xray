package analyzer

import (
	"sort"
	"testing"
)

// TestFindSCCs_* tests the Tarjan SCC algorithm directly using synthetic
// graphs, since the Go compiler itself forbids import cycles in real packages.

func TestFindSCCs_NoCycles(t *testing.T) {
	graph := map[string][]string{
		"a": {"b", "c"},
		"b": {"c"},
		"c": {},
	}
	sccs := findSCCs(graph)
	for _, scc := range sccs {
		if len(scc) > 1 {
			t.Fatalf("expected no cycles, got SCC: %v", scc)
		}
	}
}

func TestFindSCCs_SimpleCycle(t *testing.T) {
	// a -> b -> a
	graph := map[string][]string{
		"a": {"b"},
		"b": {"a"},
	}
	sccs := findSCCs(graph)
	cyclicSCCs := filterCyclic(sccs)
	if len(cyclicSCCs) != 1 {
		t.Fatalf("expected 1 cyclic SCC, got %d: %v", len(cyclicSCCs), cyclicSCCs)
	}
	members := cyclicSCCs[0]
	sort.Strings(members)
	if members[0] != "a" || members[1] != "b" {
		t.Fatalf("unexpected members: %v", members)
	}
}

func TestFindSCCs_ThreeNodeCycle(t *testing.T) {
	// a -> b -> c -> a
	graph := map[string][]string{
		"a": {"b"},
		"b": {"c"},
		"c": {"a"},
	}
	sccs := findSCCs(graph)
	cyclicSCCs := filterCyclic(sccs)
	if len(cyclicSCCs) != 1 {
		t.Fatalf("expected 1 cyclic SCC, got %d", len(cyclicSCCs))
	}
	if len(cyclicSCCs[0]) != 3 {
		t.Fatalf("expected 3-member SCC, got %d members: %v", len(cyclicSCCs[0]), cyclicSCCs[0])
	}
}

func TestFindSCCs_TwoDisjointCycles(t *testing.T) {
	// a <-> b and c <-> d, no edges between them
	graph := map[string][]string{
		"a": {"b"},
		"b": {"a"},
		"c": {"d"},
		"d": {"c"},
	}
	sccs := findSCCs(graph)
	cyclicSCCs := filterCyclic(sccs)
	if len(cyclicSCCs) != 2 {
		t.Fatalf("expected 2 cyclic SCCs, got %d: %v", len(cyclicSCCs), cyclicSCCs)
	}
}

func TestFindSCCs_SkipsExternalNodes(t *testing.T) {
	// a -> ext where ext is not in graph (stdlib dep case)
	graph := map[string][]string{
		"a": {"b", "stdlib/fmt"},
		"b": {"a"},
	}
	sccs := findSCCs(graph)
	cyclicSCCs := filterCyclic(sccs)
	if len(cyclicSCCs) != 1 {
		t.Fatalf("expected 1 cyclic SCC, got %d", len(cyclicSCCs))
	}
}

func TestDetectImportCycles_NoCycleInValidModule(t *testing.T) {
	dir := createDependencyTestModule(t, "cycles_valid", map[string]string{
		"go.mod":      "module cycles_valid\n\ngo 1.23\n",
		"app/app.go":  "package app\n\nimport \"cycles_valid/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
	})

	ws := NewWorkspace()
	result, err := DetectImportCycles(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.CycleCount != 0 {
		t.Fatalf("expected 0 cycles in valid module, got %d: %v", result.CycleCount, result.Cycles)
	}
}

// filterCyclic returns only SCCs with more than one member.
func filterCyclic(sccs [][]string) [][]string {
	var out [][]string
	for _, scc := range sccs {
		if len(scc) > 1 {
			out = append(out, scc)
		}
	}
	return out
}
