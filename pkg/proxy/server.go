package proxy

import (
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServerConfig holds all configuration for the OCI proxy server.
type ServerConfig struct {
	Port            string
	PresignTTL      time.Duration
	CacheMaxEntries int
	CacheDir        string
	CacheTTL        time.Duration
	S3MaxConcurrent int
	HealthBucket    string
	Verifier        *Verifier
	Metrics         *Metrics // nil = no metrics
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
	h.metrics = cfg.Metrics

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

// NewMetricsServer creates an HTTP server serving /metrics for Prometheus scraping.
func NewMetricsServer(port string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}
}
