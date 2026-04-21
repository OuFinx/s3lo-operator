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
	CacheMaxEntries int           // 0 → default 10000; wired in Task 2
	CacheDir        string        // "" → no disk cache; wired in Task 2
	CacheTTL        time.Duration // 0 → default 24h; wired in Task 2
	S3MaxConcurrent int           // 0 → no rate limit; wired in Task 5
	HealthBucket    string        // "" → no S3 readiness check; wired in Task 6
	Verifier        *Verifier
}

// NewServer creates the OCI proxy HTTP server.
func NewServer(client storageClient, cfg ServerConfig) *http.Server {
	h := NewHandlers(client, cfg.PresignTTL)
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
