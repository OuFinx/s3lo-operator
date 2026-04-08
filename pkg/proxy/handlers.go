package proxy

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/finx/s3lo/pkg/oci"
	s3client "github.com/finx/s3lo/pkg/s3"
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
func (h *Handlers) HandleManifest(w http.ResponseWriter, r *http.Request) {
	bucket, image, ref, err := parseManifestPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	s3c, err := h.s3.ClientForBucket(ctx, bucket)
	if err != nil {
		log.Printf("S3 client error for bucket %s: %v", bucket, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fetch manifest from S3
	key := image + "/" + ref + "/manifest.json"
	resp, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &bucket,
		Key:    &key,
	})
	if err != nil {
		log.Printf("S3 GetObject %s/%s: %v", bucket, key, err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	defer resp.Body.Close()

	manifestData, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}

	// Parse manifest to cache blob digests → S3 keys
	manifest, err := oci.ParseManifest(manifestData)
	if err == nil {
		prefix := image + "/" + ref + "/blobs/sha256/"
		h.cache.Put(manifest.Config.Digest.String(), bucket, prefix+manifest.Config.Digest.Encoded())
		for _, layer := range manifest.Layers {
			h.cache.Put(layer.Digest.String(), bucket, prefix+layer.Digest.Encoded())
		}
	}

	w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(manifestData)))
	w.WriteHeader(http.StatusOK)
	w.Write(manifestData)
}

// HandleBlob handles GET /v2/<bucket>/<image>/blobs/<digest>
func (h *Handlers) HandleBlob(w http.ResponseWriter, r *http.Request) {
	_, digest, err := parseBlobPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	loc, ok := h.cache.Get(digest)
	if !ok {
		log.Printf("Blob not in cache: %s", digest)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	ctx := r.Context()
	s3c, err := h.s3.ClientForBucket(ctx, loc.Bucket)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: &loc.Bucket,
		Key:    &loc.Key,
	})
	if err != nil {
		log.Printf("S3 GetObject %s/%s: %v", loc.Bucket, loc.Key, err)
		http.Error(w, "not found", http.StatusNotFound)
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

// ensure context is used (avoid import error if not used elsewhere)
var _ = context.Background
