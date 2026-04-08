package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleV2_ReturnsOK(t *testing.T) {
	h := &Handlers{cache: NewDigestCache()}

	req := httptest.NewRequest("GET", "/v2/", nil)
	rec := httptest.NewRecorder()

	h.HandleV2(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestParseManifestPath(t *testing.T) {
	tests := []struct {
		path       string
		wantBucket string
		wantImage  string
		wantRef    string
		wantErr    bool
	}{
		{
			path:       "/v2/my-bucket/myapp/manifests/v1.0",
			wantBucket: "my-bucket",
			wantImage:  "myapp",
			wantRef:    "v1.0",
		},
		{
			path:       "/v2/my-bucket/org/myapp/manifests/latest",
			wantBucket: "my-bucket",
			wantImage:  "org/myapp",
			wantRef:    "latest",
		},
		{
			path:    "/v2/manifests/v1.0",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			bucket, image, ref, err := parseManifestPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Errorf("bucket = %q, want %q", bucket, tt.wantBucket)
			}
			if image != tt.wantImage {
				t.Errorf("image = %q, want %q", image, tt.wantImage)
			}
			if ref != tt.wantRef {
				t.Errorf("ref = %q, want %q", ref, tt.wantRef)
			}
		})
	}
}

func TestParseBlobPath(t *testing.T) {
	tests := []struct {
		path       string
		wantBucket string
		wantDigest string
		wantErr    bool
	}{
		{
			path:       "/v2/my-bucket/myapp/blobs/sha256:abc123",
			wantBucket: "my-bucket",
			wantDigest: "sha256:abc123",
		},
		{
			path:    "/v2/blobs/sha256:abc",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			bucket, digest, err := parseBlobPath(tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Errorf("bucket = %q, want %q", bucket, tt.wantBucket)
			}
			if digest != tt.wantDigest {
				t.Errorf("digest = %q, want %q", digest, tt.wantDigest)
			}
		})
	}
}
