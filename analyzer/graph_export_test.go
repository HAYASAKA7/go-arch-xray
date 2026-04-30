package analyzer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseExportFormat(t *testing.T) {
	cases := []struct {
		in      string
		want    ExportFormat
		wantErr bool
	}{
		{"", ExportNone, false},
		{"mermaid", ExportMermaid, false},
		{"MERMAID", ExportMermaid, false},
		{"mmd", ExportMermaid, false},
		{"dot", ExportDOT, false},
		{"graphviz", ExportDOT, false},
		{"json-graph", ExportJSONGraph, false},
		{"json_graph", ExportJSONGraph, false},
		{"jsonGraph", ExportJSONGraph, false},
		{"graph", ExportJSONGraph, false},
		{"svg", ExportNone, true},
	}
	for _, c := range cases {
		got, err := ParseExportFormat(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("ParseExportFormat(%q): err=%v wantErr=%v", c.in, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("ParseExportFormat(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderGraph_NoneReturnsEmpty(t *testing.T) {
	g := Graph{Nodes: []GraphNode{{ID: "n0", Label: "x"}}}
	if got := RenderGraph(g, ExportNone); got != "" {
		t.Errorf("expected empty for ExportNone, got %q", got)
	}
}

func TestRenderGraph_MermaidShape(t *testing.T) {
	b := newGraphBuilder("test", "LR")
	b.addEdge("a", "b", "static", "")
	b.addEdge("b", "c", "interface", "")
	out := RenderGraph(b.build(), ExportMermaid)

	if !strings.HasPrefix(strings.TrimSpace(out), "%% test") && !strings.Contains(out, "graph LR") {
		t.Fatalf("missing graph header:\n%s", out)
	}
	if !strings.Contains(out, "graph LR") {
		t.Errorf("missing direction header:\n%s", out)
	}
	// Three distinct labels => three node lines.
	for _, lbl := range []string{`"a"`, `"b"`, `"c"`} {
		if !strings.Contains(out, lbl) {
			t.Errorf("missing label %s in output:\n%s", lbl, out)
		}
	}
	// Two edges with labels.
	if !strings.Contains(out, "|static|") || !strings.Contains(out, "|interface|") {
		t.Errorf("missing edge labels in output:\n%s", out)
	}
	if strings.Contains(out, "-.->") {
		t.Errorf("unexpected dashed arrow for solid edges:\n%s", out)
	}
}

func TestRenderGraph_MermaidViolationDashedAndClass(t *testing.T) {
	g := buildBoundaryGraph([]BoundaryViolation{
		{From: "app/api", Import: "app/repo", Rule: "forbid"},
	})
	out := RenderGraph(g, ExportMermaid)

	if !strings.Contains(out, "-.->") {
		t.Errorf("expected dashed arrow for violation:\n%s", out)
	}
	if !strings.Contains(out, "|forbid|") {
		t.Errorf("expected rule label on violation edge:\n%s", out)
	}
	if !strings.Contains(out, "classDef violation") {
		t.Errorf("expected classDef violation block:\n%s", out)
	}
	if !strings.Contains(out, "violation\n") && !strings.HasSuffix(strings.TrimSpace(out), "violation") {
		t.Errorf("expected at least one node assigned to violation class:\n%s", out)
	}
}

func TestRenderGraph_DOTShape(t *testing.T) {
	b := newGraphBuilder("deps", "LR")
	b.addEdge("app/api", "app/service", "", "")
	out := RenderGraph(b.build(), ExportDOT)

	if !strings.HasPrefix(out, "digraph deps {") {
		t.Errorf("DOT output should start with digraph header, got:\n%s", out)
	}
	if !strings.Contains(out, `label="app/api"`) || !strings.Contains(out, `label="app/service"`) {
		t.Errorf("DOT output missing node labels:\n%s", out)
	}
	if !strings.Contains(out, " -> ") {
		t.Errorf("DOT output missing edge arrow:\n%s", out)
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "}") {
		t.Errorf("DOT output should end with closing brace:\n%s", out)
	}
}

func TestRenderGraph_DOTDashedViolation(t *testing.T) {
	g := buildBoundaryGraph([]BoundaryViolation{
		{From: "app/api", Import: "app/repo", Rule: "forbid"},
	})
	out := RenderGraph(g, ExportDOT)
	if !strings.Contains(out, "style=dashed") {
		t.Errorf("expected dashed edge style:\n%s", out)
	}
	if !strings.Contains(out, "color=red") {
		t.Errorf("expected red color on violation:\n%s", out)
	}
}

func TestRenderGraph_JSONGraphShape(t *testing.T) {
	b := newGraphBuilder("x", "LR")
	b.addEdge("a", "b", "calls", "")
	out := RenderGraph(b.build(), ExportJSONGraph)

	var got Graph
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json-graph output not valid JSON: %v\n%s", err, out)
	}
	if len(got.Nodes) != 2 || len(got.Edges) != 1 {
		t.Errorf("expected 2 nodes/1 edge, got %d/%d", len(got.Nodes), len(got.Edges))
	}
	if got.Edges[0].Label != "calls" {
		t.Errorf("expected edge label 'calls', got %q", got.Edges[0].Label)
	}
}

func TestGraphBuilder_DedupeAndStableEdges(t *testing.T) {
	b := newGraphBuilder("", "")
	b.addEdge("a", "b", "", "")
	b.addEdge("a", "b", "", "") // duplicate edge intentionally; nodes must dedupe
	b.addEdge("b", "c", "", "")
	g := b.build()

	if len(g.Nodes) != 3 {
		t.Errorf("expected 3 distinct nodes, got %d", len(g.Nodes))
	}
	// Edges are sorted: a->b, a->b, b->c
	if g.Edges[0].From != "n0" || g.Edges[0].To != "n1" {
		t.Errorf("unexpected first edge after sort: %+v", g.Edges[0])
	}
}

func TestEscapeMermaidLabel(t *testing.T) {
	if got := escapeMermaidLabel("a|b\"c"); got != "a/b'c" {
		t.Errorf("escapeMermaidLabel = %q", got)
	}
}

func TestDotIdent(t *testing.T) {
	if got := dotIdent("hello world-123"); got != "helloworld123" {
		t.Errorf("dotIdent stripped chars unexpectedly: %q", got)
	}
	if got := dotIdent("9start"); got != "_9start" {
		t.Errorf("dotIdent should prepend underscore for leading digit: %q", got)
	}
}
