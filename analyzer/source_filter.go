package analyzer

import (
	"path/filepath"
	"strings"
)

type SourceFilter struct {
	includePatterns []string
	excludePatterns []string
}

func NewSourceFilter(include, exclude []string) *SourceFilter {
	return &SourceFilter{
		includePatterns: cleanStringSlice(include),
		excludePatterns: cleanStringSlice(exclude),
	}
}

func (f *SourceFilter) ShouldProcess(path string) bool {
	if f == nil {
		return true
	}
	normalized := strings.ReplaceAll(path, "\\", "/")
	base := filepath.Base(normalized)
	if f.isExcluded(normalized, base) && !f.isIncluded(normalized, base) {
		return false
	}
	return true
}

func (f *SourceFilter) IsTestFile(path string) bool {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(path, "\\", "/")))
	return strings.HasSuffix(base, "_test.go")
}

func (f *SourceFilter) IsVendorFile(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	return strings.Contains(normalized, "/vendor/") || strings.HasPrefix(normalized, "vendor/")
}

func (f *SourceFilter) IsGeneratedFile(path string) bool {
	normalized := strings.ReplaceAll(path, "\\", "/")
	base := filepath.Base(normalized)
	if strings.HasSuffix(base, ".pb.go") || strings.HasSuffix(base, "_gen.go") || strings.HasSuffix(base, ".gen.go") {
		return true
	}
	return strings.Contains(strings.ToLower(normalized), "code generated")
}

func (f *SourceFilter) isIncluded(path, base string) bool {
	for _, pattern := range f.includePatterns {
		if pathMatchesPattern(path, base, pattern) {
			return true
		}
	}
	return false
}

func (f *SourceFilter) isExcluded(path, base string) bool {
	if f.IsVendorFile(path) {
		for _, pattern := range f.excludePatterns {
			if strings.Contains(pattern, "vendor") {
				return true
			}
		}
	}
	if f.IsTestFile(path) {
		for _, pattern := range f.excludePatterns {
			if strings.Contains(pattern, "_test.go") {
				return true
			}
		}
	}
	if f.IsGeneratedFile(path) {
		for _, pattern := range f.excludePatterns {
			if strings.Contains(pattern, ".pb.go") || strings.Contains(pattern, "_gen.go") || strings.Contains(pattern, ".gen.go") {
				return true
			}
		}
	}
	for _, pattern := range f.excludePatterns {
		if pathMatchesPattern(path, base, pattern) {
			return true
		}
	}
	return false
}

func pathMatchesPattern(path, base, pattern string) bool {
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if pattern == "" {
		return false
	}
	path = strings.ReplaceAll(path, "\\", "/")
	base = strings.ReplaceAll(base, "\\", "/")
	switch {
	case strings.HasSuffix(pattern, "/"):
		return strings.Contains(path, pattern) || strings.HasPrefix(path, strings.TrimSuffix(pattern, "/"))
	case strings.Contains(pattern, "*"):
		ok, err := filepath.Match(pattern, base)
		if err == nil && ok {
			return true
		}
		ok, err = filepath.Match(pattern, path)
		return err == nil && ok
	default:
		return strings.Contains(path, pattern) || base == pattern || path == pattern
	}
}
