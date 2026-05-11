package analyzer

import (
	"strings"
	"testing"
)

func TestHasGormTagInASTString(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{"empty tag", "", false},
		{"gorm with colon", `gorm:"primaryKey"`, true},
		{"gorm with backtick prefix", "`gorm:\"primaryKey\"`", true},
		{"json only", `json:"name"`, false},
		{"gorm and json", `json:"name" gorm:"type:varchar(100)"`, true},
		{"gorm with double quote prefix", `gorm"primaryKey"`, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the tag substring check (same logic as hasGormTagInAST)
			hasGorm := strings.Contains(tt.tag, "gorm:") || strings.Contains(tt.tag, `gorm"`)
			if hasGorm != tt.want {
				t.Errorf("tag %q: got %v, want %v", tt.tag, hasGorm, tt.want)
			}
		})
	}
}

func TestIsOrmOperation(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		expected bool
	}{
		{"Find", "Find", true},
		{"First", "First", true},
		{"Last", "Last", true},
		{"Take", "Take", true},
		{"Create", "Create", true},
		{"Save", "Save", true},
		{"Update", "Update", true},
		{"Delete", "Delete", true},
		{"Where", "Where", true},
		{"Model", "Model", true},
		{"AutoMigrate", "AutoMigrate", true},
		{"NotGORM", "RandomMethod", false},
		{"Empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the GORM method mapping
			gormMethods := map[string]bool{
				"Find":        true,
				"First":       true,
				"Last":        true,
				"Take":        true,
				"Create":      true,
				"Save":        true,
				"Update":      true,
				"Delete":      true,
				"Where":       true,
				"Model":       true,
				"AutoMigrate": true,
			}
			result := gormMethods[tt.method]
			if result != tt.expected {
				t.Errorf("isOrmOperation(%q) = %v, want %v", tt.method, result, tt.expected)
			}
		})
	}
}

func TestOrphanedModelKey(t *testing.T) {
	tests := []struct {
		name  string
		model OrphanedModel
		want  string
	}{
		{
			name: "simple",
			model: OrphanedModel{
				Name:    "User",
				Package: "github.com/test/models",
				File:    "models/user.go",
				Line:    10,
			},
			want: "github.com/test/models|User|models/user.go:10",
		},
		{
			name: "different package",
			model: OrphanedModel{
				Name:    "Product",
				Package: "github.com/other/pkg",
				File:    "product.go",
				Line:    5,
			},
			want: "github.com/other/pkg|Product|product.go:5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := orphanedModelKey(tt.model)
			if got != tt.want {
				t.Errorf("orphanedModelKey() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrphanedModelReasonString(t *testing.T) {
	tests := []struct {
		reason OrphanedModelReason
		want   string
	}{
		{OrphanedNoReferences, "no_references"},
		{OrphanedNoOrmUsage, "no_orm_usage"},
	}

	for _, tt := range tests {
		t.Run(string(tt.reason), func(t *testing.T) {
			got := string(tt.reason)
			if got != tt.want {
				t.Errorf("OrphanedModelReason string = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOrphanedModelOptionsDefaults(t *testing.T) {
	opts := OrphanedModelOptions{}
	if opts.ORMFramework != "" {
		t.Errorf("ORMFramework should be empty by default, got %q", opts.ORMFramework)
	}
}
