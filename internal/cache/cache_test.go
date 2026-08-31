package cache

import (
	"testing"
)

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c, err := New(dir, true)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	key := Key("img-hash", "crop-1", "vision-v1", "qwen")
	type val struct {
		N int
		S string
	}
	want := val{N: 7, S: "hello"}
	if err := c.Set(key, want, map[string]any{"crop": "c1"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !c.Has(key) {
		t.Fatal("key should exist after set")
	}
	var got val
	ok, err := c.Get(key, &got)
	if err != nil || !ok {
		t.Fatalf("get ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("roundtrip mismatch: %+v != %+v", got, want)
	}
}

func TestCacheDisabledNeverHits(t *testing.T) {
	c, err := New(t.TempDir(), false)
	if err != nil {
		t.Fatalf("new cache: %v", err)
	}
	if err := c.Set("k", struct{}{}, nil); err != nil {
		t.Fatalf("set on disabled cache: %v", err)
	}
	if c.Has("k") {
		t.Fatal("disabled cache should never report hit")
	}
}

func TestKeyDeterministic(t *testing.T) {
	a := Key("x", "y")
	b := Key("x", "y")
	if a != b {
		t.Fatalf("keys not deterministic: %s vs %s", a, b)
	}
	c := Key("x", "z")
	if a == c {
		t.Fatal("distinct inputs should produce distinct keys")
	}
}
