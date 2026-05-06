package analyzer

import (
	"strings"
	"testing"
)

func TestListGRPCEndpoints_GeneratedDescriptorsAndRegistrations(t *testing.T) {
	dir := createDependencyTestModule(t, "grpcep", map[string]string{
		"grpc/grpc.go": `package grpc

type ServiceRegistrar interface { RegisterService(*ServiceDesc, any) }

type ServiceDesc struct {
	ServiceName string
	HandlerType any
	Methods []MethodDesc
	Streams []StreamDesc
	Metadata any
}

type MethodDesc struct {
	MethodName string
	Handler any
}

type StreamDesc struct {
	StreamName string
	Handler any
	ServerStreams bool
	ClientStreams bool
}
`,
		"pb/greeter.pb.go": `package pb

import "grpcep/grpc"

type GreeterServer interface{}

func _Greeter_SayHello_Handler() {}
func _Greeter_Watch_Handler() {}
func _Greeter_Chat_Handler() {}

func RegisterGreeterServer(s grpc.ServiceRegistrar, srv GreeterServer) {
	s.RegisterService(&Greeter_ServiceDesc, srv)
}

var Greeter_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "acme.greeter.v1.Greeter",
	HandlerType: (*GreeterServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "SayHello", Handler: _Greeter_SayHello_Handler},
	},
	Streams: []grpc.StreamDesc{
		{StreamName: "Watch", Handler: _Greeter_Watch_Handler, ServerStreams: true},
		{StreamName: "Chat", Handler: _Greeter_Chat_Handler, ServerStreams: true, ClientStreams: true},
	},
	Metadata: "greeter.proto",
}
`,
		"main.go": `package main

import "grpcep/pb"

type greeterServer struct{}

func main() {
	pb.RegisterGreeterServer(nil, &greeterServer{})
}
`,
	})

	ws := NewWorkspace()
	result, err := ListGRPCEndpoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 3 {
		t.Fatalf("expected 3 gRPC endpoints, got %d: %+v", result.Total, result.Endpoints)
	}
	if result.TotalRegistrations != 1 {
		t.Fatalf("expected one registration, got %d: %+v", result.TotalRegistrations, result.Registrations)
	}

	byMethod := map[string]GRPCEndpoint{}
	for _, endpoint := range result.Endpoints {
		byMethod[endpoint.Method] = endpoint
		if endpoint.Service != "acme.greeter.v1.Greeter" {
			t.Fatalf("unexpected service: %+v", endpoint)
		}
		if endpoint.ServiceShortName != "Greeter" {
			t.Fatalf("unexpected service short name: %+v", endpoint)
		}
		if endpoint.ProtoFile != "greeter.proto" {
			t.Fatalf("unexpected proto metadata: %+v", endpoint)
		}
		if !endpoint.Registered {
			t.Fatalf("expected endpoint to be marked registered: %+v", endpoint)
		}
		if len(endpoint.Implementations) != 1 || endpoint.Implementations[0] != "&greeterServer{}" {
			t.Fatalf("unexpected implementations: %+v", endpoint)
		}
		if endpoint.File == "" || endpoint.Line == 0 || endpoint.Anchor == "" {
			t.Fatalf("expected source location and anchor: %+v", endpoint)
		}
	}

	if byMethod["SayHello"].RPCType != GRPCRPCUnary {
		t.Fatalf("expected SayHello unary, got %+v", byMethod["SayHello"])
	}
	if byMethod["Watch"].RPCType != GRPCRPCServerStream {
		t.Fatalf("expected Watch server_stream, got %+v", byMethod["Watch"])
	}
	if byMethod["Chat"].RPCType != GRPCRPCBidiStream {
		t.Fatalf("expected Chat bidi_stream, got %+v", byMethod["Chat"])
	}
	if byMethod["SayHello"].FullMethod != "/acme.greeter.v1.Greeter/SayHello" {
		t.Fatalf("unexpected full method: %+v", byMethod["SayHello"])
	}
}

func TestListGRPCEndpoints_LowercaseGeneratedServiceDesc(t *testing.T) {
	dir := createDependencyTestModule(t, "grpclower", map[string]string{
		"grpc/grpc.go": `package grpc

type ServiceDesc struct { ServiceName string; Methods []MethodDesc; Metadata any }
type MethodDesc struct { MethodName string; Handler any }
`,
		"pb/greeter.pb.go": `package pb

import "grpclower/grpc"

func _Greeter_Ping_Handler() {}

var _Greeter_serviceDesc = grpc.ServiceDesc{
	ServiceName: "legacy.Greeter",
	Methods: []grpc.MethodDesc{{MethodName: "Ping", Handler: _Greeter_Ping_Handler}},
	Metadata: "legacy.proto",
}
`,
	})

	ws := NewWorkspace()
	result, err := ListGRPCEndpoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Total != 1 {
		t.Fatalf("expected one endpoint, got %+v", result)
	}
	endpoint := result.Endpoints[0]
	if endpoint.ServiceDesc != "_Greeter_serviceDesc" || endpoint.Method != "Ping" || endpoint.RPCType != GRPCRPCUnary {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
}

func TestListGRPCEndpoints_DuplicateDescriptorNamesAcrossPackages(t *testing.T) {
	dir := createDependencyTestModule(t, "grpcdups", map[string]string{
		"grpc/grpc.go": `package grpc

type ServiceRegistrar interface { RegisterService(*ServiceDesc, any) }
type ServiceDesc struct { ServiceName string; Methods []MethodDesc }
type MethodDesc struct { MethodName string; Handler any }
`,
		"alpha/greeter.pb.go": `package alpha

import "grpcdups/grpc"

func h() {}

func RegisterGreeterServer(s grpc.ServiceRegistrar, srv any) {
	s.RegisterService(&Greeter_ServiceDesc, srv)
}

var Greeter_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "alpha.Greeter",
	Methods: []grpc.MethodDesc{{MethodName: "Ping", Handler: h}},
}
`,
		"beta/greeter.pb.go": `package beta

import "grpcdups/grpc"

func h() {}

func RegisterGreeterServer(s grpc.ServiceRegistrar, srv any) {
	s.RegisterService(&Greeter_ServiceDesc, srv)
}

var Greeter_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "beta.Greeter",
	Methods: []grpc.MethodDesc{{MethodName: "Ping", Handler: h}},
}
`,
		"main.go": `package main

import (
	"grpcdups/alpha"
	"grpcdups/beta"
)

type alphaServer struct{}
type betaServer struct{}

func main() {
	alpha.RegisterGreeterServer(nil, &alphaServer{})
	beta.RegisterGreeterServer(nil, &betaServer{})
}
`,
	})

	ws := NewWorkspace()
	result, err := ListGRPCEndpoints(ws, dir, "./...")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Total != 2 || result.TotalRegistrations != 2 {
		t.Fatalf("expected two endpoints and registrations, got %+v", result)
	}
	implementationsByService := map[string][]string{}
	for _, endpoint := range result.Endpoints {
		implementationsByService[endpoint.Service] = endpoint.Implementations
	}
	if got := implementationsByService["alpha.Greeter"]; len(got) != 1 || got[0] != "&alphaServer{}" {
		t.Fatalf("alpha registration matched incorrectly: %+v", result.Endpoints)
	}
	if got := implementationsByService["beta.Greeter"]; len(got) != 1 || got[0] != "&betaServer{}" {
		t.Fatalf("beta registration matched incorrectly: %+v", result.Endpoints)
	}
}

func TestListGRPCEndpoints_Streaming(t *testing.T) {
	dir := createDependencyTestModule(t, "grpcstream", map[string]string{
		"grpc/grpc.go": `package grpc

type ServiceRegistrar interface { RegisterService(*ServiceDesc, any) }
type ServiceDesc struct { ServiceName string; Methods []MethodDesc }
type MethodDesc struct { MethodName string; Handler any }
`,
		"pb/greeter.pb.go": `package pb

import "grpcstream/grpc"

func h() {}

func RegisterGreeterServer(s grpc.ServiceRegistrar, srv any) {
	s.RegisterService(&Greeter_ServiceDesc, srv)
}

var Greeter_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "stream.Greeter",
	Methods: []grpc.MethodDesc{
		{MethodName: "A", Handler: h},
		{MethodName: "B", Handler: h},
		{MethodName: "C", Handler: h},
	},
}
`,
		"main.go": `package main

import "grpcstream/pb"

type server struct{}

func main() {
	pb.RegisterGreeterServer(nil, &server{})
}
`,
	})

	ws := NewWorkspace()
	collected := streamCollect(t, func(cursor string) (any, []string, string, bool) {
		result, err := ListGRPCEndpointsWithOptions(ws, dir, "./...", QueryOptions{ChunkSize: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("grpc stream: %v", err)
		}
		items := make([]string, 0, len(result.Endpoints)+len(result.Registrations))
		for _, endpoint := range result.Endpoints {
			items = append(items, "endpoint:"+endpoint.Method)
		}
		for _, registration := range result.Registrations {
			items = append(items, "registration:"+registration.RegisterFunc)
		}
		return result, items, result.NextCursor, result.HasMore
	})
	if len(collected) != 4 {
		t.Fatalf("expected 3 endpoints and 1 registration across stream, got %d: %+v", len(collected), collected)
	}

	if _, err := ListGRPCEndpointsWithOptions(ws, dir, "./...", QueryOptions{ChunkSize: 1, Cursor: "garbage"}); err == nil || !strings.Contains(err.Error(), "stream cursor") {
		t.Fatalf("expected stream cursor error, got: %v", err)
	}
}
