package proxy

import (
	"testing"
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
