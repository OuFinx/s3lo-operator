# s3lo-operator Roadmap

## v1.1.0 — Global Layer Deduplication ✓

- [x] Resolve blobs from global bucket-level path (`bucket/blobs/sha256/`)
- [x] Fallback to v1.0.0 layout for backward compatibility
- [x] Meaningful OCI error messages for missing images

## v1.2.0 — Multi-Architecture Images ✓

- [x] Serve OCI Image Index for multi-arch images
- [x] Handle Accept header for manifest content negotiation

## v1.3.0 — Pull-Through Cache + Observability + Distribution ✓

- [x] Local disk cache for manifests
- [x] FIFO eviction for manifest cache
- [x] Cache hit/miss/error metrics
- [x] Prometheus metrics endpoint (`/metrics` on port 9090)
- [x] Rate limiting for S3 requests (`S3LO_S3_MAX_CONCURRENT`)
- [x] Readiness probe checks S3 connectivity (`S3LO_HEALTH_BUCKET`)
- [x] Support non-EKS Kubernetes clusters (GKE, AKS, k3s, Minio, Ceph)
- [x] Update Helm chart for all new features
- [x] Publish Helm chart to GHCR as OCI artifact

## v2.0.0 — Security ✓

- [x] Verify image signatures before serving
