# s3lo-operator Roadmap

## v1.1.0 — Global Layer Deduplication

- [ ] Resolve blobs from global bucket-level path (`bucket/blobs/sha256/`)
- [ ] Fallback to v1.0.0 layout for backward compatibility
- [ ] Meaningful OCI error messages for missing images

## v1.2.0 — Multi-Architecture Images

- [ ] Serve OCI Image Index for multi-arch images
- [ ] Handle Accept header for manifest content negotiation

## v1.3.0 — Pull-Through Cache

- [ ] Local disk cache for blobs
- [ ] Local disk cache for manifests
- [ ] LRU eviction for disk cache
- [ ] Cache hit/miss metrics
- [ ] Rate limiting for S3 requests
- [ ] Update Helm chart for cache volume and metrics

## v1.4.0 — Observability

- [ ] Prometheus metrics endpoint
- [ ] Readiness probe checks S3 connectivity

## v1.5.0 — Distribution

- [ ] Publish Helm chart to GHCR as OCI artifact

## v2.0.0 — Security

- [ ] Verify image signatures before serving

## v2.1.0 — Multi-Platform Kubernetes

- [ ] Support non-EKS Kubernetes clusters (GKE, AKS, k3s)
