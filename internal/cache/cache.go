// Package cache implements caching for expensive deterministic and inference
// stages. The cache key combines input hash + stage version + model version.
// A file-based cache is used by default; SQLite is available for indexed
// metadata.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Cache is a key->value store on disk under a root directory. Each entry
// stores raw bytes plus a version tag.
type Cache struct {
	Root    string
	Enabled bool
}

// New creates a file-based cache rooted at dir.
func New(dir string, enabled bool) (*Cache, error) {
	if enabled {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Cache{Root: dir, Enabled: enabled}, nil
}

// Key builds a cache key from a list of hashable string parts.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) entryPath(key string) string {
	return filepath.Join(c.Root, key[:2], key+".json")
}

// Has reports whether a key is present.
func (c *Cache) Has(key string) bool {
	if !c.Enabled {
		return false
	}
	_, err := os.Stat(c.entryPath(key))
	return err == nil
}

// Get unmarshals the stored value into v. Returns false if absent.
func (c *Cache) Get(key string, v any) (bool, error) {
	if !c.Enabled {
		return false, nil
	}
	data, err := os.ReadFile(c.entryPath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		// Backward-compatible: accept a plain value written without an envelope.
		if err := json.Unmarshal(data, v); err != nil {
			return false, err
		}
		return true, nil
	}
	if len(env.Value) == 0 {
		return false, nil
	}
	if err := json.Unmarshal(env.Value, v); err != nil {
		return false, err
	}
	return true, nil
}

type envelope struct {
	Key   string          `json:"key"`
	Meta  map[string]any  `json:"meta,omitempty"`
	Value json.RawMessage `json:"value"`
}

// Set stores v under key with optional metadata.
func (c *Cache) Set(key string, v any, meta map[string]any) error {
	if !c.Enabled {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	env := envelope{Key: key, Meta: meta, Value: raw}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	path := c.entryPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Status returns counts for diagnostics.
type Status struct {
	Entries int `json:"entries"`
	Root    string `json:"root"`
}

// StatusReport scans the cache directory.
func (c *Cache) StatusReport() (*Status, error) {
	st := &Status{Root: c.Root}
	if !c.Enabled {
		return st, nil
	}
	count := 0
	err := filepath.Walk(c.Root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".json" {
			count++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	st.Entries = count
	return st, nil
}

// Clear removes all cached entries.
func (c *Cache) Clear() error {
	if !c.Enabled {
		return nil
	}
	return os.RemoveAll(c.Root)
}
