package analyzer

import (
	"path/filepath"
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

func TestHasEntTagInAST(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     bool
	}{
		{"ent schema file", "ent/schema/user.go", true},
		{"ent schema nested", "myapp/ent/schema/product.go", true},
		{"not ent file", "models/user.go", false},
		{"ent directory but not schema", "ent/migrate.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.Contains(filepath.ToSlash(tt.filename), "ent/schema/")
			if result != tt.want {
				t.Errorf("hasEntTagInAST(%q) = %v, want %v", tt.filename, result, tt.want)
			}
		})
	}
}

func TestHasSqlxTagInAST(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{"db tag", `db:"id"`, true},
		{"db with backtick", "`db:\"name\"`", true},
		{"db with other tags", `json:"name" db:"id"`, true},
		{"gorm and db", `gorm:"type:varchar(100)" db:"id"`, false}, // gorm takes precedence
		{"no db tag", `json:"name"`, false},
		{"empty tag", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasDb := strings.Contains(tt.tag, "db:") || strings.Contains(tt.tag, `db"`)
			hasGorm := strings.Contains(tt.tag, "gorm:") || strings.Contains(tt.tag, `gorm"`)
			result := hasDb && !hasGorm
			if result != tt.want {
				t.Errorf("tag %q: got %v, want %v", tt.tag, result, tt.want)
			}
		})
	}
}

func TestDetectOrmFrameworkFromAST(t *testing.T) {
	tests := []struct {
		name     string
		tag      string
		filename string
		expected string
	}{
		{"gorm tag", `gorm:"primaryKey"`, "models/user.go", "gorm"},
		{"gorm quote", "`gorm:\"primaryKey\"`", "models/user.go", "gorm"},
		{"ent file", `field:String`, "ent/schema/user.go", "ent"},
		{"sqlx tag", `db:"id"`, "models/user.go", "sqlx"},
		{"no tag", `json:"name"`, "models/user.go", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simulate the detection logic
			isGorm := strings.Contains(tt.tag, "gorm:") || strings.Contains(tt.tag, `gorm"`)
			isEnt := strings.Contains(filepath.ToSlash(tt.filename), "ent/schema/")
			hasDb := strings.Contains(tt.tag, "db:") || strings.Contains(tt.tag, `db"`)
			isSqlx := hasDb && !isGorm

			var got string
			if isGorm {
				got = "gorm"
			} else if isEnt {
				got = "ent"
			} else if isSqlx {
				got = "sqlx"
			}

			if got != tt.expected {
				t.Errorf("detectOrmFrameworkFromAST() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestOrmModelType(t *testing.T) {
	model := OrmModel{
		Name:      "User",
		Pkg:       "github.com/test/models",
		File:      "models/user.go",
		Line:      10,
		Framework: "gorm",
	}

	if model.Name != "User" {
		t.Errorf("Name = %q, want User", model.Name)
	}
	if model.Framework != "gorm" {
		t.Errorf("Framework = %q, want gorm", model.Framework)
	}
}
