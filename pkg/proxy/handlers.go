package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/OuFinx/s3lo/pkg/oci"
	"github.com/OuFinx/s3lo/pkg/storage"
)

// storageClient abstracts S3 operations used by the proxy.
// *storage.Client satisfies this interface.
type storageClient interface {
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)
	HeadObjectExists(ctx context.Context, bucket, key string) (bool, error)
	PresignGetObject(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}

// semaphore limits concurrent S3 API calls. A nil semaphore is a no-op.
type semaphore chan struct{}

func newSemaphore(n int) semaphore {
	if n <= 0 {
		return nil
	}
	return make(semaphore, n)
}

func (s semaphore) acquire(ctx context.Context) error {
	if s == nil {
		return nil
	}
	select {
	case s <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s semaphore) release() {
	if s != nil {
		<-s
	}
}

// Handlers implements OCI Distribution API endpoints backed by S3.
type Handlers struct {
	s3         storageClient
	cache      *DigestCache
	presignTTL time.Duration
	verifier   *Verifier  // nil means verification disabled
	metrics    *Metrics   // nil = no metrics
	sem        semaphore  // nil = no rate limit
}

// NewHandlers creates new OCI API handlers.
func NewHandlers(client storageClient, presignTTL time.Duration) *Handlers {
	return &Handlers{
		s3:         client,
		cache:      NewDigestCache(),
		presignTTL: presignTTL,
	}
}

// HandleV2 handles GET /v2/ — API version check.
func (h *Handlers) HandleV2(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// HandleManifest handles GET|HEAD /v2/<bucket>/<image>/manifests/<ref>
func (h *Handlers) HandleManifest(w http.ResponseWriter, r *http.Request) {
	bucket, image, ref, err := parseManifestPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Digest ref: check cache then fall back to blobs/ (index children stored by s3lo v1.3.0+).
	if strings.HasPrefix(ref, "sha256:") {
		if data, ok := h.cache.GetManifest(ref); ok {
			h.metrics.incManifest("cache")
			h.serveManifest(w, r, data)
			return
		}
		encoded := strings.TrimPrefix(ref, "sha256:")
		h.metrics.incS3("manifest_get")
		data, err := h.s3.GetObject(r.Context(), bucket, "blobs/sha256/"+encoded)
		if err == nil {
			h.cache.PutManifest(ref, data)
			h.metrics.incManifest("s3")
			h.serveManifest(w, r, data)
			return
		}
		h.metrics.incManifest("error")
		writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN", "manifest unknown",
			fmt.Sprintf("digest %s not in cache and not found at blobs/sha256/%s", ref, encoded))
		return
	}

	ctx := r.Context()

	if err := h.sem.acquire(ctx); err != nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "rate limited", "")
		return
	}
	defer h.sem.release()

	// Try v1.1.0 layout: manifests/<image>/<ref>/manifest.json
	v110Key := "manifests/" + image + "/" + ref + "/manifest.json"
	h.metrics.incS3("manifest_get")
	manifestData, err := h.s3.GetObject(ctx, bucket, v110Key)
	isV110 := true
	if err != nil {
		if !storage.IsNotFound(err) {
			log.Printf("S3 GetObject %s/%s: %v", bucket, v110Key, err)
			h.metrics.incManifest("error")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Fallback to v1.0.0 layout: <image>/<ref>/manifest.json
		isV110 = false
		v100Key := image + "/" + ref + "/manifest.json"
		h.metrics.incS3("manifest_get")
		manifestData, err = h.s3.GetObject(ctx, bucket, v100Key)
		if err != nil {
			if storage.IsNotFound(err) {
				h.metrics.incManifest("error")
				writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN",
					fmt.Sprintf("image not found: s3://%s/%s:%s", bucket, image, ref),
					fmt.Sprintf("tried s3://%s/%s and s3://%s/%s", bucket, v110Key, bucket, v100Key))
				return
			}
			log.Printf("S3 GetObject %s/%s (v1.0.0): %v", bucket, image+"/"+ref+"/manifest.json", err)
			h.metrics.incManifest("error")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifestData))
	h.cache.PutManifest(digest, manifestData)

	// Cache per-tag blob paths for v1.0.0 images (needed by blob handler fallback).
	if !isV110 {
		if manifest, err := oci.ParseManifest(manifestData); err == nil {
			prefix := image + "/" + ref + "/blobs/sha256/"
			h.cache.Put(manifest.Config.Digest.String(), bucket, prefix+manifest.Config.Digest.Encoded())
			for _, layer := range manifest.Layers {
				h.cache.Put(layer.Digest.String(), bucket, prefix+layer.Digest.Encoded())
			}
		}
	}

	// Signature verification — only for tag-based requests; digest pulls are follow-ups.
	if h.verifier != nil {
		if err := h.verifier.Check(ctx, h.s3, bucket, image, ref, manifestData); err != nil {
			h.metrics.incManifest("error")
			writeOCIError(w, http.StatusForbidden, "DENIED",
				"image not permitted: invalid or missing signature", err.Error())
			return
		}
	}

	h.metrics.incManifest("s3")
	h.serveManifest(w, r, manifestData)
}

// serveManifest writes manifest bytes with correct OCI headers. Handles HEAD (no body).
func (h *Handlers) serveManifest(w http.ResponseWriter, r *http.Request, data []byte) {
	ct := mediaTypeFromManifest(data)
	if accept := r.Header.Get("Accept"); accept != "" {
		if !acceptsMediaType(accept, ct) {
			writeOCIError(w, http.StatusNotAcceptable, "UNSUPPORTED", "media type not accepted", ct)
			return
		}
	}
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write(data)
	}
}

// mediaTypeFromManifest extracts the mediaType field from manifest JSON.
// Falls back to the OCI single-manifest type when the field is absent.
func mediaTypeFromManifest(data []byte) string {
	var m struct {
		MediaType string `json:"mediaType"`
	}
	if err := json.Unmarshal(data, &m); err == nil && m.MediaType != "" {
		return m.MediaType
	}
	return "application/vnd.oci.image.manifest.v1+json"
}

// acceptsMediaType returns true if accept contains mediaType or "*/*".
// accept may be a comma-separated list of media types (with optional params).
func acceptsMediaType(accept, mediaType string) bool {
	for _, part := range strings.Split(accept, ",") {
		t := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if t == "*/*" || t == mediaType {
			return true
		}
	}
	return false
}

// HandleBlob handles GET|HEAD /v2/<bucket>/<image>/blobs/<digest>
// GET responds with a presigned URL redirect (307); HEAD returns existence headers only.
func (h *Handlers) HandleBlob(w http.ResponseWriter, r *http.Request) {
	bucket, digest, err := parseBlobPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if !strings.HasPrefix(digest, "sha256:") {
		h.metrics.incBlob("error")
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob unknown", digest)
		return
	}

	encoded := strings.TrimPrefix(digest, "sha256:")
	ctx := r.Context()
	fetchBucket := bucket
	key := "blobs/sha256/" + encoded

	if err := h.sem.acquire(ctx); err != nil {
		writeOCIError(w, http.StatusServiceUnavailable, "UNAVAILABLE", "rate limited", "")
		return
	}
	defer h.sem.release()

	// Check v1.1.0 global blob path.
	h.metrics.incS3("blob_head")
	exists, err := h.s3.HeadObjectExists(ctx, fetchBucket, key)
	if err != nil {
		log.Printf("HeadObject %s/%s: %v", fetchBucket, key, err)
		h.metrics.incBlob("error")
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error", "")
		return
	}

	if !exists {
		// v1.0.0 fallback: resolve from digest cache populated by HandleManifest.
		loc, ok := h.cache.Get(digest)
		if !ok {
			h.metrics.incBlob("error")
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN",
				"blob not found in S3",
				fmt.Sprintf("digest %s not at blobs/sha256/%s and not in v1.0.0 cache", digest, encoded))
			return
		}
		fetchBucket = loc.Bucket
		key = loc.Key
		h.metrics.incS3("blob_head")
		exists, err = h.s3.HeadObjectExists(ctx, fetchBucket, key)
		if err != nil {
			log.Printf("HeadObject (v1.0.0 fallback) %s/%s: %v", fetchBucket, key, err)
			h.metrics.incBlob("error")
			writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "storage error", "")
			return
		}
		if !exists {
			h.metrics.incBlob("error")
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found in S3", digest)
			return
		}
	}

	w.Header().Set("Docker-Content-Digest", digest)
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Presign redirect — containerd follows 307 natively; no data passes through the proxy.
	h.metrics.incS3("blob_presign")
	url, err := h.s3.PresignGetObject(ctx, fetchBucket, key, h.presignTTL)
	if err != nil {
		log.Printf("PresignGetObject %s/%s: %v", fetchBucket, key, err)
		h.metrics.incBlob("error")
		writeOCIError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "presign failed", "")
		return
	}
	h.metrics.incBlob("redirect")
	http.Redirect(w, r, url, http.StatusTemporaryRedirect)
}

// HandleHealth handles GET /healthz and /readyz
func (h *Handlers) HandleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// writeOCIError writes an OCI Distribution API compliant error response.
func writeOCIError(w http.ResponseWriter, status int, code, message, detail string) {
	type ociError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Detail  string `json:"detail"`
	}
	body, _ := json.Marshal(map[string][]ociError{
		"errors": {{Code: code, Message: message, Detail: detail}},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(body)
}

// parseManifestPath parses /v2/<bucket>/<image...>/manifests/<ref>
func parseManifestPath(path string) (bucket, image, ref string, err error) {
	path = strings.TrimPrefix(path, "/v2/")
	manifestIdx := strings.Index(path, "/manifests/")
	if manifestIdx < 0 {
		return "", "", "", fmt.Errorf("invalid manifest path: missing /manifests/")
	}
	ref = path[manifestIdx+len("/manifests/"):]
	namePart := path[:manifestIdx]
	slashIdx := strings.Index(namePart, "/")
	if slashIdx < 0 {
		return "", "", "", fmt.Errorf("invalid manifest path: missing image name")
	}
	bucket = namePart[:slashIdx]
	image = namePart[slashIdx+1:]
	if bucket == "" || image == "" || ref == "" {
		return "", "", "", fmt.Errorf("invalid manifest path: empty component")
	}
	return bucket, image, ref, nil
}

// parseBlobPath parses /v2/<bucket>/<image...>/blobs/<digest>
func parseBlobPath(path string) (bucket, digest string, err error) {
	path = strings.TrimPrefix(path, "/v2/")
	blobIdx := strings.Index(path, "/blobs/")
	if blobIdx < 0 {
		return "", "", fmt.Errorf("invalid blob path: missing /blobs/")
	}
	digest = path[blobIdx+len("/blobs/"):]
	namePart := path[:blobIdx]
	slashIdx := strings.Index(namePart, "/")
	if slashIdx < 0 {
		return "", "", fmt.Errorf("invalid blob path: missing image name")
	}
	bucket = namePart[:slashIdx]
	if bucket == "" || digest == "" {
		return "", "", fmt.Errorf("invalid blob path: empty component")
	}
	return bucket, digest, nil
}
