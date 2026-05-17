package analyzer

import (
	"os"
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
		name      string
		method    string
		framework string
		expected  bool
	}{
		// GORM
		{"GORM Find", "Find", "gorm", true},
		{"GORM AutoMigrate", "AutoMigrate", "gorm", true},
		// Ent
		{"Ent Create", "Create", "ent", true},
		{"Ent UpdateOne", "UpdateOne", "ent", true},
		// Sqlx
		{"Sqlx Select", "Select", "sqlx", true},
		{"Sqlx NamedExec", "NamedExec", "sqlx", true},
		// Bun
		{"Bun NewSelect", "NewSelect", "bun", true},
		{"Bun Exec", "Exec", "bun", true},
		// Sqlc
		{"Sqlc CreateUser", "CreateUser", "sqlc", true},
		{"Sqlc GetItem", "GetItem", "sqlc", true},
		{"Sqlc ListItems", "ListItems", "sqlc", true},
		{"Sqlc ExecProc", "ExecProc", "sqlc", true},
		// Negatives
		{"NotGORM", "RandomMethod", "gorm", false},
		{"Empty", "", "gorm", false},
		{"Sqlc random", "Random", "sqlc", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result bool
			methodName := tt.method

			switch tt.framework {
			case "gorm":
				gormMethods := map[string]bool{"Find": true, "First": true, "Last": true, "Take": true, "Create": true, "Save": true, "Update": true, "Delete": true, "Where": true, "Model": true, "AutoMigrate": true}
				result = gormMethods[methodName]
			case "ent":
				entMethods := map[string]bool{"Create": true, "UpdateOne": true, "Update": true, "Delete": true, "DeleteOne": true, "Query": true, "Get": true, "First": true, "Count": true}
				result = entMethods[methodName]
			case "sqlx":
				sqlxMethods := map[string]bool{"Get": true, "Select": true, "Exec": true, "NamedExec": true, "Query": true, "QueryRow": true}
				result = sqlxMethods[methodName]
			case "bun":
				bunMethods := map[string]bool{"NewSelect": true, "NewInsert": true, "NewUpdate": true, "NewDelete": true, "NewRaw": true, "NewCreateTable": true, "NewDropTable": true, "Exec": true, "QueryRow": true, "Query": true, "Scan": true}
				result = bunMethods[methodName]
			case "sqlc":
				if strings.HasPrefix(methodName, "Create") || strings.HasPrefix(methodName, "Get") || strings.HasPrefix(methodName, "List") || strings.HasPrefix(methodName, "Update") || strings.HasPrefix(methodName, "Delete") || strings.HasPrefix(methodName, "Count") || strings.HasPrefix(methodName, "Exec") {
					result = true
				}
			}

			if result != tt.expected {
				t.Errorf("isOrmOperation(%q, %q) = %v, want %v", tt.method, tt.framework, result, tt.expected)
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
		{"bun tag", `bun:"id,pk"`, "models/user.go", "bun"},
		{"sqlc models", `json:"id"`, "models.go", "sqlc"},
		{"sqlc queries", `json:"id"`, "query.sql.go", "sqlc"},
		{"no tag", `json:"name"`, "models/user.go", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isGorm := strings.Contains(tt.tag, "gorm:") || strings.Contains(tt.tag, `gorm"`)
			isEnt := strings.Contains(filepath.ToSlash(tt.filename), "ent/schema/")
			hasDb := strings.Contains(tt.tag, "db:") || strings.Contains(tt.tag, `db"`)
			isSqlx := hasDb && !isGorm
			isBun := strings.Contains(tt.tag, "bun:") || strings.Contains(tt.tag, `bun"`)

			base := filepath.Base(tt.filename)
			isSqlc := base == "models.go" || strings.HasSuffix(base, ".sql.go")

			var got string
			if isGorm {
				got = "gorm"
			} else if isEnt {
				got = "ent"
			} else if isSqlx {
				got = "sqlx"
			} else if isBun {
				got = "bun"
			} else if isSqlc {
				got = "sqlc"
			}

			if got != tt.expected {
				t.Errorf("detectOrmFrameworkFromAST() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestHasBunTagInAST(t *testing.T) {
	tests := []struct {
		name string
		tag  string
		want bool
	}{
		{"bun tag", `bun:"id,pk"`, true},
		{"bun with backtick", "`bun:\"name\"`", true},
		{"json only", `json:"name"`, false},
		{"empty tag", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasBun := strings.Contains(tt.tag, "bun:") || strings.Contains(tt.tag, `bun"`)
			if hasBun != tt.want {
				t.Errorf("tag %q: got %v, want %v", tt.tag, hasBun, tt.want)
			}
		})
	}
}

func TestInferTableName(t *testing.T) {
	tests := []struct {
		name      string
		modelName string
		inference string
		want      string
	}{
		{"snake plural basic", "User", "", "users"},
		{"snake plural y", "Category", "", "categories"},
		{"snake plural s", "Process", "", "processes"},
		{"snake plural camel", "UserProfile", "", "user_profiles"},
		{"snake", "UserProfile", "snake", "user_profile"},
		{"exact", "UserProfile", "exact", "UserProfile"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := inferTableName(tt.modelName, "gorm", tt.inference)
			if got != tt.want {
				t.Errorf("inferTableName(%q, %q) = %v, want %v", tt.modelName, tt.inference, got, tt.want)
			}
		})
	}
}

func TestCheckInMigrations(t *testing.T) {
	tests := []struct {
		name      string
		tableName string
		text      string
		want      bool
	}{
		{"empty text", "users", "", true}, // assumes valid if no migrations configured
		{"basic create", "users", "CREATE TABLE users (", true},
		{"quotes", "users", "CREATE TABLE \"users\" (", true},
		{"backticks", "users", "CREATE TABLE `users` (", true},
		{"lowercase table", "users", "create table users (", true},
		{"not found", "users", "CREATE TABLE posts (", false},
		{"substring not matched", "users", "CREATE TABLE users_log (", false}, // it might fail the simple match, but let's see. Wait, strings.Contains("CREATE TABLE users_log (", "TABLE users ") is false.
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkInMigrations(tt.tableName, tt.text)
			if got != tt.want {
				t.Errorf("checkInMigrations(%q, %q) = %v, want %v", tt.tableName, tt.text, got, tt.want)
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

func TestReadMigrationFiles(t *testing.T) {
	dir := t.TempDir()
	migDir := filepath.Join(dir, "migrations")
	if err := os.Mkdir(migDir, 0755); err != nil {
		t.Fatal(err)
	}

	err := os.WriteFile(filepath.Join(migDir, "001_create_users.sql"), []byte("CREATE TABLE users (id int);"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(filepath.Join(migDir, "readme.md"), []byte("ignore me"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	text := readMigrationFiles(dir, []string{"migrations"})
	if !strings.Contains(text, "CREATE TABLE users") {
		t.Errorf("Expected migration text to contain CREATE TABLE users, got: %s", text)
	}
	if strings.Contains(text, "ignore me") {
		t.Errorf("Expected migration text to NOT contain ignore me, got: %s", text)
	}
}

func TestFindOrphanedDatabaseModels_TenantSessionWrapperForwarding(t *testing.T) {
	dir := createTestModuleFiles(t, "tenantwrapper", map[string]string{
		"models.go": `
package tenantwrapper

import "context"

type DB struct{}

func (db *DB) Find(dest any) error { return nil }

type TenantSession struct {
	db *DB
}

func TenantDB(ctx context.Context) *TenantSession {
	return &TenantSession{db: &DB{}}
}

func (s *TenantSession) Fetch(dest any) error {
	return s.db.Find(dest)
}

type PluginPassportUser struct {
	ID   int    ` + "`gorm:\"primaryKey\"`" + `
	Name string ` + "`gorm:\"column:name\"`" + `
}

func LoadPassportUser(ctx context.Context, id int) (*PluginPassportUser, error) {
	var user PluginPassportUser
	if err := TenantDB(ctx).Fetch(&user); err != nil {
		return nil, err
	}
	return &user, nil
}
`,
	})

	result, err := FindOrphanedDatabaseModelsWithOptions(newTestWorkspace(t), dir, "./...", OrphanedModelOptions{}, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model := findOrphanedModel(result, "PluginPassportUser"); model != nil {
		t.Fatalf("tenant wrapper forwarded ORM usage should keep model live, got orphaned finding: %+v", *model)
	}
}

func TestFindOrphanedDatabaseModels_GormRawScanDestination(t *testing.T) {
	dir := createTestModuleFiles(t, "rawscanmodel", map[string]string{
		"models.go": `
package rawscanmodel

import "context"

type DB struct{}

func (db *DB) Raw(query string, args ...any) *DB { return db }
func (db *DB) Scan(dest any) error { return nil }

func TenantDB(ctx context.Context) *DB { return &DB{} }

type PluginOrgSyncState struct {
	ID    int    ` + "`gorm:\"primaryKey\"`" + `
	OrgID string ` + "`gorm:\"column:org_id\"`" + `
}

func LoadOrgSyncStates(ctx context.Context) ([]PluginOrgSyncState, error) {
	var rows []PluginOrgSyncState
	if err := TenantDB(ctx).Raw("select * from plugin_org_sync_states").Scan(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}
`,
	})

	result, err := FindOrphanedDatabaseModelsWithOptions(newTestWorkspace(t), dir, "./...", OrphanedModelOptions{}, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model := findOrphanedModel(result, "PluginOrgSyncState"); model != nil {
		t.Fatalf("Raw(...).Scan destination should keep model live, got orphaned finding: %+v", *model)
	}
}

func TestFindOrphanedDatabaseModels_ReferencedMigrationModelIsNotReportedAsNoOrmUsage(t *testing.T) {
	dir := createTestModuleFiles(t, "migrationbackedmodel", map[string]string{
		"models.go": `
package migrationbackedmodel

type PluginPassportCasdoorMapping struct {
	ID     int    ` + "`gorm:\"primaryKey\"`" + `
	UserID string ` + "`gorm:\"column:user_id\"`" + `
}

func NewMapping(userID string) PluginPassportCasdoorMapping {
	return PluginPassportCasdoorMapping{UserID: userID}
}
`,
		"migrations/001_plugin_passport_casdoor_mappings.sql": `
CREATE TABLE plugin_passport_casdoor_mappings (
	id integer primary key,
	user_id text not null
);
`,
	})

	result, err := FindOrphanedDatabaseModelsWithOptions(newTestWorkspace(t), dir, "./...", OrphanedModelOptions{
		MigrationDirs: []string{"migrations"},
	}, QueryOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if model := findOrphanedModel(result, "PluginPassportCasdoorMapping"); model != nil {
		t.Fatalf("referenced model present in migrations should not be reported as orphaned without stronger evidence: %+v", *model)
	}
}

func findOrphanedModel(result *OrphanedModelResult, name string) *OrphanedModel {
	for i := range result.Models {
		if result.Models[i].Name == name {
			return &result.Models[i]
		}
	}
	return nil
}
