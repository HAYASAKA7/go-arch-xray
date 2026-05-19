package analyzer

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
)

func TestOpenWorkspaceStoreInitializesSQLiteUnderGAX(t *testing.T) {
	root := t.TempDir()

	store, err := OpenWorkspaceStore(root)
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})

	if store.Layout.DBPath != filepath.Join(store.Layout.GAXPath, "cache.db") {
		t.Fatalf("unexpected db path: %q", store.Layout.DBPath)
	}

	if _, err := sql.Open("sqlite", store.Layout.DBPath); err != nil {
		t.Fatalf("sanity sqlite open failed: %v", err)
	}

	var journalMode string
	if err := store.DB.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("expected WAL journal mode, got %q", journalMode)
	}

	var exists int
	if err := store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='system_state'`).Scan(&exists); err != nil {
		t.Fatalf("query schema: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected system_state table to exist, got %d", exists)
	}
}

func TestWorkspaceStoreInitializesPlannedTables(t *testing.T) {
	store := openTestWorkspaceStore(t)

	tables := []string{
		"file_meta",
		"symbol_hashes",
		"arch_edges",
		"complexity_metrics",
		"http_routes",
		"grpc_endpoints",
		"code_symbols",
		"system_state",
	}
	for _, table := range tables {
		var exists int
		if err := store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&exists); err != nil {
			t.Fatalf("query schema table %s: %v", table, err)
		}
		if exists != 1 {
			t.Fatalf("expected %s table to exist, got %d", table, exists)
		}
	}
}

func TestWorkspaceStoreFileMetaCRUD(t *testing.T) {
	store := openTestWorkspaceStore(t)
	modified := time.Date(2026, 5, 19, 9, 30, 0, 0, time.UTC)

	meta := FileMeta{
		Path:       filepath.Join(store.Layout.Root, "pkg", "a.go"),
		Hash:       "sha-a",
		Module:     "example.com/app",
		Package:    "example.com/app/pkg",
		Size:       123,
		ModifiedAt: modified,
	}
	if err := store.StoreFileMeta(meta); err != nil {
		t.Fatalf("StoreFileMeta failed: %v", err)
	}

	got, err := store.GetFileByHash("sha-a")
	if err != nil {
		t.Fatalf("GetFileByHash failed: %v", err)
	}
	if got.Path != meta.Path || got.Package != meta.Package || got.Size != meta.Size {
		t.Fatalf("unexpected file meta: %#v", got)
	}

	files, err := store.GetFilesByPrefix(filepath.Join(store.Layout.Root, "pkg"))
	if err != nil {
		t.Fatalf("GetFilesByPrefix failed: %v", err)
	}
	if len(files) != 1 || files[0].Path != meta.Path {
		t.Fatalf("unexpected prefix files: %#v", files)
	}

	if err := store.DeleteFilesByPath([]string{meta.Path}); err != nil {
		t.Fatalf("DeleteFilesByPath failed: %v", err)
	}
	if _, err := store.GetFileByHash("sha-a"); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestWorkspaceStoreUpsertsShadowDatasets(t *testing.T) {
	store := openTestWorkspaceStore(t)

	edges := []ArchEdge{
		{SourceType: "package", SourcePath: "example.com/app/a", TargetType: "package", TargetPath: "example.com/app/b", EdgeType: "imports"},
		{SourceType: "package", SourcePath: "example.com/app/a", TargetType: "package", TargetPath: "fmt", EdgeType: "imports"},
	}
	if err := store.UpsertEdges(edges); err != nil {
		t.Fatalf("UpsertEdges failed: %v", err)
	}
	gotEdges, err := store.GetEdges("example.com/app/a", "imports")
	if err != nil {
		t.Fatalf("GetEdges failed: %v", err)
	}
	if len(gotEdges) != 2 {
		t.Fatalf("expected 2 edges, got %#v", gotEdges)
	}

	metrics := []FunctionComplexity{{
		Function:             "example.com/app.Run",
		Package:              "example.com/app",
		Name:                 "Run",
		File:                 filepath.Join(store.Layout.Root, "run.go"),
		Line:                 7,
		Cyclomatic:           4,
		Cognitive:            6,
		BodyLines:            20,
		MaxNesting:           2,
		HalsteadVolume:       12.5,
		HalsteadDifficulty:   3,
		HalsteadEffort:       37.5,
		MaintainabilityIndex: 80,
	}}
	if err := store.UpsertMetrics(metrics); err != nil {
		t.Fatalf("UpsertMetrics failed: %v", err)
	}
	gotMetrics, err := store.GetMetrics(ComplexityOptions{MinCognitive: 5})
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(gotMetrics) != 1 || gotMetrics[0].Function != metrics[0].Function {
		t.Fatalf("unexpected metrics: %#v", gotMetrics)
	}

	if err := store.UpsertRoutes([]HTTPRoute{{Method: "GET", Path: "/health", Handler: "health", Framework: "chi", File: "routes.go", Line: 12}}); err != nil {
		t.Fatalf("UpsertRoutes failed: %v", err)
	}
	if err := store.UpsertGRPCEndpoints([]GRPCEndpoint{{Service: "api.Health", Method: "Check", FullMethod: "/api.Health/Check", RPCType: GRPCRPCUnary, Package: "example.com/app/api", File: "health_grpc.pb.go", Line: 33}}); err != nil {
		t.Fatalf("UpsertGRPCEndpoints failed: %v", err)
	}
	if err := store.UpsertSymbols([]CodeSymbol{{ID: "sym-1", Type: "function", PackagePath: "example.com/app", Name: "Run", FilePath: "run.go", LineStart: 1, LineEnd: 3, Source: "func Run() {}"}}); err != nil {
		t.Fatalf("UpsertSymbols failed: %v", err)
	}
	results, err := store.SemanticSearch(normalizeSearchQuery("Run"), 1)
	if err != nil {
		t.Fatalf("SemanticSearch failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "sym-1" {
		t.Fatalf("unexpected semantic search results: %#v", results)
	}
}

func TestWorkspaceStoreReadsShadowFastPathDatasets(t *testing.T) {
	store := openTestWorkspaceStore(t)

	if err := store.UpsertEdges([]ArchEdge{
		{SourceType: "package", SourcePath: "example.com/app/api", TargetType: "package", TargetPath: "example.com/app/internal", EdgeType: "imports"},
	}); err != nil {
		t.Fatalf("UpsertEdges failed: %v", err)
	}
	if err := store.StoreFileMeta(FileMeta{
		Path:    filepath.Join(store.Layout.Root, "api", "routes.go"),
		Package: "example.com/app/api",
		Module:  "example.com/app",
		Hash:    "h1",
	}); err != nil {
		t.Fatalf("StoreFileMeta failed: %v", err)
	}
	if err := store.UpsertRoutes([]HTTPRoute{{Method: "GET", Path: "/health", Handler: "health", Framework: "chi", File: "routes.go", Line: 12}}); err != nil {
		t.Fatalf("UpsertRoutes failed: %v", err)
	}
	if err := store.UpsertGRPCEndpoints([]GRPCEndpoint{{Service: "api.Health", Method: "Check", FullMethod: "/api.Health/Check", RPCType: GRPCRPCUnary, Package: "example.com/app/api", File: "health_grpc.pb.go", Line: 33, Registered: true}}); err != nil {
		t.Fatalf("UpsertGRPCEndpoints failed: %v", err)
	}
	if err := store.UpsertGRPCRegistrations([]GRPCRegistration{{Service: "api.Health", RegisterFunc: "RegisterHealthServer", Registrar: "*grpc.Server", Implementation: "HealthServer", Package: "example.com/app/api", File: "health_grpc.pb.go", Line: 44}}); err != nil {
		t.Fatalf("UpsertGRPCRegistrations failed: %v", err)
	}

	deps, err := store.GetPackageDependencies(false)
	if err != nil {
		t.Fatalf("GetPackageDependencies failed: %v", err)
	}
	if len(deps) != 1 || deps[0].Package != "example.com/app/api" || len(deps[0].Imports) != 1 || deps[0].Imports[0] != "example.com/app/internal" {
		t.Fatalf("unexpected package dependencies: %#v", deps)
	}

	routes, err := store.GetHTTPRoutes()
	if err != nil {
		t.Fatalf("GetHTTPRoutes failed: %v", err)
	}
	if len(routes) != 1 || routes[0].Path != "/health" || routes[0].Handler != "health" {
		t.Fatalf("unexpected routes: %#v", routes)
	}

	endpoints, registrations, err := store.GetGRPCEndpoints()
	if err != nil {
		t.Fatalf("GetGRPCEndpoints failed: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].FullMethod != "/api.Health/Check" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
	if len(registrations) != 1 || registrations[0].RegisterFunc != "RegisterHealthServer" {
		t.Fatalf("unexpected registrations: %#v", registrations)
	}
}

func TestWorkspaceStoreSemanticSearchUsesSQLiteVecIndex(t *testing.T) {
	store := openTestWorkspaceStore(t)
	symbols := []CodeSymbol{
		{
			ID:          "needle",
			Type:        "function",
			PackagePath: "example.com/app",
			Name:        "Needle",
			FilePath:    "needle.go",
			LineStart:   1,
			LineEnd:     1,
			Source:      "func Needle() {}",
			Embedding:   encodeEmbedding([]float64{1, 0, 0}),
		},
		{
			ID:          "haystack",
			Type:        "function",
			PackagePath: "example.com/app",
			Name:        "Haystack",
			FilePath:    "haystack.go",
			LineStart:   1,
			LineEnd:     1,
			Source:      "func Haystack() {}",
			Embedding:   encodeEmbedding([]float64{0, 1, 0}),
		},
	}
	if err := store.UpsertSymbols(symbols); err != nil {
		t.Fatalf("UpsertSymbols failed: %v", err)
	}

	var exists int
	if err := store.DB.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='code_symbol_vec'`).Scan(&exists); err != nil {
		t.Fatalf("query vector table: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected sqlite-vec table to exist, got %d", exists)
	}
	var indexed int
	if err := store.DB.QueryRow(`SELECT count(*) FROM code_symbol_vec`).Scan(&indexed); err != nil {
		t.Fatalf("query vector rows: %v", err)
	}
	if indexed != 2 {
		t.Fatalf("expected 2 sqlite-vec rows, got %d", indexed)
	}

	if _, err := store.DB.Exec(`UPDATE code_symbols SET embedding = NULL`); err != nil {
		t.Fatalf("corrupt code_symbols embeddings: %v", err)
	}
	results, err := store.SemanticSearch([]float64{1, 0, 0}, 1)
	if err != nil {
		t.Fatalf("SemanticSearch failed: %v", err)
	}
	if len(results) != 1 || results[0].ID != "needle" {
		t.Fatalf("expected sqlite-vec result to prefer needle, got %#v", results)
	}
}

func TestWorkspaceStoreDetectsStaleSymbolHashes(t *testing.T) {
	store := openTestWorkspaceStore(t)

	current := []SymbolHash{
		{SymbolID: "same", FilePath: "a.go", SymbolType: "function", SymbolName: "Same", SourceHash: "h1", EmbeddingVersion: 1},
		{SymbolID: "changed", FilePath: "b.go", SymbolType: "function", SymbolName: "Changed", SourceHash: "old", EmbeddingVersion: 1},
	}
	if err := store.UpsertSymbolHashes(current); err != nil {
		t.Fatalf("UpsertSymbolHashes failed: %v", err)
	}

	next := []SymbolHash{
		{SymbolID: "same", SourceHash: "h1", EmbeddingVersion: 1},
		{SymbolID: "changed", SourceHash: "new", EmbeddingVersion: 1},
		{SymbolID: "missing", SourceHash: "h3", EmbeddingVersion: 1},
	}
	stale, err := store.GetStaleSymbolHashes(next)
	if err != nil {
		t.Fatalf("GetStaleSymbolHashes failed: %v", err)
	}
	if len(stale) != 2 || stale[0].SymbolID != "changed" || stale[1].SymbolID != "missing" {
		t.Fatalf("unexpected stale hashes: %#v", stale)
	}
}

type countingEmbeddingProvider struct {
	calls int
	texts []string
}

func (p *countingEmbeddingProvider) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	p.calls++
	p.texts = append(p.texts, texts...)
	out := make([][]float64, len(texts))
	for i, text := range texts {
		if strings.Contains(text, "changed") {
			out[i] = []float64{1, 0}
		} else {
			out[i] = []float64{0, 1}
		}
	}
	return out, nil
}

func (p *countingEmbeddingProvider) Dimension() int { return 2 }

func (p *countingEmbeddingProvider) Name() string { return "counting" }

func TestWorkspaceStoreUpsertSymbolsEmbedsOnlyChangedSymbols(t *testing.T) {
	store := openTestWorkspaceStore(t)
	now := time.Now().UTC()
	initial := []CodeSymbol{
		{ID: "same", Type: "function", PackagePath: "example.com/app", Name: "Same", FilePath: "same.go", LineStart: 1, LineEnd: 1, Source: "func Same() {}"},
		{ID: "changed", Type: "function", PackagePath: "example.com/app", Name: "Changed", FilePath: "changed.go", LineStart: 1, LineEnd: 1, Source: "func Changed() {}"},
	}
	initialHashes := []SymbolHash{
		symbolHashForCodeSymbol(initial[0], now),
		symbolHashForCodeSymbol(initial[1], now),
	}
	provider := &countingEmbeddingProvider{}
	if err := store.UpsertChangedSymbols(context.Background(), initial, initialHashes, provider); err != nil {
		t.Fatalf("initial UpsertChangedSymbols failed: %v", err)
	}
	if provider.calls != 1 || len(provider.texts) != 2 {
		t.Fatalf("expected initial embed of both symbols, calls=%d texts=%v", provider.calls, provider.texts)
	}

	next := []CodeSymbol{
		initial[0],
		{ID: "changed", Type: "function", PackagePath: "example.com/app", Name: "Changed", FilePath: "changed.go", LineStart: 1, LineEnd: 1, Source: "func Changed() string { return \"changed\" }"},
	}
	nextHashes := []SymbolHash{
		symbolHashForCodeSymbol(next[0], now),
		symbolHashForCodeSymbol(next[1], now),
	}
	provider.calls = 0
	provider.texts = nil
	if err := store.UpsertChangedSymbols(context.Background(), next, nextHashes, provider); err != nil {
		t.Fatalf("second UpsertChangedSymbols failed: %v", err)
	}
	if provider.calls != 1 || len(provider.texts) != 1 || !strings.Contains(provider.texts[0], "changed") {
		t.Fatalf("expected only changed symbol to be embedded, calls=%d texts=%v", provider.calls, provider.texts)
	}
	symbols, err := store.GetSymbolsByPackage("example.com/app")
	if err != nil {
		t.Fatalf("GetSymbolsByPackage failed: %v", err)
	}
	if len(symbols) != 2 {
		t.Fatalf("expected both symbols to remain stored, got %#v", symbols)
	}
}

func TestShadowStoreProgramEmbedsOnlyChangedSymbols(t *testing.T) {
	dir := createTestModule(t, "shadowincremental", `package main

func Same() string { return "same" }

func Changed() string { return "v1" }
`)
	prog, err := loadProgram(dir, []string{"./..."}, LoadModeSyntax)
	if err != nil {
		t.Fatalf("loadProgram failed: %v", err)
	}

	provider := &countingEmbeddingProvider{}
	originalProviderFactory := newEmbeddingProviderFromConfig
	newEmbeddingProviderFromConfig = func(config ConfigEmbeddings) (EmbeddingProvider, error) {
		return provider, nil
	}
	t.Cleanup(func() {
		newEmbeddingProviderFromConfig = originalProviderFactory
	})

	if err := ShadowStoreProgram(dir, prog); err != nil {
		t.Fatalf("initial ShadowStoreProgram failed: %v", err)
	}
	if provider.calls != 1 || len(provider.texts) != 2 {
		t.Fatalf("expected first shadow write to embed both symbols, calls=%d texts=%v", provider.calls, provider.texts)
	}

	changed := &LoadedProgram{
		Packages:           append([]*packages.Package(nil), prog.Packages...),
		SSAFuncs:           append([]*ssa.Function(nil), prog.SSAFuncs...),
		RootPaths:          prog.RootPaths,
		Patterns:           append([]string(nil), prog.Patterns...),
		Mode:               prog.Mode,
		httpRoutes:         append([]HTTPRoute(nil), prog.httpRoutes...),
		grpcEndpoints:      append([]GRPCEndpoint(nil), prog.grpcEndpoints...),
		grpcRegistrations:  append([]GRPCRegistration(nil), prog.grpcRegistrations...),
		methodFingerprints: append([]MethodFingerprint(nil), prog.methodFingerprints...),
		complexityMetrics:  append([]FunctionComplexity(nil), prog.complexityMetrics...),
		fileMetas:          append([]FileMeta(nil), prog.fileMetas...),
		codeSymbols:        append([]CodeSymbol(nil), prog.codeSymbols...),
		symbolHashes:       append([]SymbolHash(nil), prog.symbolHashes...),
		entrypoints:        append([]Entrypoint(nil), prog.entrypoints...),
		ormModels:          append([]OrmModel(nil), prog.ormModels...),
	}
	for i := range changed.codeSymbols {
		if changed.codeSymbols[i].ID == "shadowincremental.Changed" {
			changed.codeSymbols[i].Source = `func Changed() string { return "changed" }`
			changed.symbolHashes[i] = symbolHashForCodeSymbol(changed.codeSymbols[i], time.Now().UTC())
		}
	}
	provider.calls = 0
	provider.texts = nil
	if err := ShadowStoreProgram(dir, changed); err != nil {
		t.Fatalf("second ShadowStoreProgram failed: %v", err)
	}
	if provider.calls != 1 || len(provider.texts) != 1 || !strings.Contains(provider.texts[0], "changed") {
		t.Fatalf("expected second shadow write to embed only changed symbol, calls=%d texts=%v", provider.calls, provider.texts)
	}
}

func TestWorkspaceLoadWritesShadowSnapshot(t *testing.T) {
	ws := newTestWorkspace(t)
	dir := createDependencyTestModule(t, "shadowdeps", map[string]string{
		"app/app.go": `package app

import "shadowdeps/domain"

func Run(flag bool) string {
	if flag {
		return domain.Name()
	}
	return ""
}
`,
		"domain/d.go": `package domain

func Name() string { return "domain" }
`,
	})

	result, err := GetPackageDependencies(ws, dir, "./...", false)
	if err != nil {
		t.Fatalf("GetPackageDependencies failed: %v", err)
	}
	if !hasDependency(result, "shadowdeps/app", "shadowdeps/domain") {
		t.Fatalf("expected in-memory dependency result, got %#v", result.Packages)
	}

	store, err := OpenWorkspaceStore(dir)
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	edges, err := store.GetEdges("shadowdeps/app", "imports")
	if err != nil {
		t.Fatalf("GetEdges failed: %v", err)
	}
	if len(edges) != 1 || edges[0].TargetPath != "shadowdeps/domain" {
		t.Fatalf("unexpected shadow edges: %#v", edges)
	}
	metrics, err := store.GetMetrics(ComplexityOptions{})
	if err != nil {
		t.Fatalf("GetMetrics failed: %v", err)
	}
	if len(metrics) == 0 {
		t.Fatal("expected shadow complexity metrics")
	}
}

func openTestWorkspaceStore(t *testing.T) *WorkspaceStore {
	t.Helper()
	store, err := OpenWorkspaceStore(t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspaceStore failed: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}
