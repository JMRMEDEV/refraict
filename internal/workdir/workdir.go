// Package workdir manages the on-disk analysis workspace layout.
package workdir

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Workspace is an analysis output directory with the standard layout.
type Workspace struct {
	Root string
}

// New ensures the directory tree exists and returns a Workspace.
func New(root string) (*Workspace, error) {
	if root == "" {
		return nil, fmt.Errorf("empty output directory")
	}
	for _, sub := range []string{"regions", "crops", "evidence"} {
		if err := os.MkdirAll(filepath.Join(root, sub), 0o755); err != nil {
			return nil, err
		}
	}
	return &Workspace{Root: root}, nil
}

// Path returns a path within the workspace.
func (w *Workspace) Path(parts ...string) string {
	return filepath.Join(append([]string{w.Root}, parts...)...)
}

// WriteJSON writes v as pretty JSON at path.
func (w *Workspace) WriteJSON(rel string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(w.Path(rel), data, 0o644)
}

// WriteText writes text at path.
func (w *Workspace) WriteText(rel, text string) error {
	return os.WriteFile(w.Path(rel), []byte(text), 0o644)
}

// WriteBytes writes raw bytes at path (e.g. PNG).
func (w *Workspace) WriteBytes(rel string, data []byte) error {
	return os.WriteFile(w.Path(rel), data, 0o644)
}

// ReadText reads a text file.
func (w *Workspace) ReadText(rel string) (string, error) {
	data, err := os.ReadFile(w.Path(rel))
	return string(data), err
}

// Exists reports whether a relative path exists.
func (w *Workspace) Exists(rel string) bool {
	_, err := os.Stat(w.Path(rel))
	return err == nil
}
