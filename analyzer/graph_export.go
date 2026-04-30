package analyzer

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ExportFormat selects the diagram representation produced for graph-shaped
// tool results. ExportNone disables diagram emission entirely (the default,
// preserving the historical JSON-only payload).
type ExportFormat string

const (
	ExportNone      ExportFormat = ""
	ExportMermaid   ExportFormat = "mermaid"
	ExportDOT       ExportFormat = "dot"
	ExportJSONGraph ExportFormat = "json-graph"
)

// ParseExportFormat normalizes user-provided export tokens. Recognized
// values are "mermaid", "dot", and "json-graph"/"jsongraph"/"json_graph"/"graph"
// (case-insensitive). Returns ExportNone for the empty string and an error
// for any other value, so unknown formats are rejected at the boundary.
func ParseExportFormat(s string) (ExportFormat, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return ExportNone, nil
	case "mermaid", "mmd":
		return ExportMermaid, nil
	case "dot", "graphviz":
		return ExportDOT, nil
	case "json-graph", "jsongraph", "json_graph", "graph":
		return ExportJSONGraph, nil
	default:
		return ExportNone, fmt.Errorf("unsupported export format %q (expected mermaid, dot, or json-graph)", s)
	}
}

// GraphNode is a renderable node in a generic directed graph.
// ID must be a stable, sanitized identifier (Mermaid/DOT-safe).
// Label is the human-readable name shown in the rendered diagram.
// Class is an optional logical group used by renderers to apply styling
// (e.g. "violation" highlights boundary violations in Mermaid).
type GraphNode struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
	Class string `json:"class,omitempty"`
}

// GraphEdge is a directed edge between two GraphNode IDs.
// Label is shown on the edge when non-empty.
// Style is "solid" (default) or "dashed" (rendered as dotted in Mermaid /
// dashed in DOT) and is typically used to signal exceptional edges such
// as boundary violations.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label,omitempty"`
	Style string `json:"style,omitempty"`
}

// Graph is a renderable directed graph. Direction applies to Mermaid
// (e.g. "LR", "TD") and is ignored by DOT (which uses rankdir below).
type Graph struct {
	Title     string      `json:"title,omitempty"`
	Direction string      `json:"direction,omitempty"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
}

// graphBuilder accumulates nodes and edges with stable sequential IDs and
// label-based deduplication so the same logical node is only emitted once
// even when referenced by many edges.
type graphBuilder struct {
	g       Graph
	nodeIDs map[string]string
	classes map[string]string
}

func newGraphBuilder(title, direction string) *graphBuilder {
	return &graphBuilder{
		g:       Graph{Title: title, Direction: direction},
		nodeIDs: make(map[string]string),
		classes: make(map[string]string),
	}
}

// addNode registers label as a node and returns its sanitized ID. Repeated
// calls with the same label return the same ID without duplicating the node.
// When class is non-empty and the node is new, the class is recorded; when
// the node already exists the class is preserved unless the new value is
// non-empty (in which case the latest non-empty class wins, allowing later
// metadata such as "violation" to override an earlier neutral classification).
func (b *graphBuilder) addNode(label, class string) string {
	if id, ok := b.nodeIDs[label]; ok {
		if class != "" {
			b.classes[id] = class
			for i := range b.g.Nodes {
				if b.g.Nodes[i].ID == id {
					b.g.Nodes[i].Class = class
					break
				}
			}
		}
		return id
	}
	id := fmt.Sprintf("n%d", len(b.g.Nodes))
	b.nodeIDs[label] = id
	if class != "" {
		b.classes[id] = class
	}
	b.g.Nodes = append(b.g.Nodes, GraphNode{ID: id, Label: label, Class: class})
	return id
}

func (b *graphBuilder) addEdge(fromLabel, toLabel, edgeLabel, style string) {
	from := b.addNode(fromLabel, "")
	to := b.addNode(toLabel, "")
	b.g.Edges = append(b.g.Edges, GraphEdge{From: from, To: to, Label: edgeLabel, Style: style})
}

func (b *graphBuilder) build() Graph {
	// Stable sort edges so output is deterministic regardless of insertion order.
	sort.SliceStable(b.g.Edges, func(i, j int) bool {
		if b.g.Edges[i].From != b.g.Edges[j].From {
			return b.g.Edges[i].From < b.g.Edges[j].From
		}
		if b.g.Edges[i].To != b.g.Edges[j].To {
			return b.g.Edges[i].To < b.g.Edges[j].To
		}
		return b.g.Edges[i].Label < b.g.Edges[j].Label
	})
	return b.g
}

// RenderGraph serializes the graph in the requested format. For ExportNone
// it returns "" so callers can unconditionally assign the result to a
// JSON-omitempty field.
func RenderGraph(g Graph, format ExportFormat) string {
	switch format {
	case ExportMermaid:
		return renderMermaid(g)
	case ExportDOT:
		return renderDOT(g)
	case ExportJSONGraph:
		return renderJSONGraph(g)
	default:
		return ""
	}
}

func renderMermaid(g Graph) string {
	dir := g.Direction
	if dir == "" {
		dir = "LR"
	}
	var b strings.Builder
	if g.Title != "" {
		// Mermaid does not support a graph-level title across all renderers,
		// so emit it as a leading comment for readability.
		b.WriteString("%% ")
		b.WriteString(g.Title)
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "graph %s\n", dir)
	for _, n := range g.Nodes {
		fmt.Fprintf(&b, "  %s[%q]\n", n.ID, n.Label)
	}
	for _, e := range g.Edges {
		arrow := "-->"
		if e.Style == "dashed" {
			arrow = "-.->"
		}
		if e.Label != "" {
			fmt.Fprintf(&b, "  %s %s|%s| %s\n", e.From, arrow, escapeMermaidLabel(e.Label), e.To)
		} else {
			fmt.Fprintf(&b, "  %s %s %s\n", e.From, arrow, e.To)
		}
	}
	// Emit class definitions for any classed nodes so renderers that
	// understand classDef can apply consistent visual cues. Classes are
	// stable preset names; unknown classes simply have no visual effect.
	classed := map[string][]string{}
	for _, n := range g.Nodes {
		if n.Class == "" {
			continue
		}
		classed[n.Class] = append(classed[n.Class], n.ID)
	}
	if len(classed) > 0 {
		b.WriteString("  classDef violation fill:#fdd,stroke:#c00,color:#900\n")
		b.WriteString("  classDef root fill:#dfd,stroke:#080\n")
		b.WriteString("  classDef target fill:#ddf,stroke:#008\n")
		// Deterministic class order
		keys := make([]string, 0, len(classed))
		for k := range classed {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, class := range keys {
			ids := classed[class]
			sort.Strings(ids)
			fmt.Fprintf(&b, "  class %s %s\n", strings.Join(ids, ","), class)
		}
	}
	return b.String()
}

// escapeMermaidLabel escapes characters that would break a Mermaid edge
// label rendered inside |...|.
func escapeMermaidLabel(s string) string {
	r := strings.NewReplacer("|", "/", "\"", "'", "\n", " ")
	return r.Replace(s)
}

func renderDOT(g Graph) string {
	var b strings.Builder
	name := dotIdent(g.Title)
	if name == "" {
		name = "graph_export"
	}
	fmt.Fprintf(&b, "digraph %s {\n", name)
	b.WriteString("  rankdir=LR;\n")
	for _, n := range g.Nodes {
		if n.Class == "violation" {
			fmt.Fprintf(&b, "  %s [label=%q, color=red, fontcolor=red];\n", n.ID, n.Label)
		} else if n.Class != "" {
			fmt.Fprintf(&b, "  %s [label=%q, group=%q];\n", n.ID, n.Label, n.Class)
		} else {
			fmt.Fprintf(&b, "  %s [label=%q];\n", n.ID, n.Label)
		}
	}
	for _, e := range g.Edges {
		attrs := make([]string, 0, 2)
		if e.Label != "" {
			attrs = append(attrs, fmt.Sprintf("label=%q", e.Label))
		}
		if e.Style == "dashed" {
			attrs = append(attrs, "style=dashed", "color=red")
		}
		if len(attrs) == 0 {
			fmt.Fprintf(&b, "  %s -> %s;\n", e.From, e.To)
		} else {
			fmt.Fprintf(&b, "  %s -> %s [%s];\n", e.From, e.To, strings.Join(attrs, ", "))
		}
	}
	b.WriteString("}\n")
	return b.String()
}

// dotIdent reduces s to a valid DOT identifier (letters, digits, underscore).
func dotIdent(s string) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderJSONGraph(g Graph) string {
	// Marshal can only fail on unsupported types, none of which Graph contains,
	// so a marshal error here would indicate a programming bug.
	data, err := json.Marshal(g)
	if err != nil {
		return ""
	}
	return string(data)
}
