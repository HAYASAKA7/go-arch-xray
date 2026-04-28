package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cyanl/go-arch-xray/analyzer"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	workspace = analyzer.NewWorkspace()
	stderr    = log.New(os.Stderr, "[go-arch-xray] ", log.LstdFlags)
)

type InterfaceTopologyInput struct {
	InterfaceName   string   `json:"interface_name" jsonschema:"Name of the interface to find implementors for; accepts short name or fully qualified pkgpath.Name"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern (e.g. ./... or ./internal/...); also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together; merged with package_pattern. Defaults to ./..."`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	IncludeStdlib   bool     `json:"include_stdlib,omitempty" jsonschema:"Include standard library implementations"`
}

type PackageDependenciesInput struct {
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together; merged with package_pattern. Defaults to ./..."`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	IncludeStdlib   bool     `json:"include_stdlib,omitempty" jsonschema:"Include standard library imports"`
}

type ReloadWorkspaceInput struct {
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to reload together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

type ReloadWorkspaceResult struct {
	RootPath        string   `json:"root_path"`
	PackagePatterns []string `json:"package_patterns"`
	PackagesLoaded  int      `json:"packages_loaded"`
	FunctionsLoaded int      `json:"functions_loaded"`
	CacheSize       int      `json:"cache_size"`
	CacheCapacity   int      `json:"cache_capacity"`
}

type CallHierarchyInput struct {
	FunctionName    string   `json:"function_name" jsonschema:"Function name to analyze; may be short name or package-qualified"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	MaxDepth        int      `json:"max_depth,omitempty" jsonschema:"Maximum call depth, capped at 3"`
}

type CallersInput struct {
	FunctionName    string   `json:"function_name" jsonschema:"Function name to analyze callers for; may be short name or package-qualified"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	MaxDepth        int      `json:"max_depth,omitempty" jsonschema:"Maximum caller depth, capped at 8"`
}

type StructLifecycleInput struct {
	StructName      string   `json:"struct_name" jsonschema:"Struct type name to trace"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	DedupeMode      string   `json:"dedupe_mode,omitempty" jsonschema:"Lifecycle dedupe mode: none, function_field, or function_kind_field"`
	MaxHops         int      `json:"max_hops,omitempty" jsonschema:"Maximum lifecycle hops to return, capped at 20000"`
	Summary         bool     `json:"summary,omitempty" jsonschema:"Include aggregated summary counts"`
}

type ConcurrencyRisksInput struct {
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

type FindCallPathInput struct {
	FromFunction    string   `json:"from_function" jsonschema:"Starting function for path search; may be short name or package-qualified"`
	ToFunction      string   `json:"to_function" jsonschema:"Target function for path search; may be short name or package-qualified"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	MaxDepth        int      `json:"max_depth,omitempty" jsonschema:"Maximum path depth, default 8, max 12"`
	MaxPaths        int      `json:"max_paths,omitempty" jsonschema:"Maximum number of paths to return, default 20, max 100"`
}

type DetectImportCyclesInput struct {
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern; also accepts comma-separated patterns"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to scan together"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

type FindReverseDependenciesInput struct {
	TargetPackage     string   `json:"target_package" jsonschema:"Package path to find dependents for (e.g. github.com/org/repo/internal/core)"`
	PackagePattern    string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern to restrict search scope"`
	PackagePatterns   []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns to restrict search scope"`
	RootPath          string   `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	IncludeTransitive bool     `json:"include_transitive,omitempty" jsonschema:"Also return transitive dependents (packages that depend on dependents)"`
}

type CacheStatusInput struct{}

type CacheStatusResult struct {
	CacheSize     int                    `json:"cache_size"`
	CacheCapacity int                    `json:"cache_capacity"`
	Entries       []analyzer.CacheRecord `json:"entries"`
}

type ClearCacheInput struct {
	All             bool     `json:"all,omitempty" jsonschema:"Clear all cache entries"`
	RootPath        string   `json:"root_path,omitempty" jsonschema:"Root directory for targeted clear"`
	PackagePattern  string   `json:"package_pattern,omitempty" jsonschema:"Single Go package pattern for targeted clear"`
	PackagePatterns []string `json:"package_patterns,omitempty" jsonschema:"List of Go package patterns for targeted clear"`
}

type ClearCacheResult struct {
	Cleared       int  `json:"cleared"`
	ClearedAll    bool `json:"cleared_all"`
	CacheSize     int  `json:"cache_size"`
	CacheCapacity int  `json:"cache_capacity"`
}

func main() {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "go-arch-xray",
			Version: "0.3.0",
		},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_interface_topology",
		Description: "Find all structs that implement a given Go interface, including via embedding. Returns struct names, package paths, and source locations. Accepts package_patterns array or comma-separated package_pattern for multi-pattern scans.",
	}, handleInterfaceTopology)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_package_dependencies",
		Description: "Return direct package import dependencies for one or more Go package patterns. Useful for architecture boundary and layering inspection.",
	}, handlePackageDependencies)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reload_workspace",
		Description: "Invalidate and reload the cached Go package/SSA analysis for a root path and pattern set. Returns cache occupancy info.",
	}, handleReloadWorkspace)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze_call_hierarchy",
		Description: "Build a CHA static call hierarchy from a target function, capped at 3 hops, with static/interface/goroutine edge labels. CHA graph is cached per loaded program for reuse across requests.",
	}, handleAnalyzeCallHierarchy)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_callers",
		Description: "Find incoming callers for a target function over cached CHA call graph, with depth control and edge labels.",
	}, handleFindCallers)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "trace_struct_lifecycle",
		Description: "Trace struct instantiation, field mutation, and interface handoff points across SSA. Scans only functions in the requested (root) packages.",
	}, handleTraceStructLifecycle)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "detect_concurrency_risks",
		Description: "Detect heuristic goroutine field mutation risks without visible mutex or atomic protection. Scans only functions in the requested (root) packages.",
	}, handleDetectConcurrencyRisks)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_call_path",
		Description: "Find call paths (reachability) from one function to another via BFS over the CHA call graph. Returns up to max_paths distinct paths.",
	}, handleFindCallPath)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "detect_import_cycles",
		Description: "Detect import cycles in the loaded package graph using Tarjan SCC. Returns all cyclic strongly-connected components.",
	}, handleDetectImportCycles)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "find_reverse_dependencies",
		Description: "Find which packages directly (or transitively) import a given target package within the loaded program.",
	}, handleFindReverseDependencies)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "cache_status",
		Description: "Return workspace cache occupancy and LRU entry metadata.",
	}, handleCacheStatus)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "clear_cache",
		Description: "Clear cached analysis entries by root/pattern key or clear all entries.",
	}, handleClearCache)

	stderr.Println("starting go-arch-xray MCP server")

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		stderr.Fatalf("server error: %v", err)
	}
}

func handleInterfaceTopology(ctx context.Context, req *mcp.CallToolRequest, input InterfaceTopologyInput) (*mcp.CallToolResult, *analyzer.TopologyResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.GetInterfaceTopology(workspace, rootPath, pattern, input.InterfaceName, input.IncludeStdlib)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handlePackageDependencies(ctx context.Context, req *mcp.CallToolRequest, input PackageDependenciesInput) (*mcp.CallToolResult, *analyzer.DependencyResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.GetPackageDependencies(workspace, rootPath, pattern, input.IncludeStdlib)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleReloadWorkspace(ctx context.Context, req *mcp.CallToolRequest, input ReloadWorkspaceInput) (*mcp.CallToolResult, *ReloadWorkspaceResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	prog, err := workspace.Reload(rootPath, pattern)
	if err != nil {
		return toolError(err), nil, nil
	}

	size, capacity := workspace.Stats()
	return nil, &ReloadWorkspaceResult{
		RootPath:        rootPath,
		PackagePatterns: prog.Patterns,
		PackagesLoaded:  len(prog.Packages),
		FunctionsLoaded: len(prog.SSAFuncs),
		CacheSize:       size,
		CacheCapacity:   capacity,
	}, nil
}

func handleAnalyzeCallHierarchy(ctx context.Context, req *mcp.CallToolRequest, input CallHierarchyInput) (*mcp.CallToolResult, *analyzer.CallHierarchyResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.AnalyzeCallHierarchy(workspace, rootPath, pattern, input.FunctionName, input.MaxDepth)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleFindCallers(ctx context.Context, req *mcp.CallToolRequest, input CallersInput) (*mcp.CallToolResult, *analyzer.CallersResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.FindCallers(workspace, rootPath, pattern, input.FunctionName, input.MaxDepth)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleTraceStructLifecycle(ctx context.Context, req *mcp.CallToolRequest, input StructLifecycleInput) (*mcp.CallToolResult, *analyzer.StructLifecycleResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.TraceStructLifecycle(workspace, rootPath, pattern, input.StructName, analyzer.LifecycleOptions{
		DedupeMode: input.DedupeMode,
		MaxHops:    input.MaxHops,
		Summary:    input.Summary,
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleDetectConcurrencyRisks(ctx context.Context, req *mcp.CallToolRequest, input ConcurrencyRisksInput) (*mcp.CallToolResult, *analyzer.ConcurrencyRiskResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.DetectConcurrencyRisks(workspace, rootPath, pattern)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleFindCallPath(ctx context.Context, req *mcp.CallToolRequest, input FindCallPathInput) (*mcp.CallToolResult, *analyzer.FindCallPathResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.FindCallPath(workspace, rootPath, pattern, input.FromFunction, input.ToFunction, input.MaxDepth, input.MaxPaths)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleDetectImportCycles(ctx context.Context, req *mcp.CallToolRequest, input DetectImportCyclesInput) (*mcp.CallToolResult, *analyzer.ImportCyclesResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.DetectImportCycles(workspace, rootPath, pattern)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleFindReverseDependencies(ctx context.Context, req *mcp.CallToolRequest, input FindReverseDependenciesInput) (*mcp.CallToolResult, *analyzer.ReverseDependenciesResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)

	result, err := analyzer.FindReverseDependencies(workspace, rootPath, pattern, input.TargetPackage, input.IncludeTransitive)
	if err != nil {
		return toolError(err), nil, nil
	}
	return nil, result, nil
}

func handleCacheStatus(ctx context.Context, req *mcp.CallToolRequest, input CacheStatusInput) (*mcp.CallToolResult, *CacheStatusResult, error) {
	size, capacity, entries := workspace.Status()
	return nil, &CacheStatusResult{
		CacheSize:     size,
		CacheCapacity: capacity,
		Entries:       entries,
	}, nil
}

func handleClearCache(ctx context.Context, req *mcp.CallToolRequest, input ClearCacheInput) (*mcp.CallToolResult, *ClearCacheResult, error) {
	cleared := 0
	if input.All {
		cleared = workspace.ClearAll()
		size, capacity := workspace.Stats()
		return nil, &ClearCacheResult{Cleared: cleared, ClearedAll: true, CacheSize: size, CacheCapacity: capacity}, nil
	}

	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := mergePatterns(input.PackagePattern, input.PackagePatterns)
	if workspace.Clear(rootPath, pattern) {
		cleared = 1
	}
	size, capacity := workspace.Stats()
	return nil, &ClearCacheResult{Cleared: cleared, ClearedAll: false, CacheSize: size, CacheCapacity: capacity}, nil
}

func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// mergePatterns combines an optional list of patterns with the legacy
// comma-separated pattern string. Returned value is suitable for
// analyzer.SplitPatterns.
func mergePatterns(single string, multi []string) string {
	parts := make([]string, 0, len(multi)+1)
	for _, p := range multi {
		if s := strings.TrimSpace(p); s != "" {
			parts = append(parts, s)
		}
	}
	if s := strings.TrimSpace(single); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, ",")
}

func resolveRootPath(rootPath string) (string, error) {
	if rootPath != "" {
		return rootPath, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	return wd, nil
}
