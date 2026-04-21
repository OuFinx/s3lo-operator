package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerRouting(t *testing.T) {
	srv := NewServer(nil, ServerConfig{Port: "5732", PresignTTL: time.Hour})

	tests := []struct {
		method string
		path   string
		want   int
	}{
		{"GET", "/v2/", http.StatusOK},
		{"GET", "/healthz", http.StatusOK},
		{"GET", "/readyz", http.StatusOK},
		{"POST", "/v2/", http.StatusMethodNotAllowed},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
		})
	}
}
