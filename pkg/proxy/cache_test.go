package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDigestCache_PutGet(t *testing.T) {
	c := NewDigestCache()
	c.Put("sha256:abc123", "my-bucket", "myapp/v1.0/blobs/sha256/abc123")
	loc, ok := c.Get("sha256:abc123")
	if !ok {
		t.Fatal("expected to find cached digest")
	}
	if loc.Bucket != "my-bucket" {
		t.Errorf("bucket = %q, want %q", loc.Bucket, "my-bucket")
	}
	if loc.Key != "myapp/v1.0/blobs/sha256/abc123" {
		t.Errorf("key = %q, want %q", loc.Key, "myapp/v1.0/blobs/sha256/abc123")
	}
}

func TestDigestCache_Miss(t *testing.T) {
	c := NewDigestCache()
	_, ok := c.Get("sha256:nonexistent")
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestDigestCache_ManifestEviction(t *testing.T) {
	c := NewDigestCacheWithConfig(3, "", 24*time.Hour)
	c.PutManifest("sha256:a", []byte("a"))
	c.PutManifest("sha256:b", []byte("b"))
	c.PutManifest("sha256:c", []byte("c"))

	if _, ok := c.GetManifest("sha256:a"); !ok {
		t.Fatal("sha256:a should be cached before eviction")
	}

	// Adding a 4th entry must evict the oldest (sha256:a).
	c.PutManifest("sha256:d", []byte("d"))

	if _, ok := c.GetManifest("sha256:a"); ok {
		t.Fatal("sha256:a should have been evicted")
	}
	if _, ok := c.GetManifest("sha256:d"); !ok {
		t.Fatal("sha256:d should be present after insert")
	}
}

func TestDigestCache_ManifestEviction_UpdateExisting(t *testing.T) {
	c := NewDigestCacheWithConfig(2, "", 24*time.Hour)
	c.PutManifest("sha256:a", []byte("a"))
	c.PutManifest("sha256:b", []byte("b"))
	c.PutManifest("sha256:a", []byte("a-updated")) // update, not new entry

	if _, ok := c.GetManifest("sha256:a"); !ok {
		t.Fatal("sha256:a should still be cached after update")
	}
	if _, ok := c.GetManifest("sha256:b"); !ok {
		t.Fatal("sha256:b must not be evicted on update of sha256:a")
	}
}

func TestDigestCache_DiskCache_WriteAndRead(t *testing.T) {
	dir := t.TempDir()
	c := NewDigestCacheWithConfig(10, dir, time.Hour)
	c.PutManifest("sha256:abc", []byte("manifest data"))

	onDisk, err := os.ReadFile(filepath.Join(dir, "manifests", "abc"))
	if err != nil {
		t.Fatalf("disk file not written: %v", err)
	}
	if string(onDisk) != "manifest data" {
		t.Errorf("disk contents = %q, want %q", onDisk, "manifest data")
	}

	c2 := NewDigestCacheWithConfig(10, dir, time.Hour)
	got, ok := c2.GetManifest("sha256:abc")
	if !ok {
		t.Fatal("expected disk cache hit in fresh cache instance")
	}
	if string(got) != "manifest data" {
		t.Errorf("data = %q, want %q", got, "manifest data")
	}
}

func TestDigestCache_DiskCache_TTLExpired(t *testing.T) {
	dir := t.TempDir()

	hexPath := filepath.Join(dir, "manifests", "abc")
	if err := os.MkdirAll(filepath.Dir(hexPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hexPath, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(hexPath, past, past); err != nil {
		t.Fatal(err)
	}

	c := NewDigestCacheWithConfig(10, dir, 24*time.Hour)
	_, ok := c.GetManifest("sha256:abc")
	if ok {
		t.Fatal("expected cache miss for TTL-expired disk entry")
	}
}
