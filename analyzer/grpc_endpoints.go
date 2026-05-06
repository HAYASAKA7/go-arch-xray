package analyzer

import (
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

const (
	GRPCRPCUnary        = "unary"
	GRPCRPCClientStream = "client_stream"
	GRPCRPCServerStream = "server_stream"
	GRPCRPCBidiStream   = "bidi_stream"
)

// GRPCEndpoint describes one RPC method discovered from generated grpc-go
// ServiceDesc metadata.
type GRPCEndpoint struct {
	Service          string   `json:"service"`
	ServiceShortName string   `json:"service_short_name,omitempty"`
	Method           string   `json:"method"`
	FullMethod       string   `json:"full_method"`
	RPCType          string   `json:"rpc_type"`
	Handler          string   `json:"handler,omitempty"`
	HandlerType      string   `json:"handler_type,omitempty"`
	ServiceDesc      string   `json:"service_desc,omitempty"`
	ProtoFile        string   `json:"proto_file,omitempty"`
	Package          string   `json:"package"`
	Registered       bool     `json:"registered"`
	Implementations  []string `json:"implementations,omitempty"`
	File             string   `json:"file,omitempty"`
	Line             int      `json:"line,omitempty"`
	Anchor           string   `json:"context_anchor,omitempty"`
}

// GRPCRegistration describes a generated Register<Service>Server call site.
type GRPCRegistration struct {
	Service          string `json:"service,omitempty"`
	ServiceShortName string `json:"service_short_name,omitempty"`
	RegisterFunc     string `json:"register_func"`
	Registrar        string `json:"registrar,omitempty"`
	Implementation   string `json:"implementation,omitempty"`
	Package          string `json:"package"`
	File             string `json:"file,omitempty"`
	Line             int    `json:"line,omitempty"`
	Anchor           string `json:"context_anchor,omitempty"`
}

// GRPCEndpointsResult is returned by ListGRPCEndpoints.
type GRPCEndpointsResult struct {
	Endpoints          []GRPCEndpoint     `json:"endpoints"`
	Registrations      []GRPCRegistration `json:"registrations,omitempty"`
	Total              int                `json:"total"`
	TotalRegistrations int                `json:"total_registrations"`
	Notes              []string           `json:"notes,omitempty"`

	Offset              int    `json:"offset,omitempty"`
	Limit               int    `json:"limit,omitempty"`
	MaxItems            int    `json:"max_items,omitempty"`
	ChunkSize           int    `json:"chunk_size,omitempty"`
	NextCursor          string `json:"next_cursor,omitempty"`
	HasMore             bool   `json:"has_more,omitempty"`
	TotalBeforeTruncate int    `json:"total_before_truncate"`
	Truncated           bool   `json:"truncated"`
}

type grpcServiceDesc struct {
	Name             string
	Service          string
	ServiceShortName string
	HandlerType      string
	ProtoFile        string
	Package          string
	File             string
	Line             int
	Methods          []grpcMethodDesc
}

type grpcMethodDesc struct {
	Name    string
	Handler string
	RPCType string
}

type grpcExtraction struct {
	endpoints     []GRPCEndpoint
	registrations []GRPCRegistration
}

type grpcResultItem struct {
	Kind         string
	Endpoint     GRPCEndpoint
	Registration GRPCRegistration
}

// ListGRPCEndpoints reports gRPC service methods discovered from generated
// grpc-go ServiceDesc metadata and best-effort Register<Service>Server call
// sites in the loaded packages.
func ListGRPCEndpoints(ws *Workspace, dir, pattern string) (*GRPCEndpointsResult, error) {
	return ListGRPCEndpointsWithOptions(ws, dir, pattern, QueryOptions{})
}

func ListGRPCEndpointsWithOptions(ws *Workspace, dir, pattern string, opts QueryOptions) (*GRPCEndpointsResult, error) {
	prog, err := ws.GetOrLoad(dir, pattern)
	if err != nil {
		return nil, fmt.Errorf("loading packages: %w", err)
	}

	endpoints := append([]GRPCEndpoint(nil), prog.grpcEndpoints...)
	registrations := append([]GRPCRegistration(nil), prog.grpcRegistrations...)
	sortGRPCEndpoints(endpoints)
	sortGRPCRegistrations(registrations)

	result := &GRPCEndpointsResult{
		Total:              len(endpoints),
		TotalRegistrations: len(registrations),
		Notes: []string{
			"gRPC endpoints are discovered from generated grpc-go ServiceDesc values and Register<Service>Server call sites in loaded Go packages.",
			"Use package_pattern/package_patterns that include generated *.pb.go or *_grpc.pb.go packages; proto-only directories without Go packages are not loaded.",
			"Pagination and streaming apply across endpoint rows and registration rows together; total and total_registrations report the full unpaged counts for each kind.",
			"Dynamic grpc.ServiceDesc construction, reflection-only registration, grpc-gateway, Connect, and Twirp are outside this tool's v1 scope.",
		},
	}
	result.Offset = opts.Offset
	result.Limit = opts.Limit
	result.MaxItems = opts.MaxItems

	items := buildGRPCResultItems(endpoints, registrations)
	var err2 error
	items, result.TotalBeforeTruncate, result.Truncated, result.HasMore, result.NextCursor, err2 = streamOrWindow(items, "grpc_endpoints:"+dir+"|"+pattern, grpcResultItemKey, opts)
	if err2 != nil {
		return nil, err2
	}
	result.Endpoints, result.Registrations = splitGRPCResultItems(items)
	if opts.ChunkSize > 0 {
		result.ChunkSize = clampChunkSize(opts.ChunkSize)
	}

	return result, nil
}

// extractGRPCFromSyntax walks loaded package syntax once during workspace load.
// It intentionally relies on generated Go descriptors rather than parsing .proto
// files so it does not need another parser dependency.
func extractGRPCFromSyntax(pkgs []*packages.Package) grpcExtraction {
	services := make(map[string]*grpcServiceDesc, 16)
	registerFuncToDesc := make(map[string]string, 16)
	registrations := make([]GRPCRegistration, 0, 16)

	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				switch d := decl.(type) {
				case *ast.GenDecl:
					extractGRPCServiceDescsFromDecl(pkg, d, services)
				case *ast.FuncDecl:
					if name, descName := extractRegisterFuncDescriptor(pkg.Fset, d); name != "" && descName != "" {
						registerFuncToDesc[grpcRegisterFuncKey(pkg.PkgPath, name)] = grpcServiceDescKey(pkg.PkgPath, descName)
					}
				}
			}
		}
	}

	serviceByShortName := uniqueGRPCServicesByShortName(services)

	for _, pkg := range pkgs {
		if pkg.Fset == nil || len(pkg.Syntax) == 0 || pkg.PkgPath == "" {
			continue
		}
		for _, file := range pkg.Syntax {
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				registration := extractGRPCRegistration(pkg, call, registerFuncToDesc, services, serviceByShortName)
				if registration != nil {
					registrations = append(registrations, *registration)
				}
				return true
			})
		}
	}

	endpoints := buildGRPCEndpoints(services, registrations)
	sortGRPCEndpoints(endpoints)
	sortGRPCRegistrations(registrations)
	return grpcExtraction{endpoints: endpoints, registrations: registrations}
}

func extractGRPCServiceDescsFromDecl(pkg *packages.Package, decl *ast.GenDecl, services map[string]*grpcServiceDesc) {
	for _, spec := range decl.Specs {
		valueSpec, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, value := range valueSpec.Values {
			if i >= len(valueSpec.Names) || valueSpec.Names[i] == nil {
				continue
			}
			lit, ok := value.(*ast.CompositeLit)
			if !ok || !isGRPCCompositeType(lit.Type, "ServiceDesc") {
				continue
			}
			service := parseGRPCServiceDesc(pkg, valueSpec.Names[i].Name, lit)
			if service != nil && service.Service != "" {
				services[grpcServiceDescKey(pkg.PkgPath, service.Name)] = service
			}
		}
	}
}

func parseGRPCServiceDesc(pkg *packages.Package, descName string, lit *ast.CompositeLit) *grpcServiceDesc {
	pos := pkg.Fset.Position(lit.Pos())
	service := &grpcServiceDesc{
		Name:    descName,
		Package: pkg.PkgPath,
		File:    pos.Filename,
		Line:    pos.Line,
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key := identName(kv.Key)
		switch key {
		case "ServiceName":
			service.Service = stringLiteral(kv.Value)
			service.ServiceShortName = grpcServiceShortName(service.Service)
		case "HandlerType":
			service.HandlerType = grpcExprString(pkg.Fset, kv.Value)
		case "Metadata":
			service.ProtoFile = stringLiteral(kv.Value)
		case "Methods":
			service.Methods = append(service.Methods, parseGRPCMethods(pkg.Fset, kv.Value)...)
		case "Streams":
			service.Methods = append(service.Methods, parseGRPCStreams(pkg.Fset, kv.Value)...)
		}
	}
	return service
}

func parseGRPCMethods(fset *token.FileSet, expr ast.Expr) []grpcMethodDesc {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	out := make([]grpcMethodDesc, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		methodLit, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}
		method := grpcMethodDesc{RPCType: GRPCRPCUnary}
		for _, field := range methodLit.Elts {
			kv, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			switch identName(kv.Key) {
			case "MethodName":
				method.Name = stringLiteral(kv.Value)
			case "Handler":
				method.Handler = grpcExprString(fset, kv.Value)
			}
		}
		if method.Name != "" {
			out = append(out, method)
		}
	}
	return out
}

func parseGRPCStreams(fset *token.FileSet, expr ast.Expr) []grpcMethodDesc {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	out := make([]grpcMethodDesc, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		streamLit, ok := elt.(*ast.CompositeLit)
		if !ok {
			continue
		}
		stream := grpcMethodDesc{}
		serverStreams := false
		clientStreams := false
		for _, field := range streamLit.Elts {
			kv, ok := field.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			switch identName(kv.Key) {
			case "StreamName":
				stream.Name = stringLiteral(kv.Value)
			case "Handler":
				stream.Handler = grpcExprString(fset, kv.Value)
			case "ServerStreams":
				serverStreams = boolLiteral(kv.Value)
			case "ClientStreams":
				clientStreams = boolLiteral(kv.Value)
			}
		}
		stream.RPCType = grpcRPCType(serverStreams, clientStreams)
		if stream.Name != "" {
			out = append(out, stream)
		}
	}
	return out
}

func extractRegisterFuncDescriptor(fset *token.FileSet, fd *ast.FuncDecl) (string, string) {
	if fd == nil || fd.Name == nil || fd.Body == nil || !isGRPCRegisterFuncName(fd.Name.Name) {
		return "", ""
	}
	registerFunc := fd.Name.Name
	descName := ""
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if descName != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if selectorName(call.Fun) != "RegisterService" {
			return true
		}
		descName = grpcDescriptorArgName(call.Args[0], fset)
		return descName == ""
	})
	return registerFunc, descName
}

func extractGRPCRegistration(pkg *packages.Package, call *ast.CallExpr, registerFuncToDesc map[string]string, services map[string]*grpcServiceDesc, serviceByShortName map[string]*grpcServiceDesc) *GRPCRegistration {
	calleeName := selectorName(call.Fun)
	if !isGRPCRegisterFuncName(calleeName) || len(call.Args) < 2 {
		return nil
	}
	shortName := strings.TrimSuffix(strings.TrimPrefix(calleeName, "Register"), "Server")
	serviceName := ""
	if descKey := registerFuncToDesc[grpcRegisterCallKey(pkg, call.Fun, calleeName)]; descKey != "" {
		if service := services[descKey]; service != nil {
			serviceName = service.Service
			shortName = service.ServiceShortName
		}
	}
	if serviceName == "" {
		if service := serviceByShortName[shortName]; service != nil {
			serviceName = service.Service
		}
	}
	pos := pkg.Fset.Position(call.Pos())
	return &GRPCRegistration{
		Service:          serviceName,
		ServiceShortName: shortName,
		RegisterFunc:     grpcExprString(pkg.Fset, call.Fun),
		Registrar:        grpcExprString(pkg.Fset, call.Args[0]),
		Implementation:   grpcExprString(pkg.Fset, call.Args[1]),
		Package:          pkg.PkgPath,
		File:             pos.Filename,
		Line:             pos.Line,
		Anchor:           contextAnchor(pos.Filename, pos.Line, calleeName),
	}
}

func buildGRPCEndpoints(services map[string]*grpcServiceDesc, registrations []GRPCRegistration) []GRPCEndpoint {
	implementationsByService := make(map[string][]string, len(registrations))
	uniqueShortNames := uniqueGRPCShortNames(services)
	for _, registration := range registrations {
		keys := []string{registration.Service}
		if uniqueShortNames[registration.ServiceShortName] {
			keys = append(keys, registration.ServiceShortName)
		}
		for _, key := range keys {
			if key == "" {
				continue
			}
			implementationsByService[key] = appendUniqueString(implementationsByService[key], registration.Implementation)
		}
	}

	endpoints := make([]GRPCEndpoint, 0, len(services))
	for _, service := range services {
		for _, method := range service.Methods {
			implementations := append([]string(nil), implementationsByService[service.Service]...)
			if len(implementations) == 0 {
				implementations = append([]string(nil), implementationsByService[service.ServiceShortName]...)
			}
			endpoints = append(endpoints, GRPCEndpoint{
				Service:          service.Service,
				ServiceShortName: service.ServiceShortName,
				Method:           method.Name,
				FullMethod:       "/" + service.Service + "/" + method.Name,
				RPCType:          method.RPCType,
				Handler:          method.Handler,
				HandlerType:      service.HandlerType,
				ServiceDesc:      service.Name,
				ProtoFile:        service.ProtoFile,
				Package:          service.Package,
				Registered:       len(implementations) > 0,
				Implementations:  implementations,
				File:             service.File,
				Line:             service.Line,
				Anchor:           contextAnchor(service.File, service.Line, service.ServiceShortName),
			})
		}
	}
	return endpoints
}

func buildGRPCResultItems(endpoints []GRPCEndpoint, registrations []GRPCRegistration) []grpcResultItem {
	items := make([]grpcResultItem, 0, len(endpoints)+len(registrations))
	for _, endpoint := range endpoints {
		items = append(items, grpcResultItem{Kind: "endpoint", Endpoint: endpoint})
	}
	for _, registration := range registrations {
		items = append(items, grpcResultItem{Kind: "registration", Registration: registration})
	}
	return items
}

func splitGRPCResultItems(items []grpcResultItem) ([]GRPCEndpoint, []GRPCRegistration) {
	endpoints := make([]GRPCEndpoint, 0, len(items))
	registrations := make([]GRPCRegistration, 0, len(items))
	for _, item := range items {
		switch item.Kind {
		case "endpoint":
			endpoints = append(endpoints, item.Endpoint)
		case "registration":
			registrations = append(registrations, item.Registration)
		}
	}
	return endpoints, registrations
}

func uniqueGRPCServicesByShortName(services map[string]*grpcServiceDesc) map[string]*grpcServiceDesc {
	counts := make(map[string]int, len(services))
	for _, service := range services {
		if service.ServiceShortName != "" {
			counts[service.ServiceShortName]++
		}
	}

	unique := make(map[string]*grpcServiceDesc, len(services))
	for _, service := range services {
		if service.ServiceShortName != "" && counts[service.ServiceShortName] == 1 {
			unique[service.ServiceShortName] = service
		}
	}
	return unique
}

func uniqueGRPCShortNames(services map[string]*grpcServiceDesc) map[string]bool {
	counts := make(map[string]int, len(services))
	for _, service := range services {
		if service.ServiceShortName != "" {
			counts[service.ServiceShortName]++
		}
	}

	unique := make(map[string]bool, len(counts))
	for shortName, count := range counts {
		unique[shortName] = count == 1
	}
	return unique
}

func isGRPCCompositeType(expr ast.Expr, name string) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel != nil && e.Sel.Name == name
	case *ast.Ident:
		return e.Name == name
	case *ast.ArrayType:
		return isGRPCCompositeType(e.Elt, name)
	}
	return false
}

func isGRPCRegisterFuncName(name string) bool {
	return strings.HasPrefix(name, "Register") && strings.HasSuffix(name, "Server") && len(name) > len("RegisterServer")
}

func grpcServiceDescKey(pkgPath, descName string) string {
	return pkgPath + "|" + descName
}

func grpcRegisterFuncKey(pkgPath, funcName string) string {
	return pkgPath + "|" + funcName
}

func grpcRegisterCallKey(pkg *packages.Package, expr ast.Expr, funcName string) string {
	if pkg == nil {
		return ""
	}
	if pkg.TypesInfo != nil {
		switch e := expr.(type) {
		case *ast.Ident:
			if obj := pkg.TypesInfo.Uses[e]; obj != nil && obj.Pkg() != nil {
				return grpcRegisterFuncKey(obj.Pkg().Path(), funcName)
			}
		case *ast.SelectorExpr:
			if e.Sel != nil {
				if obj := pkg.TypesInfo.Uses[e.Sel]; obj != nil && obj.Pkg() != nil {
					return grpcRegisterFuncKey(obj.Pkg().Path(), funcName)
				}
			}
		}
	}
	if _, ok := expr.(*ast.Ident); ok {
		return grpcRegisterFuncKey(pkg.PkgPath, funcName)
	}
	return ""
}

func selectorName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if e.Sel == nil {
			return ""
		}
		return e.Sel.Name
	}
	return ""
}

func grpcDescriptorArgName(expr ast.Expr, fset *token.FileSet) string {
	switch e := expr.(type) {
	case *ast.UnaryExpr:
		return grpcDescriptorArgName(e.X, fset)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		if e.Sel != nil {
			return e.Sel.Name
		}
	}
	return grpcExprString(fset, expr)
}

func identName(expr ast.Expr) string {
	ident, ok := expr.(*ast.Ident)
	if !ok || ident == nil {
		return ""
	}
	return ident.Name
}

func stringLiteral(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return strings.Trim(lit.Value, `"`)
	}
	return value
}

func boolLiteral(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "true"
}

func grpcExprString(fset *token.FileSet, expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var b strings.Builder
	_ = printer.Fprint(&b, fset, expr)
	return collapseWhitespace(b.String())
}

func grpcServiceShortName(service string) string {
	if service == "" {
		return ""
	}
	parts := strings.Split(service, ".")
	return parts[len(parts)-1]
}

func grpcRPCType(serverStreams, clientStreams bool) string {
	switch {
	case serverStreams && clientStreams:
		return GRPCRPCBidiStream
	case serverStreams:
		return GRPCRPCServerStream
	case clientStreams:
		return GRPCRPCClientStream
	default:
		return GRPCRPCUnary
	}
}

func appendUniqueString(items []string, item string) []string {
	if item == "" {
		return items
	}
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

func sortGRPCEndpoints(endpoints []GRPCEndpoint) {
	sort.Slice(endpoints, func(i, j int) bool {
		a, b := endpoints[i], endpoints[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		if a.RPCType != b.RPCType {
			return a.RPCType < b.RPCType
		}
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

func sortGRPCRegistrations(registrations []GRPCRegistration) {
	sort.Slice(registrations, func(i, j int) bool {
		a, b := registrations[i], registrations[j]
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.RegisterFunc != b.RegisterFunc {
			return a.RegisterFunc < b.RegisterFunc
		}
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Line < b.Line
	})
}

func grpcEndpointKey(endpoint GRPCEndpoint) string {
	return endpoint.Service + "|" + endpoint.Method + "|" + endpoint.RPCType + "|" + endpoint.Package + "|" + endpoint.File + ":" + fmt.Sprintf("%d", endpoint.Line)
}

func grpcRegistrationKey(registration GRPCRegistration) string {
	return registration.Service + "|" + registration.ServiceShortName + "|" + registration.RegisterFunc + "|" + registration.Package + "|" + registration.File + ":" + fmt.Sprintf("%d", registration.Line)
}

func grpcResultItemKey(item grpcResultItem) string {
	if item.Kind == "registration" {
		return "registration|" + grpcRegistrationKey(item.Registration)
	}
	return "endpoint|" + grpcEndpointKey(item.Endpoint)
}
