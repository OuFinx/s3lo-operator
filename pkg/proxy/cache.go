package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DigestCache maps blob digests to their S3 location and caches manifest bytes.
type DigestCache struct {
	mu           sync.RWMutex
	entries      map[string]BlobLocation
	manifests    map[string][]byte
	manifestKeys []string // insertion-ordered keys for FIFO eviction
	maxEntries   int      // 0 = unlimited
	cacheDir     string   // "" = no disk cache
	cacheTTL     time.Duration
}

// BlobLocation identifies a blob on S3.
type BlobLocation struct {
	Bucket string
	Key    string
}

// NewDigestCache creates a cache with default in-memory-only settings.
func NewDigestCache() *DigestCache {
	return NewDigestCacheWithConfig(10000, "", 24*time.Hour)
}

// NewDigestCacheWithConfig creates a cache with explicit settings.
// maxEntries=0 disables eviction. cacheDir="" disables disk persistence.
func NewDigestCacheWithConfig(maxEntries int, cacheDir string, cacheTTL time.Duration) *DigestCache {
	return &DigestCache{
		entries:    make(map[string]BlobLocation),
		manifests:  make(map[string][]byte),
		maxEntries: maxEntries,
		cacheDir:   cacheDir,
		cacheTTL:   cacheTTL,
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

// PutManifest caches manifest bytes by digest, evicting the oldest entry if
// the cap is reached, and writing to disk if cacheDir is set.
func (c *DigestCache) PutManifest(digest string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.manifests[digest]; !exists {
		if c.maxEntries > 0 && len(c.manifests) >= c.maxEntries {
			oldest := c.manifestKeys[0]
			c.manifestKeys = c.manifestKeys[1:]
			delete(c.manifests, oldest)
		}
		c.manifestKeys = append(c.manifestKeys, digest)
	}
	c.manifests[digest] = data
	if c.cacheDir != "" {
		c.writeToDisk(digest, data)
	}
}

// GetManifest retrieves manifest bytes by digest.
// On an in-memory miss, falls back to disk if cacheDir is set.
func (c *DigestCache) GetManifest(digest string) ([]byte, bool) {
	c.mu.RLock()
	data, ok := c.manifests[digest]
	c.mu.RUnlock()
	if ok {
		return data, true
	}
	if c.cacheDir != "" {
		if data, ok := c.readFromDisk(digest); ok {
			c.PutManifest(digest, data) // warm in-memory cache
			return data, true
		}
	}
	return nil, false
}

// writeToDisk writes manifest bytes to <cacheDir>/manifests/<hex>.
// Errors are silently ignored — disk cache is best-effort.
func (c *DigestCache) writeToDisk(digest string, data []byte) {
	dir := filepath.Join(c.cacheDir, "manifests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	hex := strings.TrimPrefix(digest, "sha256:")
	_ = os.WriteFile(filepath.Join(dir, hex), data, 0o644)
}

// readFromDisk reads manifest bytes from disk if the file exists and is within TTL.
func (c *DigestCache) readFromDisk(digest string) ([]byte, bool) {
	hex := strings.TrimPrefix(digest, "sha256:")
	path := filepath.Join(c.cacheDir, "manifests", hex)
	info, err := os.Stat(path)
	if err != nil {
		return nil, false
	}
	if c.cacheTTL > 0 && time.Since(info.ModTime()) > c.cacheTTL {
		return nil, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}
