package analyzer

import (
	"os"
	"path/filepath"
)

type WorkspaceLayout struct {
	Root       string
	GAXPath    string
	DBPath     string
	ConfigPath string
	StatePath  string
}

func WorkspaceLayoutFor(root string) WorkspaceLayout {
	root = filepath.Clean(root)
	gax := filepath.Join(root, ".gax")
	return WorkspaceLayout{
		Root:       root,
		GAXPath:    gax,
		DBPath:     filepath.Join(gax, "cache.db"),
		ConfigPath: filepath.Join(gax, "config.yml"),
		StatePath:  filepath.Join(gax, "state.json"),
	}
}

func (l WorkspaceLayout) EnsureExists() error {
	return os.MkdirAll(l.GAXPath, 0o755)
}
