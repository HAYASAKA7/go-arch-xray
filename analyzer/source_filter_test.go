package analyzer

import "testing"

func TestSourceFilter_DefaultPatterns(t *testing.T) {
	filter := NewSourceFilter([]string{"cmd/"}, []string{"vendor/", "*_test.go", "*.pb.go"})

	if !filter.ShouldProcess("cmd/main.go") {
		t.Fatal("expected cmd/main.go to be included")
	}
	if filter.ShouldProcess("vendor/pkg/x.go") {
		t.Fatal("expected vendor file to be excluded")
	}
	if filter.ShouldProcess("pkg/foo_test.go") {
		t.Fatal("expected test file to be excluded")
	}
	if filter.ShouldProcess("pkg/service.pb.go") {
		t.Fatal("expected generated protobuf file to be excluded")
	}
}

func TestSourceFilter_RecognizesGeneratedAndVendorFiles(t *testing.T) {
	filter := NewSourceFilter(nil, nil)
	if !filter.IsVendorFile("vendor/x.go") {
		t.Fatal("expected vendor detection")
	}
	if !filter.IsTestFile("pkg/x_test.go") {
		t.Fatal("expected test detection")
	}
	if !filter.IsGeneratedFile("pkg/x_gen.go") {
		t.Fatal("expected generated detection")
	}
}
