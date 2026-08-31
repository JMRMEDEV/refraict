package config

import (
	"encoding/json"
	"testing"
)

func TestCacheDirBackwardCompat(t *testing.T) {
	// Legacy "database" key still works.
	legacy := `{"cache":{"enabled":true,"database":"./legacy.db"}}`
	var c Config
	if err := json.Unmarshal([]byte(legacy), &c); err != nil {
		t.Fatal(err)
	}
	if c.Cache.Dir != "./legacy.db" {
		t.Fatalf("legacy database not mapped to Dir: %q", c.Cache.Dir)
	}

	// Canonical "dir" key works.
	canon := `{"cache":{"enabled":true,"dir":"./.refraict-cache"}}`
	var c2 Config
	if err := json.Unmarshal([]byte(canon), &c2); err != nil {
		t.Fatal(err)
	}
	if c2.Cache.Dir != "./.refraict-cache" {
		t.Fatalf("dir not parsed: %q", c2.Cache.Dir)
	}
}
