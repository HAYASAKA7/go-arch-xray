package analyzer

import (
	"testing"
)

func TestExtractCodeSymbolsFromSyntax_ExtractsTopLevelSymbols(t *testing.T) {
	dir := createTestModule(t, "symbols", `package main

type Runner struct {
	Name string
}

type Starter interface {
	Start() error
}

func Run() {}

func (r Runner) Start() error {
	return nil
}
`)

	ws := newTestWorkspace(t)
	prog, err := ws.GetOrLoadSyntaxOnly(dir, "./...")
	if err != nil {
		t.Fatalf("GetOrLoadSyntaxOnly failed: %v", err)
	}

	byID := make(map[string]CodeSymbol, len(prog.codeSymbols))
	for _, symbol := range prog.codeSymbols {
		byID[symbol.ID] = symbol
	}

	for _, id := range []string{
		"symbols.Run",
		"symbols.Runner",
		"symbols.Starter",
		"symbols.Runner.Start",
	} {
		if _, ok := byID[id]; !ok {
			t.Fatalf("expected symbol %s in %#v", id, prog.codeSymbols)
		}
	}
	if byID["symbols.Run"].Source == "" || byID["symbols.Run"].LineStart == 0 || byID["symbols.Run"].LineEnd == 0 {
		t.Fatalf("expected source and line span for Run, got %#v", byID["symbols.Run"])
	}
	if len(prog.symbolHashes) != len(prog.codeSymbols) {
		t.Fatalf("expected matching hashes and symbols, got %d hashes for %d symbols", len(prog.symbolHashes), len(prog.codeSymbols))
	}
	for _, hash := range prog.symbolHashes {
		if hash.SourceHash == "" || hash.SymbolID == "" || hash.FilePath == "" {
			t.Fatalf("expected complete symbol hash, got %#v", hash)
		}
	}
}

func TestWorkspaceLoadWritesShadowSymbols(t *testing.T) {
	ws := newTestWorkspace(t)
	dir := createTestModule(t, "shadowsymbols", `package main

type Server struct{}

func NewServer() Server {
	return Server{}
}
`)

	if _, err := ws.GetOrLoadSyntaxOnly(dir, "./..."); err != nil {
		t.Fatalf("GetOrLoadSyntaxOnly failed: %v", err)
	}
	store, err := OpenWorkspaceStore(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	symbols, err := store.GetSymbolsByPackage("shadowsymbols")
	if err != nil {
		t.Fatalf("GetSymbolsByPackage failed: %v", err)
	}
	if len(symbols) == 0 {
		t.Fatal("expected shadow code symbols to be stored")
	}
	var found CodeSymbol
	for _, symbol := range symbols {
		if symbol.ID == "shadowsymbols.NewServer" {
			found = symbol
			break
		}
	}
	if found.ID == "" {
		t.Fatalf("expected NewServer symbol in %#v", symbols)
	}
	stale, err := store.GetStaleSymbolHashes([]SymbolHash{{
		SymbolID:         found.ID,
		SourceHash:       SymbolSourceHash(found.Source),
		EmbeddingVersion: 0,
	}})
	if err != nil {
		t.Fatalf("GetStaleSymbolHashes failed: %v", err)
	}
	if len(stale) != 0 {
		t.Fatalf("expected NewServer symbol hash version 0 to be current, got stale %#v", stale)
	}
}
