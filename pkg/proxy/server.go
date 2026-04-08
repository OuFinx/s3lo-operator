package proxy

import (
	"net/http"
	"strings"

	s3client "github.com/finx/s3lo/pkg/s3"
)

// NewServer creates an HTTP server with OCI Distribution API routes.
func NewServer(client *s3client.Client, port string) *http.Server {
	h := NewHandlers(client)
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
		Addr:    ":" + port,
		Handler: mux,
	}
}
