package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeStorage is an in-memory storageClient for tests.
type fakeStorage struct {
	objects    map[string][]byte
	presignURL string
	presignErr error
}

func newFakeStorage() *fakeStorage {
	return &fakeStorage{objects: make(map[string][]byte)}
}

func (f *fakeStorage) put(bucket, key string, data []byte) {
	f.objects[bucket+"/"+key] = data
}

func (f *fakeStorage) GetObject(_ context.Context, bucket, key string) ([]byte, error) {
	data, ok := f.objects[bucket+"/"+key]
	if !ok {
		return nil, fmt.Errorf("object not found: %s/%s", bucket, key)
	}
	return data, nil
}

func (f *fakeStorage) HeadObjectExists(_ context.Context, bucket, key string) (bool, error) {
	_, ok := f.objects[bucket+"/"+key]
	return ok, nil
}

func (f *fakeStorage) PresignGetObject(_ context.Context, _, _ string, _ time.Duration) (string, error) {
	if f.presignErr != nil {
		return "", f.presignErr
	}
	if f.presignURL != "" {
		return f.presignURL, nil
	}
	return "https://s3.example.com/presigned-url", nil
}

// singleManifestJSON is a minimal OCI single-arch manifest.
var singleManifestJSON = []byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json","schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:abc","size":1},"layers":[]}`)

// imageIndexJSON is a minimal OCI Image Index.
var imageIndexJSON = []byte(`{"mediaType":"application/vnd.oci.image.index.v1+json","schemaVersion":2,"manifests":[]}`)

// noMediaTypeManifestJSON has no mediaType field — should default to single-manifest type.
var noMediaTypeManifestJSON = []byte(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:abc","size":1},"layers":[]}`)

func TestHandleV2_ReturnsOK(t *testing.T) {
	h := &Handlers{cache: NewDigestCache()}
	req := httptest.NewRequest("GET", "/v2/", nil)
	rec := httptest.NewRecorder()
	h.HandleV2(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleManifest_SingleArch_V110(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "manifests/myapp/v1.0/manifest.json", singleManifestJSON)
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("Content-Type = %q, want single-manifest type", ct)
	}
	if rec.Header().Get("Docker-Content-Digest") == "" {
		t.Error("Docker-Content-Digest header must be set")
	}
}

func TestHandleManifest_ImageIndex(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "manifests/myapp/v1.0/manifest.json", imageIndexJSON)
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/vnd.oci.image.index.v1+json" {
		t.Errorf("Content-Type = %q, want image index type", ct)
	}
}

func TestHandleManifest_HEAD_NoBody(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "manifests/myapp/v1.0/manifest.json", singleManifestJSON)
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("HEAD", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD response must have empty body, got %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("Docker-Content-Digest") == "" {
		t.Error("Docker-Content-Digest must be set on HEAD")
	}
}

func TestHandleManifest_NoMediaType_DefaultsToSingleManifest(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "manifests/myapp/v1.0/manifest.json", noMediaTypeManifestJSON)
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/vnd.oci.image.manifest.v1+json" {
		t.Errorf("Content-Type = %q, want single-manifest type", ct)
	}
}

func TestHandleManifest_V110Fallback_NotFound(t *testing.T) {
	s := newFakeStorage()
	// Neither v1.1.0 nor v1.0.0 path exists
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/v1.0", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "MANIFEST_UNKNOWN") {
		t.Errorf("expected MANIFEST_UNKNOWN in body, got: %s", body)
	}
}

func TestHandleManifest_DigestFromCache(t *testing.T) {
	h := NewHandlers(newFakeStorage(), time.Hour)
	h.cache.PutManifest("sha256:cafebabe", singleManifestJSON)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/sha256:cafebabe", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleManifest_DigestFromBlobFallback(t *testing.T) {
	// Index child manifest stored at blobs/sha256/<encoded> by s3lo v1.3.0+
	s := newFakeStorage()
	s.put("my-bucket", "blobs/sha256/cafebabe", singleManifestJSON)
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/sha256:cafebabe", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestHandleManifest_DigestNotInCache(t *testing.T) {
	// Neither cache nor blobs/ path has the manifest
	h := NewHandlers(newFakeStorage(), time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/manifests/sha256:abc123", nil)
	rec := httptest.NewRecorder()
	h.HandleManifest(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "MANIFEST_UNKNOWN") {
		t.Errorf("expected MANIFEST_UNKNOWN in body")
	}
}

func TestHandleBlob_GET_PresignRedirect(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "blobs/sha256/abc123", []byte("layer data"))
	s.presignURL = "https://s3.example.com/presigned"
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/blobs/sha256:abc123", nil)
	rec := httptest.NewRecorder()
	h.HandleBlob(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d (307)", rec.Code, http.StatusSeeOther)
	}
	if loc := rec.Header().Get("Location"); loc != "https://s3.example.com/presigned" {
		t.Errorf("Location = %q, want presigned URL", loc)
	}
}

func TestHandleBlob_HEAD_NoBody(t *testing.T) {
	s := newFakeStorage()
	s.put("my-bucket", "blobs/sha256/abc123", []byte("layer data"))
	h := NewHandlers(s, time.Hour)

	req := httptest.NewRequest("HEAD", "/v2/my-bucket/myapp/blobs/sha256:abc123", nil)
	rec := httptest.NewRecorder()
	h.HandleBlob(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD must return empty body, got %d bytes", rec.Body.Len())
	}
	if rec.Header().Get("Docker-Content-Digest") != "sha256:abc123" {
		t.Errorf("Docker-Content-Digest = %q, want sha256:abc123", rec.Header().Get("Docker-Content-Digest"))
	}
}

func TestHandleBlob_NotFound(t *testing.T) {
	h := NewHandlers(newFakeStorage(), time.Hour)

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/blobs/sha256:missing", nil)
	rec := httptest.NewRecorder()
	h.HandleBlob(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "BLOB_UNKNOWN") {
		t.Errorf("expected BLOB_UNKNOWN in body")
	}
}

func TestHandleBlob_V100FallbackFromCache(t *testing.T) {
	s := newFakeStorage()
	// blob only at legacy v1.0.0 per-tag path
	s.put("my-bucket", "myapp/v1.0/blobs/sha256/abc123", []byte("layer data"))
	s.presignURL = "https://s3.example.com/legacy"
	h := NewHandlers(s, time.Hour)
	h.cache.Put("sha256:abc123", "my-bucket", "myapp/v1.0/blobs/sha256/abc123")

	req := httptest.NewRequest("GET", "/v2/my-bucket/myapp/blobs/sha256:abc123", nil)
	rec := httptest.NewRecorder()
	h.HandleBlob(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 307", rec.Code)
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
		{"/v2/my-bucket/myapp/manifests/v1.0", "my-bucket", "myapp", "v1.0", false},
		{"/v2/my-bucket/org/myapp/manifests/latest", "my-bucket", "org/myapp", "latest", false},
		{"/v2/manifests/v1.0", "", "", "", true},
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
		{"/v2/my-bucket/myapp/blobs/sha256:abc123", "my-bucket", "sha256:abc123", false},
		{"/v2/blobs/sha256:abc", "", "", true},
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

func TestWriteOCIError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeOCIError(rec, http.StatusNotFound, "MANIFEST_UNKNOWN", "image not found", "detail here")

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var body map[string][]map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	errs := body["errors"]
	if len(errs) != 1 {
		t.Fatalf("expected 1 error, got %d", len(errs))
	}
	if errs[0]["code"] != "MANIFEST_UNKNOWN" {
		t.Errorf("code = %q, want MANIFEST_UNKNOWN", errs[0]["code"])
	}
}

func TestMediaTypeFromManifest(t *testing.T) {
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(singleManifestJSON))
	_ = digest // used to silence unused import

	tests := []struct {
		data []byte
		want string
	}{
		{singleManifestJSON, "application/vnd.oci.image.manifest.v1+json"},
		{imageIndexJSON, "application/vnd.oci.image.index.v1+json"},
		{noMediaTypeManifestJSON, "application/vnd.oci.image.manifest.v1+json"},
		{[]byte(`{}`), "application/vnd.oci.image.manifest.v1+json"},
	}
	for _, tt := range tests {
		got := mediaTypeFromManifest(tt.data)
		if got != tt.want {
			t.Errorf("mediaTypeFromManifest(%s) = %q, want %q", tt.data, got, tt.want)
		}
	}
}
