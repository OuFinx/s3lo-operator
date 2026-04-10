package proxy

import (
	"sync"
)

// DigestCache maps blob digests to their S3 bucket and key,
// and caches manifest data by digest.
type DigestCache struct {
	mu        sync.RWMutex
	entries   map[string]BlobLocation
	manifests map[string][]byte // digest → manifest bytes
}

// BlobLocation identifies a blob on S3.
type BlobLocation struct {
	Bucket string
	Key    string
}

// NewDigestCache creates a new empty digest cache.
func NewDigestCache() *DigestCache {
	return &DigestCache{
		entries:   make(map[string]BlobLocation),
		manifests: make(map[string][]byte),
	}
}

// Put registers a blob digest with its S3 location.
func (c *DigestCache) Put(digest, bucket, key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[digest] = BlobLocation{Bucket: bucket, Key: key}
}

// Get looks up the S3 location for a blob digest.
func (c *DigestCache) Get(digest string) (BlobLocation, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	loc, ok := c.entries[digest]
	return loc, ok
}

// PutManifest caches manifest bytes by digest.
func (c *DigestCache) PutManifest(digest string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manifests[digest] = data
}

// GetManifest retrieves cached manifest bytes by digest.
func (c *DigestCache) GetManifest(digest string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	data, ok := c.manifests[digest]
	return data, ok
}
