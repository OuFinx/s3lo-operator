package proxy

import (
	"net/http"
	"strings"
	"time"
)

// ServerConfig holds all configuration for the OCI proxy server.
// Metrics *Metrics field is added in Task 3 once the type is defined.
type ServerConfig struct {
	Port            string
	PresignTTL      time.Duration
	CacheMaxEntries int
	CacheDir        string
	CacheTTL        time.Duration
	S3MaxConcurrent int
	HealthBucket    string
	Verifier        *Verifier
}

// newHandlersWithCache creates handlers using a pre-configured DigestCache.
func newHandlersWithCache(client storageClient, cache *DigestCache, presignTTL time.Duration) *Handlers {
	return &Handlers{
		s3:         client,
		cache:      cache,
		presignTTL: presignTTL,
	}
}

// NewServer creates the OCI proxy HTTP server.
func NewServer(client storageClient, cfg ServerConfig) *http.Server {
	maxEntries := cfg.CacheMaxEntries
	if maxEntries == 0 {
		maxEntries = 10000
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = 24 * time.Hour
	}
	cache := NewDigestCacheWithConfig(maxEntries, cfg.CacheDir, cacheTTL)
	h := newHandlersWithCache(client, cache, cfg.PresignTTL)
	h.verifier = cfg.Verifier

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path := r.URL.Path
		switch {
		case path == "/v2/" || path == "/v2":
			h.HandleV2(w, r)
		case strings.Contains(path, "/manifests/"):
			h.HandleManifest(w, r)
		case strings.Contains(path, "/blobs/"):
			h.HandleBlob(w, r)
		default:
			http.NotFound(w, r)
		}
	})
	mux.HandleFunc("/healthz", h.HandleHealth)
	mux.HandleFunc("/readyz", h.HandleHealth)

	return &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: mux,
	}
}
