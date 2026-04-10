package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/OuFinx/s3lo/pkg/oci"
	s3client "github.com/OuFinx/s3lo/pkg/s3"
)

// Handlers implements OCI Distribution API endpoints backed by S3.
type Handlers struct {
	s3    *s3client.Client
	cache *DigestCache
}

// NewHandlers creates new OCI API handlers.
func NewHandlers(client *s3client.Client) *Handlers {
	return &Handlers{
		s3:    client,
		cache: NewDigestCache(),
	}
}

// HandleV2 handles GET /v2/ — API version check.
func (h *Handlers) HandleV2(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("{}"))
}

// HandleManifest handles GET /v2/<bucket>/<image>/manifests/<ref>
// Tries v1.1.0 layout (manifests/<image>/<ref>/manifest.json) first,
// falls back to v1.0.0 (<image>/<ref>/manifest.json) on 404.
func (h *Handlers) HandleManifest(w http.ResponseWriter, r *http.Request) {
	bucket, image, ref, err := parseManifestPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Digest ref: serve from cache (set on first tag-based fetch).
	if strings.HasPrefix(ref, "sha256:") {
		if data, ok := h.cache.GetManifest(ref); ok {
			h.serveManifest(w, data, ref)
			return
		}
		log.Printf("manifest digest not in cache: %s", ref)
		writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN",
			"manifest unknown",
			fmt.Sprintf("digest %s not in cache — pull by tag first", ref))
		return
	}

	ctx := r.Context()
	s3c, err := h.s3.ClientForBucket(ctx, bucket)
	if err != nil {
		log.Printf("S3 client error for bucket %s: %v", bucket, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Try v1.1.0: manifests/<image>/<ref>/manifest.json
	v110Key := "manifests/" + image + "/" + ref + "/manifest.json"
	manifestData, err := getS3Object(ctx, s3c, bucket, v110Key)
	isV110 := true
	if err != nil {
		if !isNotFound(err) {
			log.Printf("S3 GetObject %s/%s: %v", bucket, v110Key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		// Fallback to v1.0.0: <image>/<ref>/manifest.json
		isV110 = false
		v100Key := image + "/" + ref + "/manifest.json"
		manifestData, err = getS3Object(ctx, s3c, bucket, v100Key)
		if err != nil {
			if isNotFound(err) {
				writeOCIError(w, http.StatusNotFound, "MANIFEST_UNKNOWN",
					fmt.Sprintf("image not found in S3: s3://%s/%s:%s", bucket, image, ref),
					fmt.Sprintf("tried s3://%s/%s and s3://%s/%s", bucket, v110Key, bucket, v100Key))
				return
			}
			log.Printf("S3 GetObject %s/%s: %v", bucket, v100Key, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(manifestData))
	h.cache.PutManifest(digest, manifestData)

	// For v1.0.0 images, cache explicit blob paths (needed by HandleBlob fallback).
	// For v1.1.0 images, blob paths are deterministic from digest — no cache needed.
	if !isV110 {
		manifest, err := oci.ParseManifest(manifestData)
		if err == nil {
			prefix := image + "/" + ref + "/blobs/sha256/"
			h.cache.Put(manifest.Config.Digest.String(), bucket, prefix+manifest.Config.Digest.Encoded())
			for _, layer := range manifest.Layers {
				h.cache.Put(layer.Digest.String(), bucket, prefix+layer.Digest.Encoded())
			}
		}
	}

	h.serveManifest(w, manifestData, digest)
}

func (h *Handlers) serveManifest(w http.ResponseWriter, data []byte, digest string) {
	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Docker-Content-Digest", digest)
	w.WriteHeader(http.StatusOK)
	w.Write(data)
}

// HandleBlob handles GET /v2/<bucket>/<image>/blobs/<digest>
// Tries v1.1.0 global path (blobs/sha256/<encoded>) first,
// falls back to v1.0.0 per-tag path via cache on 404.
func (h *Handlers) HandleBlob(w http.ResponseWriter, r *http.Request) {
	bucket, digest, err := parseBlobPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	s3c, err := h.s3.ClientForBucket(ctx, bucket)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	encoded := strings.TrimPrefix(digest, "sha256:")
	fetchBucket := bucket
	key := "blobs/sha256/" + encoded

	resp, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &fetchBucket,
		Key:    &key,
	})
	if isNotFound(err) {
		// v1.0.0 fallback: look up per-tag blob path from cache.
		loc, ok := h.cache.Get(digest)
		if !ok {
			log.Printf("blob not found: %s (not in v1.1.0 global store or v1.0.0 cache)", digest)
			writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN",
				"blob not found in S3",
				fmt.Sprintf("digest %s not found at blobs/sha256/%s and not in cache", digest, encoded))
			return
		}
		fetchBucket = loc.Bucket
		key = loc.Key
		s3c, err = h.s3.ClientForBucket(ctx, fetchBucket)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		resp, err = s3c.GetObject(ctx, &s3.GetObjectInput{
			Bucket: &fetchBucket,
			Key:    &key,
		})
	}
	if err != nil {
		log.Printf("S3 GetObject %s/%s: %v", fetchBucket, key, err)
		writeOCIError(w, http.StatusNotFound, "BLOB_UNKNOWN", "blob not found in S3", digest)
		return
	}
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	if resp.ContentLength != nil {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", *resp.ContentLength))
	}
	w.WriteHeader(http.StatusOK)
	io.Copy(w, resp.Body)
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

// isNotFound returns true if the error is an S3 NoSuchKey or HTTP 404.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var noSuchKey *s3types.NoSuchKey
	if errors.As(err, &noSuchKey) {
		return true
	}
	var re interface{ HTTPStatusCode() int }
	if errors.As(err, &re) && re.HTTPStatusCode() == http.StatusNotFound {
		return true
	}
	return false
}

// getS3Object fetches an S3 object and returns its body as bytes.
func getS3Object(ctx context.Context, client *s3.Client, bucket, key string) ([]byte, error) {
	resp, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
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
