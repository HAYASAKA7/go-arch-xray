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
	InterfaceName  string `json:"interface_name" jsonschema:"Name of the interface to find implementors for"`
	PackagePattern string `json:"package_pattern" jsonschema:"Go package pattern to scan (e.g. ./... or ./internal/...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	IncludeStdlib  bool   `json:"include_stdlib,omitempty" jsonschema:"Include standard library implementations"`
}

type PackageDependenciesInput struct {
	PackagePattern string `json:"package_pattern,omitempty" jsonschema:"Go package pattern to scan (defaults to ./...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	IncludeStdlib  bool   `json:"include_stdlib,omitempty" jsonschema:"Include standard library imports"`
}

type ReloadWorkspaceInput struct {
	PackagePattern string `json:"package_pattern,omitempty" jsonschema:"Go package pattern to reload (defaults to ./...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

type ReloadWorkspaceResult struct {
	RootPath        string `json:"root_path"`
	PackagePattern  string `json:"package_pattern"`
	PackagesLoaded  int    `json:"packages_loaded"`
	FunctionsLoaded int    `json:"functions_loaded"`
}

type CallHierarchyInput struct {
	FunctionName   string `json:"function_name" jsonschema:"Function name to analyze; may be short name or package-qualified"`
	PackagePattern string `json:"package_pattern,omitempty" jsonschema:"Go package pattern to scan (defaults to ./...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
	MaxDepth       int    `json:"max_depth,omitempty" jsonschema:"Maximum call depth, capped at 3"`
}

type StructLifecycleInput struct {
	StructName     string `json:"struct_name" jsonschema:"Struct type name to trace"`
	PackagePattern string `json:"package_pattern,omitempty" jsonschema:"Go package pattern to scan (defaults to ./...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

type ConcurrencyRisksInput struct {
	PackagePattern string `json:"package_pattern,omitempty" jsonschema:"Go package pattern to scan (defaults to ./...)"`
	RootPath       string `json:"root_path,omitempty" jsonschema:"Root directory of the Go project (defaults to cwd)"`
}

func main() {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "go-arch-xray",
			Version: "0.1.0",
		},
		nil,
	)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_interface_topology",
		Description: "Find all structs that implement a given Go interface, including via embedding. Returns struct names, package paths, and source locations.",
	}, handleInterfaceTopology)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "get_package_dependencies",
		Description: "Return direct package import dependencies for a Go package pattern. Useful for architecture boundary and layering inspection.",
	}, handlePackageDependencies)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "reload_workspace",
		Description: "Invalidate and reload the cached Go package/SSA analysis for a root path and package pattern.",
	}, handleReloadWorkspace)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "analyze_call_hierarchy",
		Description: "Build a CHA static call hierarchy from a target function, capped at 3 hops, with static/interface/goroutine edge labels.",
	}, handleAnalyzeCallHierarchy)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "trace_struct_lifecycle",
		Description: "Trace struct instantiation, field mutation, and interface handoff points across SSA.",
	}, handleTraceStructLifecycle)

	mcp.AddTool(server, &mcp.Tool{
		Name:        "detect_concurrency_risks",
		Description: "Detect heuristic goroutine field mutation risks without visible mutex or atomic protection.",
	}, handleDetectConcurrencyRisks)

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

	result, err := analyzer.GetInterfaceTopology(workspace, rootPath, input.PackagePattern, input.InterfaceName, input.IncludeStdlib)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, result, nil
}

func handlePackageDependencies(ctx context.Context, req *mcp.CallToolRequest, input PackageDependenciesInput) (*mcp.CallToolResult, *analyzer.DependencyResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}

	result, err := analyzer.GetPackageDependencies(workspace, rootPath, input.PackagePattern, input.IncludeStdlib)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, result, nil
}

func handleReloadWorkspace(ctx context.Context, req *mcp.CallToolRequest, input ReloadWorkspaceInput) (*mcp.CallToolResult, *ReloadWorkspaceResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}
	pattern := input.PackagePattern
	if strings.TrimSpace(pattern) == "" {
		pattern = "./..."
	}

	prog, err := workspace.Reload(rootPath, pattern)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, &ReloadWorkspaceResult{
		RootPath:        rootPath,
		PackagePattern:  pattern,
		PackagesLoaded:  len(prog.Packages),
		FunctionsLoaded: len(prog.SSAFuncs),
	}, nil
}

func handleAnalyzeCallHierarchy(ctx context.Context, req *mcp.CallToolRequest, input CallHierarchyInput) (*mcp.CallToolResult, *analyzer.CallHierarchyResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}

	result, err := analyzer.AnalyzeCallHierarchy(workspace, rootPath, input.PackagePattern, input.FunctionName, input.MaxDepth)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, result, nil
}

func handleTraceStructLifecycle(ctx context.Context, req *mcp.CallToolRequest, input StructLifecycleInput) (*mcp.CallToolResult, *analyzer.StructLifecycleResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}

	result, err := analyzer.TraceStructLifecycle(workspace, rootPath, input.PackagePattern, input.StructName)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, result, nil
}

func handleDetectConcurrencyRisks(ctx context.Context, req *mcp.CallToolRequest, input ConcurrencyRisksInput) (*mcp.CallToolResult, *analyzer.ConcurrencyRiskResult, error) {
	rootPath, err := resolveRootPath(input.RootPath)
	if err != nil {
		return nil, nil, err
	}

	result, err := analyzer.DetectConcurrencyRisks(workspace, rootPath, input.PackagePattern)
	if err != nil {
		return &mcp.CallToolResult{
			IsError: true,
			Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
		}, nil, nil
	}

	return nil, result, nil
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
