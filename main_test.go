package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/HAYASAKA7/go-arch-xray/analyzer"
)

func TestHandlePackageDependencies_ReturnsStructuredDependencies(t *testing.T) {
	dir := createMainTestModule(t, "handlerdeps", map[string]string{
		"app/app.go":  "package app\n\nimport \"handlerdeps/domain\"\n\nfunc Run() string { return domain.Name() }\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success result, got tool result: %#v", toolResult)
	}
	if result == nil {
		t.Fatal("expected dependency result")
	}
	if !mainHasDependency(result, "handlerdeps/app", "handlerdeps/domain") {
		t.Fatal("missing handlerdeps/app -> handlerdeps/domain dependency")
	}
}

func TestHandleInterfaceTopology_ReturnsStructuredTopology(t *testing.T) {
	dir := createMainTestModule(t, "handlertopo", map[string]string{
		"iface.go": "package main\n\ntype Worker interface {\n\tWork()\n}\n",
		"a.go":     "package main\n\ntype A struct{}\nfunc (A) Work() {}\n",
		"b.go":     "package main\n\ntype B struct{}\nfunc (B) Work() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleInterfaceTopology(context.Background(), nil, InterfaceTopologyInput{
		RootPath:      dir,
		InterfaceName: "Worker",
		Limit:         1,
		Summary:       true,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil {
		t.Fatal("expected topology result")
	}
	if len(result.Implementors) != 1 {
		t.Fatalf("expected exactly 1 implementor due to limit, got %d", len(result.Implementors))
	}
	if result.TotalBeforeTruncate != 2 {
		t.Fatalf("expected 2 total implementors before truncate, got %d", result.TotalBeforeTruncate)
	}
	if result.Summary == nil || result.Summary.TotalImplementors != 2 {
		t.Fatalf("expected summary with 2 total implementors, got %#v", result.Summary)
	}
}

func TestHandleInterfaceTopology_ReturnsToolErrorForInvalidInput(t *testing.T) {
	dir := createMainTestModule(t, "handlerinvalid", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleInterfaceTopology(context.Background(), nil, InterfaceTopologyInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error result, got %#v", toolResult)
	}
}

func TestHandleReloadWorkspace_ReloadsChangedSource(t *testing.T) {
	dir := createMainTestModule(t, "handlerreload", map[string]string{
		"main.go": "package main\n\nfunc Version() string { return \"v1\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, err := workspace.GetOrLoad(dir, "./..."); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Version() string { return \"v2\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	toolResult, result, err := handleReloadWorkspace(context.Background(), nil, ReloadWorkspaceInput{RootPath: dir})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured reload success, got %#v", toolResult)
	}
	if result == nil || result.PackagesLoaded == 0 {
		t.Fatalf("expected reload summary with loaded packages, got %#v", result)
	}
}

func TestHandleAnalyzeCallHierarchy_ReturnsStructuredEdges(t *testing.T) {
	dir := createMainTestModule(t, "handlercalls", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{
		RootPath:     dir,
		FunctionName: "Root",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured call hierarchy success, got %#v", toolResult)
	}
	if result == nil || !mainHasCallEdge(result, "Root", "Worker") {
		t.Fatalf("expected Root -> Worker edge, got %#v", result)
	}
}

func TestHandleAnalyzeCallHierarchy_ReturnsToolErrorForMissingFunction(t *testing.T) {
	dir := createMainTestModule(t, "handlermissingcall", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleAnalyzeCallHierarchy(context.Background(), nil, CallHierarchyInput{
		RootPath:     dir,
		FunctionName: "Missing",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error, got %#v", toolResult)
	}
}

func TestHandleTraceStructLifecycle_ReturnsStructuredHops(t *testing.T) {
	dir := createMainTestModule(t, "handlerlife", map[string]string{
		"main.go": "package main\n\ntype User struct{ Name string }\nfunc NewUser() *User { return &User{} }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleTraceStructLifecycle(context.Background(), nil, StructLifecycleInput{
		RootPath:   dir,
		StructName: "User",
		Summary:    true,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured lifecycle success, got %#v", toolResult)
	}
	if result == nil || !mainHasLifecycleHop(result, "Instantiate") {
		t.Fatalf("expected Instantiate hop, got %#v", result)
	}
	if result.Summary == nil {
		t.Fatal("expected lifecycle summary")
	}
}

func TestHandleTraceStructLifecycle_ReturnsToolErrorForInvalidInput(t *testing.T) {
	dir := createMainTestModule(t, "handlerlifeinvalid", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleTraceStructLifecycle(context.Background(), nil, StructLifecycleInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error, got %#v", toolResult)
	}
}

func TestHandleDetectConcurrencyRisks_ReturnsStructuredRisks(t *testing.T) {
	dir := createMainTestModule(t, "handlerrisk", map[string]string{
		"main.go": "package main\n\ntype State struct{ Count int }\nfunc Run(s *State) { go func() { s.Count++ }() }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleDetectConcurrencyRisks(context.Background(), nil, ConcurrencyRisksInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured risk success, got %#v", toolResult)
	}
	if result == nil || !mainHasConcurrencyRisk(result, "High") {
		t.Fatalf("expected high concurrency risk, got %#v", result)
	}
}

func TestHandleFindCallers_ReturnsIncomingEdges(t *testing.T) {
	dir := createMainTestModule(t, "handlercallers", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleFindCallers(context.Background(), nil, CallersInput{
		RootPath:     dir,
		FunctionName: "Worker",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured callers success, got %#v", toolResult)
	}
	if result == nil || len(result.Edges) == 0 {
		t.Fatalf("expected caller edges, got %#v", result)
	}
}

func TestHandleCheckArchitectureBoundaries_DetectsForbidViolation(t *testing.T) {
	dir := createMainTestModule(t, "handlerbounds", map[string]string{
		"app/app.go": "package app\n\nimport \"handlerbounds/db\"\n\nfunc Run() string { return db.Query() }\n",
		"db/db.go":   "package db\n\nfunc Query() string { return \"\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	rules := []analyzer.BoundaryRule{
		{Type: analyzer.RuleForbid, From: "handlerbounds/app", To: "handlerbounds/db"},
	}
	toolResult, result, err := handleCheckArchitectureBoundaries(context.Background(), nil, CheckArchitectureBoundariesInput{
		RootPath: dir,
		Rules:    rules,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.ViolationCount == 0 {
		t.Fatalf("expected at least one boundary violation, got %#v", result)
	}
}

func TestHandleCheckArchitectureBoundaries_NoRulesReturnsEmpty(t *testing.T) {
	dir := createMainTestModule(t, "handlerboundsnone", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleCheckArchitectureBoundaries(context.Background(), nil, CheckArchitectureBoundariesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.ViolationCount != 0 {
		t.Fatalf("expected zero violations with no rules, got %#v", result)
	}
}

func TestHandleListEntrypoints_DetectsMainFunction(t *testing.T) {
	dir := createMainTestModule(t, "handlerentrypoints", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleListEntrypoints(context.Background(), nil, ListEntrypointsInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.Total == 0 {
		t.Fatalf("expected at least one entrypoint, got %#v", result)
	}
	found := false
	for _, ep := range result.Entrypoints {
		if ep.Kind == analyzer.EntrypointMain {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected a main entrypoint, got %#v", result.Entrypoints)
	}
}

func TestHandleListEntrypoints_TotalMatchesSlice(t *testing.T) {
	dir := createMainTestModule(t, "handlerentrypointslen", map[string]string{
		"main.go": "package main\n\nfunc main() { go worker() }\nfunc worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	_, result, err := handleListEntrypoints(context.Background(), nil, ListEntrypointsInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result == nil {
		t.Fatal("expected entrypoints result")
	}
	if result.Total != len(result.Entrypoints) {
		t.Fatalf("Total %d != len(Entrypoints) %d", result.Total, len(result.Entrypoints))
	}
}

func TestHandleListHTTPRoutes_DetectsHandleFunc(t *testing.T) {
	dir := createMainTestModule(t, "handlerroutes", map[string]string{
		"main.go": "package main\n\ntype Router struct{}\n\nfunc (r *Router) HandleFunc(path string, h func()) {}\n\nfunc handler() {}\n\nfunc main() {\n\tr := &Router{}\n\tr.HandleFunc(\"/api/v1\", handler)\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleListHTTPRoutes(context.Background(), nil, ListHTTPRoutesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.Total == 0 {
		t.Fatalf("expected HTTP routes, got %#v", result)
	}
	if result.Routes[0].Path != "/api/v1" {
		t.Fatalf("expected path /api/v1, got %q", result.Routes[0].Path)
	}
}

func TestHandleListHTTPRoutes_TotalMatchesSlice(t *testing.T) {
	dir := createMainTestModule(t, "handlerrouteslen", map[string]string{
		"main.go": "package main\n\ntype Router struct{}\n\nfunc (r *Router) HandleFunc(path string, h func()) {}\n\nfunc main() {\n\tr := &Router{}\n\tr.HandleFunc(\"/a\", nil)\n\tr.HandleFunc(\"/b\", nil)\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	_, result, err := handleListHTTPRoutes(context.Background(), nil, ListHTTPRoutesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result == nil {
		t.Fatal("expected routes result")
	}
	if result.Total != len(result.Routes) {
		t.Fatalf("Total %d != len(Routes) %d", result.Total, len(result.Routes))
	}
}

func TestHandleListGRPCEndpoints_DetectsGeneratedDescriptor(t *testing.T) {
	dir := createMainTestModule(t, "handlergrpc", map[string]string{
		"grpc/grpc.go":     "package grpc\n\ntype ServiceDesc struct { ServiceName string; Methods []MethodDesc; Metadata any }\ntype MethodDesc struct { MethodName string; Handler any }\n",
		"pb/greeter.pb.go": "package pb\n\nimport \"handlergrpc/grpc\"\n\nfunc h() {}\n\nvar Greeter_ServiceDesc = grpc.ServiceDesc{\n\tServiceName: \"handler.Greeter\",\n\tMethods: []grpc.MethodDesc{{MethodName: \"Ping\", Handler: h}},\n\tMetadata: \"handler.proto\",\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleListGRPCEndpoints(context.Background(), nil, ListGRPCEndpointsInput{RootPath: dir})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.Total != 1 {
		t.Fatalf("expected one gRPC endpoint, got %#v", result)
	}
	endpoint := result.Endpoints[0]
	if endpoint.FullMethod != "/handler.Greeter/Ping" || endpoint.RPCType != analyzer.GRPCRPCUnary {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
	if endpoint.Handler != "h" || endpoint.ServiceDesc != "Greeter_ServiceDesc" || endpoint.ProtoFile != "handler.proto" {
		t.Fatalf("unexpected endpoint metadata: %+v", endpoint)
	}
}

func TestHandleListGRPCEndpoints_InvalidRootPathReturnsToolError(t *testing.T) {
	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleListGRPCEndpoints(context.Background(), nil, ListGRPCEndpointsInput{
		RootPath: "/path/to/nonexistent/dir/that/should/not/exist",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error result for invalid root path, got %#v", toolResult)
	}
}

func TestHandleComputeComplexityMetrics_HalsteadMaintainabilityInputs(t *testing.T) {
	dir := createMainTestModule(t, "handlercomplexityhalstead", map[string]string{
		"quality.go": "package quality\n\nfunc simple() int {\n\treturn 1\n}\n\nfunc dense(a, b, c, d int) int {\n\tresult := ((a + b) * (c - d)) / (a + 1)\n\tif result > 10 {\n\t\treturn result\n\t}\n\treturn result + b\n}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleComputeComplexityMetrics(context.Background(), nil, ComplexityMetricsInput{
		RootPath:                dir,
		MinHalsteadVolume:       10,
		MaxMaintainabilityIndex: 100,
		SortBy:                  "maintainability",
		IncludePackages:         true,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got tool result: %#v", toolResult)
	}
	if result == nil || result.Total != 1 {
		t.Fatalf("expected one filtered complexity result, got %#v", result)
	}
	function := result.Functions[0]
	if function.Name != "dense" || function.HalsteadVolume <= 10 || function.MaintainabilityIndex <= 0 {
		t.Fatalf("unexpected Halstead complexity result: %+v", function)
	}
	if len(result.Packages) != 1 || result.Packages[0].MaxHalsteadFunction == "" {
		t.Fatalf("expected package Halstead aggregate, got %+v", result.Packages)
	}
}

func TestHandleCacheStatusAndClearCache(t *testing.T) {
	dir := createMainTestModule(t, "handlercache", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, err := workspace.GetOrLoad(dir, "./..."); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	_, status, err := handleCacheStatus(context.Background(), nil, CacheStatusInput{})
	if err != nil {
		t.Fatalf("cache status failed: %v", err)
	}
	if status == nil || status.CacheSize == 0 {
		t.Fatalf("expected non-empty cache status, got %#v", status)
	}

	_, clearRes, err := handleClearCache(context.Background(), nil, ClearCacheInput{All: true})
	if err != nil {
		t.Fatalf("clear cache failed: %v", err)
	}
	if clearRes == nil || clearRes.Cleared == 0 {
		t.Fatalf("expected cleared entries, got %#v", clearRes)
	}
	if clearRes.CacheSize != 0 {
		t.Fatalf("expected empty cache after clear-all, got %#v", clearRes)
	}
}

func TestHandlePackageDependencies_InvalidRootPathReturnsToolError(t *testing.T) {
	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{
		RootPath: "/path/to/nonexistent/dir/that/should/not/exist",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error result for invalid root path, got %#v", toolResult)
	}
}

func TestHandlePackageDependencies_MalformedPackagePatternReturnsToolError(t *testing.T) {
	dir := createMainTestModule(t, "handlerbadpattern", map[string]string{
		"main.go": "package main\n\nfunc main() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handlePackageDependencies(context.Background(), nil, PackageDependenciesInput{
		RootPath:       dir,
		PackagePattern: "invalidquery=foo", // invalid query operator, causes packages.Load to error
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected no structured result, got %#v", result)
	}
	if toolResult == nil || !toolResult.IsError {
		t.Fatalf("expected MCP tool error result for malformed pattern, got %#v", toolResult)
	}
}

func TestResolveRootPath_FallbackToWD(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveRootPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved != wd {
		t.Fatalf("expected resolved path %q, got %q", wd, resolved)
	}
}

func TestHandleFindCallPath_ReturnsPaths(t *testing.T) {
	dir := createMainTestModule(t, "handlercallpath", map[string]string{
		"main.go": "package main\n\nfunc Root() { Worker() }\nfunc Worker() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleFindCallPath(context.Background(), nil, FindCallPathInput{
		RootPath:     dir,
		FromFunction: "Root",
		ToFunction:   "Worker",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured call path success, got %#v", toolResult)
	}
	if result == nil || len(result.Paths) == 0 {
		t.Fatalf("expected call paths, got %#v", result)
	}
}

func TestHandleDetectImportCycles_ReturnsZeroForValidModule(t *testing.T) {
	dir := createMainTestModule(t, "handlercyclesvalid", map[string]string{
		"a/a.go": "package a\n\nimport _ \"handlercyclesvalid/b\"\n",
		"b/b.go": "package b\n\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleDetectImportCycles(context.Background(), nil, DetectImportCyclesInput{
		RootPath: dir,
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got %#v", toolResult)
	}
	if result == nil || result.CycleCount != 0 {
		t.Fatalf("expected 0 import cycles for valid module, got %d", result.CycleCount)
	}
}

func TestHandleFindReverseDependencies_ReturnsReverseDeps(t *testing.T) {
	dir := createMainTestModule(t, "handlerrevdeps", map[string]string{
		"app/app.go":  "package app\n\nimport _ \"handlerrevdeps/domain\"\n",
		"domain/d.go": "package domain\n\nfunc Name() string { return \"domain\" }\n",
	})

	workspace = analyzer.NewWorkspace()
	toolResult, result, err := handleFindReverseDependencies(context.Background(), nil, FindReverseDependenciesInput{
		RootPath:      dir,
		TargetPackage: "handlerrevdeps/domain",
	})
	if err != nil {
		t.Fatalf("unexpected handler error: %v", err)
	}
	if toolResult != nil {
		t.Fatalf("expected structured success, got %#v", toolResult)
	}
	if result == nil || result.DirectCount == 0 {
		t.Fatalf("expected reverse dependencies, got %#v", result)
	}
}

func TestHandleClearCache_WithPattern(t *testing.T) {
	dir := createMainTestModule(t, "handlerclearpattern", map[string]string{
		"main.go": "package main\n\nfunc Root() {}\n",
	})

	workspace = analyzer.NewWorkspace()
	if _, err := workspace.GetOrLoad(dir, "./..."); err != nil {
		t.Fatalf("initial load failed: %v", err)
	}

	_, clearRes, err := handleClearCache(context.Background(), nil, ClearCacheInput{
		RootPath:       dir,
		PackagePattern: "./...",
	})
	if err != nil {
		t.Fatalf("clear cache failed: %v", err)
	}
	if clearRes == nil || clearRes.Cleared == 0 {
		t.Fatalf("expected cleared entries, got %#v", clearRes)
	}
}

func mainHasDependency(r *analyzer.DependencyResult, from, to string) bool {
	for _, node := range r.Packages {
		if node.Package != from {
			continue
		}
		for _, imp := range node.Imports {
			if imp == to {
				return true
			}
		}
	}
	return false
}

func mainHasConcurrencyRisk(r *analyzer.ConcurrencyRiskResult, level string) bool {
	for _, risk := range r.Risks {
		if risk.RiskLevel == level {
			return true
		}
	}
	return false
}

func mainHasLifecycleHop(r *analyzer.StructLifecycleResult, kind string) bool {
	for _, hop := range r.Hops {
		if hop.Kind == kind {
			return true
		}
	}
	return false
}

func mainHasCallEdge(r *analyzer.CallHierarchyResult, caller, callee string) bool {
	for _, edge := range r.Edges {
		if analyzerShortName(edge.Caller) == caller && analyzerShortName(edge.Callee) == callee {
			return true
		}
	}
	return false
}

func analyzerShortName(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i+1:]
		}
	}
	return name
}

func createMainTestModule(t testing.TB, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	modContent := "module " + name + "\n\ngo 1.23\n"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(modContent), 0o644); err != nil {
		t.Fatal(err)
	}
	for fname, content := range files {
		path := filepath.Join(dir, fname)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
