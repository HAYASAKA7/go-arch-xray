package analyzer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
	"gopkg.in/yaml.v3"
)

const WorkspaceConfigFile = ".go-arch-xray.yml"

const userConfigEnv = "GO_ARCH_XRAY_USER_CONFIG"

type WorkspaceConfig struct {
	Version         int              `json:"version" yaml:"version,omitempty"`
	Workspace       ConfigWorkspace  `json:"workspace" yaml:"workspace,omitempty"`
	Modules         []ConfigModule   `json:"modules,omitempty" yaml:"modules,omitempty"`
	PackagePatterns []string         `json:"package_patterns,omitempty" yaml:"package_patterns,omitempty"`
	Sources         ConfigSources    `json:"sources,omitempty" yaml:"sources,omitempty"`
	CacheCapacity   int              `json:"cache_capacity,omitempty" yaml:"cache_capacity,omitempty"`
	Output          ConfigOutput     `json:"output,omitempty" yaml:"output,omitempty"`
	Boundaries      []BoundaryRule   `json:"boundaries,omitempty" yaml:"boundaries,omitempty"`
	Complexity      ConfigComplexity `json:"complexity,omitempty" yaml:"complexity,omitempty"`
	Lifecycle       ConfigLifecycle  `json:"lifecycle,omitempty" yaml:"lifecycle,omitempty"`
	ORM             ConfigORM        `json:"orm,omitempty" yaml:"orm,omitempty"`
	Sync            ConfigSync       `json:"sync,omitempty" yaml:"sync,omitempty"`
	Embeddings      ConfigEmbeddings `json:"embeddings,omitempty" yaml:"embeddings,omitempty"`
}

type ConfigSync struct {
	Debounce      ConfigDuration `json:"debounce,omitempty" yaml:"debounce,omitempty"`
	CheckInterval ConfigDuration `json:"check_interval,omitempty" yaml:"check_interval,omitempty"`
	AutoRebuild   *bool          `json:"auto_rebuild,omitempty" yaml:"auto_rebuild,omitempty"`
}

type ConfigEmbeddings struct {
	Provider  string                  `json:"provider,omitempty" yaml:"provider,omitempty"`
	Local     ConfigEmbeddingEndpoint `json:"local,omitempty" yaml:"local,omitempty"`
	API       ConfigEmbeddingAPI      `json:"api,omitempty" yaml:"api,omitempty"`
	BatchSize int                     `json:"batch_size,omitempty" yaml:"batch_size,omitempty"`
	ChunkSize int                     `json:"chunk_size,omitempty" yaml:"chunk_size,omitempty"`
	Dimension int                     `json:"dimension,omitempty" yaml:"dimension,omitempty"`
}

type ConfigEmbeddingEndpoint struct {
	Endpoint string         `json:"endpoint,omitempty" yaml:"endpoint,omitempty"`
	Model    string         `json:"model,omitempty" yaml:"model,omitempty"`
	Timeout  ConfigDuration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type ConfigEmbeddingAPI struct {
	BaseURL   string         `json:"base_url,omitempty" yaml:"base_url,omitempty"`
	Model     string         `json:"model,omitempty" yaml:"model,omitempty"`
	APIKeyEnv string         `json:"api_key_env,omitempty" yaml:"api_key_env,omitempty"`
	Timeout   ConfigDuration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

type ConfigORM struct {
	DefaultFramework string   `json:"default_framework,omitempty" yaml:"default_framework,omitempty"`
	MigrationDirs    []string `json:"migration_dirs,omitempty" yaml:"migration_dirs,omitempty"`
	TableInference   string   `json:"table_inference,omitempty" yaml:"table_inference,omitempty"`
}

type ConfigWorkspace struct {
	Mode string `json:"mode,omitempty" yaml:"mode,omitempty"`
	File string `json:"file,omitempty" yaml:"file,omitempty"`
}

type ConfigSources struct {
	Include []string `json:"include,omitempty" yaml:"include,omitempty"`
	Exclude []string `json:"exclude,omitempty" yaml:"exclude,omitempty"`
}

type ConfigModule struct {
	Name            string   `json:"name,omitempty" yaml:"name,omitempty"`
	Root            string   `json:"root" yaml:"root"`
	ModulePath      string   `json:"module_path,omitempty" yaml:"module_path,omitempty"`
	PackagePatterns []string `json:"package_patterns,omitempty" yaml:"package_patterns,omitempty"`
}

type ConfigOutput struct {
	Limit     int  `json:"limit,omitempty" yaml:"limit,omitempty"`
	Offset    int  `json:"offset,omitempty" yaml:"offset,omitempty"`
	MaxItems  int  `json:"max_items,omitempty" yaml:"max_items,omitempty"`
	ChunkSize int  `json:"chunk_size,omitempty" yaml:"chunk_size,omitempty"`
	Summary   bool `json:"summary,omitempty" yaml:"summary,omitempty"`
}

type ConfigComplexity struct {
	MinCyclomatic           int     `json:"min_cyclomatic,omitempty" yaml:"min_cyclomatic,omitempty"`
	MinCognitive            int     `json:"min_cognitive,omitempty" yaml:"min_cognitive,omitempty"`
	MinHalsteadVolume       float64 `json:"min_halstead_volume,omitempty" yaml:"min_halstead_volume,omitempty"`
	MaxMaintainabilityIndex float64 `json:"max_maintainability_index,omitempty" yaml:"max_maintainability_index,omitempty"`
	SortBy                  string  `json:"sort_by,omitempty" yaml:"sort_by,omitempty"`
	IncludePackages         bool    `json:"include_packages,omitempty" yaml:"include_packages,omitempty"`
}

type ConfigLifecycle struct {
	DedupeMode string `json:"dedupe_mode,omitempty" yaml:"dedupe_mode,omitempty"`
	MaxHops    int    `json:"max_hops,omitempty" yaml:"max_hops,omitempty"`
	Summary    bool   `json:"summary,omitempty" yaml:"summary,omitempty"`
}

type WorkspaceConfigInspection struct {
	RootPath            string          `json:"root_path"`
	ConfigPath          string          `json:"config_path"`
	ConfigExists        bool            `json:"config_exists"`
	UserConfigPath      string          `json:"user_config_path,omitempty"`
	UserConfigExists    bool            `json:"user_config_exists"`
	GoWorkPath          string          `json:"go_work_path,omitempty"`
	GoModPath           string          `json:"go_mod_path,omitempty"`
	SuggestedConfig     WorkspaceConfig `json:"suggested_config"`
	EffectiveConfig     WorkspaceConfig `json:"effective_config"`
	RecommendedNextStep string          `json:"recommended_next_step,omitempty"`
	Notes               []string        `json:"notes,omitempty"`
}

type WorkspaceConfigInitResult struct {
	RootPath    string          `json:"root_path"`
	ConfigPath  string          `json:"config_path"`
	Created     bool            `json:"created"`
	Overwritten bool            `json:"overwritten"`
	Config      WorkspaceConfig `json:"config"`
	YAML        string          `json:"yaml"`
	Notes       []string        `json:"notes,omitempty"`
}

func InspectWorkspaceConfig(root string) (*WorkspaceConfigInspection, error) {
	root = filepath.Clean(root)
	suggested, err := SuggestWorkspaceConfig(root)
	if err != nil {
		return nil, err
	}
	effective, repoExists, userExists, err := effectiveWorkspaceConfigWithSources(root, suggested)
	if err != nil {
		return nil, err
	}

	inspection := &WorkspaceConfigInspection{
		RootPath:            root,
		ConfigPath:          ProjectWorkspaceConfigPath(root),
		ConfigExists:        repoExists || pathExists(ProjectWorkspaceConfigPath(root)),
		UserConfigPath:      UserWorkspaceConfigPath(),
		UserConfigExists:    userExists,
		SuggestedConfig:     suggested,
		EffectiveConfig:     effective,
		RecommendedNextStep: configRecommendedNextStep(repoExists),
		Notes:               configNotes(suggested, repoExists, userExists),
	}
	if suggested.Workspace.Mode == "go_work" {
		inspection.GoWorkPath = filepath.Join(root, suggested.Workspace.File)
	}
	if suggested.Workspace.Mode == "go_mod" {
		inspection.GoModPath = filepath.Join(root, suggested.Workspace.File)
	}
	return inspection, nil
}

func SuggestWorkspaceConfig(root string) (WorkspaceConfig, error) {
	root = filepath.Clean(root)
	if pathExists(filepath.Join(root, "go.work")) {
		return suggestGoWorkConfig(root)
	}
	if pathExists(filepath.Join(root, "go.mod")) {
		modulePath, err := readGoModulePath(filepath.Join(root, "go.mod"))
		if err != nil {
			return WorkspaceConfig{}, err
		}
		return normalizeWorkspaceConfig(WorkspaceConfig{
			Version:         1,
			Workspace:       ConfigWorkspace{Mode: "go_mod", File: "go.mod"},
			PackagePatterns: []string{"./..."},
			Modules: []ConfigModule{{
				Name:            configModuleName(".", modulePath),
				Root:            ".",
				ModulePath:      modulePath,
				PackagePatterns: []string{"./..."},
			}},
		}), nil
	}
	return normalizeWorkspaceConfig(WorkspaceConfig{
		Version:         1,
		Workspace:       ConfigWorkspace{Mode: "auto"},
		PackagePatterns: []string{"./..."},
	}), nil
}

func EffectiveWorkspaceConfig(root string) (WorkspaceConfig, error) {
	suggested, err := SuggestWorkspaceConfig(root)
	if err != nil {
		return WorkspaceConfig{}, err
	}
	effective, _, _, err := effectiveWorkspaceConfigWithSources(root, suggested)
	return effective, err
}

func InitWorkspaceConfig(root string, overwrite bool) (*WorkspaceConfigInitResult, error) {
	root = filepath.Clean(root)
	config, err := SuggestWorkspaceConfig(root)
	if err != nil {
		return nil, err
	}
	yamlText, err := MarshalWorkspaceConfig(config)
	if err != nil {
		return nil, err
	}

	path := RepoWorkspaceConfigPath(root)
	existed := pathExists(path)
	flags := os.O_WRONLY | os.O_CREATE
	if overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("workspace config already exists at %s; pass overwrite=true to replace it", path)
		}
		return nil, fmt.Errorf("create workspace config: %w", err)
	}
	defer file.Close()
	if _, err := file.WriteString(yamlText); err != nil {
		return nil, fmt.Errorf("write workspace config: %w", err)
	}

	return &WorkspaceConfigInitResult{
		RootPath:    root,
		ConfigPath:  path,
		Created:     !existed,
		Overwritten: existed && overwrite,
		Config:      config,
		YAML:        yamlText,
		Notes:       []string{"repo config is shared team policy; explicit tool inputs still override these defaults"},
	}, nil
}

func MarshalWorkspaceConfig(config WorkspaceConfig) (string, error) {
	config = normalizeWorkspaceConfig(config)
	data, err := yaml.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("marshal workspace config: %w", err)
	}
	return string(data), nil
}

func RepoWorkspaceConfigPath(root string) string {
	return filepath.Join(filepath.Clean(root), WorkspaceConfigFile)
}

func ProjectWorkspaceConfigPath(root string) string {
	return filepath.Join(WorkspaceLayoutFor(root).GAXPath, "config.yml")
}

func UserWorkspaceConfigPath() string {
	if override, ok := os.LookupEnv(userConfigEnv); ok {
		override = strings.TrimSpace(override)
		if override == "" || strings.EqualFold(override, "off") || strings.EqualFold(override, "none") {
			return ""
		}
		return override
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		return ""
	}
	return filepath.Join(dir, "go-arch-xray", "config.yml")
}

func ConfigPackagePatterns(config WorkspaceConfig) []string {
	config = normalizeWorkspaceConfig(config)
	return append([]string(nil), config.PackagePatterns...)
}

func effectiveWorkspaceConfigWithSources(root string, suggested WorkspaceConfig) (WorkspaceConfig, bool, bool, error) {
	effective := suggested
	userConfig, userExists, err := loadOptionalWorkspaceConfig(UserWorkspaceConfigPath())
	if err != nil {
		return WorkspaceConfig{}, false, false, err
	}
	if userExists {
		effective = mergeWorkspaceConfig(effective, userConfig)
	}
	repoConfig, repoExists, err := loadOptionalWorkspaceConfig(RepoWorkspaceConfigPath(root))
	if err != nil {
		return WorkspaceConfig{}, false, false, err
	}
	if repoExists {
		effective = mergeWorkspaceConfig(effective, repoConfig)
	}
	projectConfig, projectExists, err := loadOptionalWorkspaceConfig(ProjectWorkspaceConfigPath(root))
	if err != nil {
		return WorkspaceConfig{}, false, false, err
	}
	if projectExists {
		effective = mergeWorkspaceConfig(effective, projectConfig)
	}
	return normalizeWorkspaceConfig(effective), repoExists, userExists, nil
}

func loadOptionalWorkspaceConfig(path string) (WorkspaceConfig, bool, error) {
	if path == "" {
		return WorkspaceConfig{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return WorkspaceConfig{}, false, nil
		}
		return WorkspaceConfig{}, false, fmt.Errorf("read workspace config %s: %w", path, err)
	}
	var config WorkspaceConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return WorkspaceConfig{}, false, fmt.Errorf("parse workspace config %s: %w", path, err)
	}
	return normalizeWorkspaceConfig(config), true, nil
}

func mergeWorkspaceConfig(base, overlay WorkspaceConfig) WorkspaceConfig {
	merged := base
	if overlay.Version > 0 {
		merged.Version = overlay.Version
	}
	if overlay.Workspace.Mode != "" || overlay.Workspace.File != "" {
		merged.Workspace = overlay.Workspace
	}
	if len(overlay.Modules) > 0 {
		merged.Modules = append([]ConfigModule(nil), overlay.Modules...)
	}
	if len(overlay.PackagePatterns) > 0 {
		merged.PackagePatterns = append([]string(nil), overlay.PackagePatterns...)
	}
	merged.Sources = mergeConfigSources(merged.Sources, overlay.Sources)
	if overlay.CacheCapacity > 0 {
		merged.CacheCapacity = overlay.CacheCapacity
	}
	merged.Output = mergeConfigOutput(merged.Output, overlay.Output)
	if len(overlay.Boundaries) > 0 {
		merged.Boundaries = append([]BoundaryRule(nil), overlay.Boundaries...)
	}
	merged.Complexity = mergeConfigComplexity(merged.Complexity, overlay.Complexity)
	merged.Lifecycle = mergeConfigLifecycle(merged.Lifecycle, overlay.Lifecycle)
	merged.ORM = mergeConfigORM(merged.ORM, overlay.ORM)
	merged.Sync = mergeConfigSync(merged.Sync, overlay.Sync)
	merged.Embeddings = mergeConfigEmbeddings(merged.Embeddings, overlay.Embeddings)
	return normalizeWorkspaceConfig(merged)
}

func mergeConfigORM(base, overlay ConfigORM) ConfigORM {
	out := base
	if strings.TrimSpace(overlay.DefaultFramework) != "" {
		out.DefaultFramework = strings.TrimSpace(overlay.DefaultFramework)
	}
	if len(overlay.MigrationDirs) > 0 {
		out.MigrationDirs = append([]string(nil), overlay.MigrationDirs...)
	}
	if strings.TrimSpace(overlay.TableInference) != "" {
		out.TableInference = strings.TrimSpace(overlay.TableInference)
	}
	return out
}

func mergeConfigSync(base, overlay ConfigSync) ConfigSync {
	out := base
	if overlay.Debounce.Duration() > 0 {
		out.Debounce = overlay.Debounce
	}
	if overlay.CheckInterval.Duration() > 0 {
		out.CheckInterval = overlay.CheckInterval
	}
	if overlay.AutoRebuild != nil {
		v := *overlay.AutoRebuild
		out.AutoRebuild = &v
	}
	return out
}

func mergeConfigEmbeddings(base, overlay ConfigEmbeddings) ConfigEmbeddings {
	out := base
	if strings.TrimSpace(overlay.Provider) != "" {
		out.Provider = strings.TrimSpace(overlay.Provider)
	}
	if strings.TrimSpace(overlay.Local.Endpoint) != "" {
		out.Local.Endpoint = strings.TrimSpace(overlay.Local.Endpoint)
	}
	if strings.TrimSpace(overlay.Local.Model) != "" {
		out.Local.Model = strings.TrimSpace(overlay.Local.Model)
	}
	if overlay.Local.Timeout.Duration() > 0 {
		out.Local.Timeout = overlay.Local.Timeout
	}
	if strings.TrimSpace(overlay.API.BaseURL) != "" {
		out.API.BaseURL = strings.TrimSpace(overlay.API.BaseURL)
	}
	if strings.TrimSpace(overlay.API.Model) != "" {
		out.API.Model = strings.TrimSpace(overlay.API.Model)
	}
	if strings.TrimSpace(overlay.API.APIKeyEnv) != "" {
		out.API.APIKeyEnv = strings.TrimSpace(overlay.API.APIKeyEnv)
	}
	if overlay.API.Timeout.Duration() > 0 {
		out.API.Timeout = overlay.API.Timeout
	}
	if overlay.BatchSize > 0 {
		out.BatchSize = overlay.BatchSize
	}
	if overlay.ChunkSize > 0 {
		out.ChunkSize = overlay.ChunkSize
	}
	if overlay.Dimension > 0 {
		out.Dimension = overlay.Dimension
	}
	return out
}

func mergeConfigSources(base, overlay ConfigSources) ConfigSources {
	out := base
	if len(overlay.Include) > 0 {
		out.Include = append([]string(nil), overlay.Include...)
	}
	if len(overlay.Exclude) > 0 {
		out.Exclude = append([]string(nil), overlay.Exclude...)
	}
	return out
}

func mergeConfigOutput(base, overlay ConfigOutput) ConfigOutput {
	out := base
	if overlay.Limit > 0 {
		out.Limit = overlay.Limit
	}
	if overlay.Offset > 0 {
		out.Offset = overlay.Offset
	}
	if overlay.MaxItems > 0 {
		out.MaxItems = overlay.MaxItems
	}
	if overlay.ChunkSize > 0 {
		out.ChunkSize = overlay.ChunkSize
	}
	if overlay.Summary {
		out.Summary = true
	}
	return out
}

func mergeConfigComplexity(base, overlay ConfigComplexity) ConfigComplexity {
	out := base
	if overlay.MinCyclomatic > 0 {
		out.MinCyclomatic = overlay.MinCyclomatic
	}
	if overlay.MinCognitive > 0 {
		out.MinCognitive = overlay.MinCognitive
	}
	if overlay.MinHalsteadVolume > 0 {
		out.MinHalsteadVolume = overlay.MinHalsteadVolume
	}
	if overlay.MaxMaintainabilityIndex > 0 {
		out.MaxMaintainabilityIndex = overlay.MaxMaintainabilityIndex
	}
	if strings.TrimSpace(overlay.SortBy) != "" {
		out.SortBy = strings.TrimSpace(overlay.SortBy)
	}
	if overlay.IncludePackages {
		out.IncludePackages = true
	}
	return out
}

func mergeConfigLifecycle(base, overlay ConfigLifecycle) ConfigLifecycle {
	out := base
	if strings.TrimSpace(overlay.DedupeMode) != "" {
		out.DedupeMode = strings.TrimSpace(overlay.DedupeMode)
	}
	if overlay.MaxHops > 0 {
		out.MaxHops = overlay.MaxHops
	}
	if overlay.Summary {
		out.Summary = true
	}
	return out
}

func normalizeWorkspaceConfig(config WorkspaceConfig) WorkspaceConfig {
	if config.Version == 0 {
		config.Version = 1
	}
	config.PackagePatterns = cleanStringSlice(config.PackagePatterns)
	config.Sources.Include = cleanStringSlice(config.Sources.Include)
	config.Sources.Exclude = cleanStringSlice(config.Sources.Exclude)
	for i := range config.Modules {
		config.Modules[i].Root = cleanModuleRoot(config.Modules[i].Root)
		config.Modules[i].PackagePatterns = cleanStringSlice(config.Modules[i].PackagePatterns)
		if len(config.Modules[i].PackagePatterns) == 0 {
			config.Modules[i].PackagePatterns = []string{"./..."}
		}
		if strings.TrimSpace(config.Modules[i].Name) == "" {
			config.Modules[i].Name = configModuleName(config.Modules[i].Root, config.Modules[i].ModulePath)
		}
	}
	if len(config.PackagePatterns) == 0 && len(config.Modules) > 0 {
		config.PackagePatterns = modulePackagePatterns(config.Modules)
	}
	if len(config.PackagePatterns) == 0 {
		config.PackagePatterns = []string{"./..."}
	}
	if config.Sync.Debounce.Duration() <= 0 {
		config.Sync.Debounce = ConfigDuration(2500 * time.Millisecond)
	}
	if config.Sync.CheckInterval.Duration() <= 0 {
		config.Sync.CheckInterval = ConfigDuration(5 * time.Minute)
	}
	config.Embeddings.Provider = strings.ToLower(strings.TrimSpace(config.Embeddings.Provider))
	if config.Embeddings.Provider == "" {
		config.Embeddings.Provider = "local"
	}
	if config.Embeddings.BatchSize <= 0 {
		config.Embeddings.BatchSize = 50
	}
	if config.Embeddings.ChunkSize <= 0 {
		config.Embeddings.ChunkSize = 500
	}
	if config.Embeddings.Dimension <= 0 {
		config.Embeddings.Dimension = 16
	}
	if config.Embeddings.Local.Timeout.Duration() <= 0 {
		config.Embeddings.Local.Timeout = ConfigDuration(30 * time.Second)
	}
	if config.Embeddings.API.Timeout.Duration() <= 0 {
		config.Embeddings.API.Timeout = ConfigDuration(30 * time.Second)
	}
	if strings.TrimSpace(config.Embeddings.API.APIKeyEnv) == "" {
		config.Embeddings.API.APIKeyEnv = "OPENAI_API_KEY"
	}
	return config
}

func suggestGoWorkConfig(root string) (WorkspaceConfig, error) {
	goWorkPath := filepath.Join(root, "go.work")
	data, err := os.ReadFile(goWorkPath)
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("read go.work: %w", err)
	}
	work, err := modfile.ParseWork(goWorkPath, data, nil)
	if err != nil {
		return WorkspaceConfig{}, fmt.Errorf("parse go.work: %w", err)
	}

	modules := make([]ConfigModule, 0, len(work.Use))
	for _, use := range work.Use {
		moduleRoot := cleanModuleRoot(use.Path)
		moduleAbs := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(moduleRoot, "./")))
		if moduleRoot == "." {
			moduleAbs = root
		}
		modulePath, err := readGoModulePath(filepath.Join(moduleAbs, "go.mod"))
		if err != nil {
			return WorkspaceConfig{}, err
		}
		modules = append(modules, ConfigModule{
			Name:            configModuleName(moduleRoot, modulePath),
			Root:            moduleRoot,
			ModulePath:      modulePath,
			PackagePatterns: []string{"./..."},
		})
	}

	return normalizeWorkspaceConfig(WorkspaceConfig{
		Version:         1,
		Workspace:       ConfigWorkspace{Mode: "go_work", File: "go.work"},
		Modules:         modules,
		PackagePatterns: modulePackagePatterns(modules),
	}), nil
}

func readGoModulePath(goModPath string) (string, error) {
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return "", fmt.Errorf("read go.mod %s: %w", goModPath, err)
	}
	mod, err := modfile.Parse(goModPath, data, nil)
	if err != nil {
		return "", fmt.Errorf("parse go.mod %s: %w", goModPath, err)
	}
	if mod.Module == nil {
		return "", fmt.Errorf("go.mod %s has no module directive", goModPath)
	}
	return mod.Module.Mod.Path, nil
}

func modulePackagePatterns(modules []ConfigModule) []string {
	patterns := make([]string, 0, len(modules))
	for _, module := range modules {
		root := cleanModuleRoot(module.Root)
		modulePatterns := module.PackagePatterns
		if len(modulePatterns) == 0 {
			modulePatterns = []string{"./..."}
		}
		for _, pattern := range modulePatterns {
			patterns = append(patterns, joinModulePattern(root, pattern))
		}
	}
	return cleanStringSlice(patterns)
}

func joinModulePattern(root, pattern string) string {
	root = cleanModuleRoot(root)
	pattern = strings.TrimSpace(strings.ReplaceAll(pattern, "\\", "/"))
	if root == "." || root == "./" || root == "" {
		if pattern == "" {
			return "./..."
		}
		return pattern
	}
	root = strings.TrimPrefix(root, "./")
	if pattern == "" || pattern == "./..." {
		return "./" + root + "/..."
	}
	pattern = strings.TrimPrefix(pattern, "./")
	return "./" + strings.TrimSuffix(root, "/") + "/" + pattern
}

func cleanModuleRoot(root string) string {
	root = strings.TrimSpace(strings.ReplaceAll(root, "\\", "/"))
	if root == "" || root == "." || root == "./" {
		return "."
	}
	root = strings.TrimSuffix(root, "/")
	if filepath.IsAbs(filepath.FromSlash(root)) {
		return filepath.ToSlash(filepath.Clean(filepath.FromSlash(root)))
	}
	return "./" + strings.TrimPrefix(root, "./")
}

func cleanStringSlice(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func configModuleName(root, modulePath string) string {
	if modulePath != "" {
		parts := strings.Split(strings.Trim(modulePath, "/"), "/")
		if len(parts) > 0 && parts[len(parts)-1] != "" {
			return parts[len(parts)-1]
		}
	}
	root = strings.TrimPrefix(cleanModuleRoot(root), "./")
	if root == "." || root == "" {
		return "root"
	}
	return filepath.Base(filepath.FromSlash(root))
}

func configNotes(config WorkspaceConfig, repoExists, userExists bool) []string {
	notes := []string{"explicit tool inputs override config defaults"}
	if !repoExists {
		notes = append(notes, "no repo config found; using auto-detected workspace defaults")
	}
	if userExists {
		notes = append(notes, "user-local config was merged before repo config")
	}
	if config.Workspace.Mode == "go_work" {
		notes = append(notes, "go.work modules were expanded into root-relative package patterns")
	}
	return notes
}

func configRecommendedNextStep(repoExists bool) string {
	if repoExists {
		return "Use effective_config for analysis; do not call init_workspace_config unless the user explicitly asks to replace the repo config."
	}
	return "Use effective_config for analysis now; ask the user before calling init_workspace_config to create .go-arch-xray.yml."
}
