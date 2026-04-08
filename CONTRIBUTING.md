# Contributing to s3lo-operator

Thanks for your interest in contributing!

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/YOUR-USERNAME/s3lo-operator.git`
3. Create a branch: `git checkout -b my-feature`
4. Make your changes
5. Run tests: `go test ./...`
6. Commit and push
7. Open a Pull Request

## Development

### Prerequisites

- Go 1.22+
- Docker (for building images)
- Helm 3 (for chart development)
- Access to a Kubernetes cluster with containerd (for integration testing)

### Build

```bash
make build
```

### Test

```bash
make test
```

### Docker Image

```bash
make docker
```

### Project Structure

```
s3lo-operator/
├── cmd/s3lo-proxy/     # Entry point
├── pkg/
│   ├── proxy/          # OCI Distribution API proxy (handlers, server, cache)
│   └── setup/          # Containerd hosts.toml configuration
├── deploy/helm/        # Helm chart
└── Dockerfile
```

### How the Proxy Works

The proxy implements 3 OCI Distribution Spec endpoints:

| Endpoint | Purpose |
|----------|---------|
| `GET /v2/` | API version check |
| `GET /v2/<bucket>/<image>/manifests/<ref>` | Fetch manifest from S3 |
| `GET /v2/<bucket>/<image>/blobs/<digest>` | Fetch layer blob from S3 |

Containerd is configured via `hosts.toml` to route all `s3/*` image pulls to `localhost:5732`.

### Code Style

- Follow standard Go conventions
- Run `go vet ./...` before submitting
- Write tests for new functionality
- Keep the proxy lightweight — no unnecessary dependencies

## Reporting Issues

- Use GitHub Issues
- Include Kubernetes version, EKS version, and containerd version
- Include pod logs from s3lo-proxy DaemonSet
- Include the full error message and steps to reproduce

## Pull Requests

- Keep PRs focused on a single change
- Update tests for new functionality
- Update Helm chart if changing configuration
- Update README if adding user-facing features
