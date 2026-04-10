# s3lo-operator

[![CI](https://github.com/OuFinx/s3lo-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/OuFinx/s3lo-operator/actions/workflows/ci.yml)
[![Release](https://github.com/OuFinx/s3lo-operator/actions/workflows/release.yml/badge.svg)](https://github.com/OuFinx/s3lo-operator/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Kubernetes DaemonSet that enables pulling container images directly from AWS S3. Runs a lightweight OCI-compatible proxy on each node — containerd pulls images as if from a regular registry.

## How It Works

```
Pod: image: s3.local/my-bucket/myapp:v1.0
  → containerd → hosts.toml → localhost:5732
    → s3lo-proxy (OCI registry API)
      → S3 GetObject
    → containerd stores & mounts layers
  → container starts
```

The proxy implements the OCI Distribution Spec read endpoints and translates them into S3 operations. Containerd is configured via native `hosts.toml` — no patching, no containerd restart needed.

## Quick Start

### 1. Install s3lo CLI

Push images to S3 using [s3lo](https://github.com/OuFinx/s3lo):

```bash
curl -sSL https://raw.githubusercontent.com/OuFinx/s3lo/main/install.sh | sh
```

### 2. Push an image to S3

```bash
docker pull --platform linux/amd64 myapp:v1.0
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

> **Important:** Push linux/amd64 images for EKS. If building on Apple Silicon, use `docker build --platform linux/amd64`.

### 3. Deploy s3lo-operator

```bash
helm install s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --create-namespace \
  --set image.tag=1.0.0
```

### 4. Configure AWS Access

Create an IAM role with S3 read access and associate it with the s3lo-proxy service account using [EKS Pod Identity](https://docs.aws.amazon.com/eks/latest/userguide/pod-identities.html):

```bash
aws eks create-pod-identity-association \
  --cluster-name my-cluster \
  --namespace s3lo \
  --service-account-name s3lo-proxy \
  --role-arn arn:aws:iam::123456789:role/s3lo-role
```

#### Minimum IAM Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "s3:GetObject",
        "s3:HeadObject",
        "s3:ListBucket",
        "s3:GetBucketLocation"
      ],
      "Resource": [
        "arn:aws:s3:::YOUR-BUCKET",
        "arn:aws:s3:::YOUR-BUCKET/*"
      ]
    }
  ]
}
```

#### Terraform Example

```hcl
data "aws_iam_policy_document" "s3lo_access" {
  statement {
    effect  = "Allow"
    actions = ["s3:GetObject", "s3:HeadObject", "s3:ListBucket", "s3:GetBucketLocation"]
    resources = [
      "arn:aws:s3:::my-bucket",
      "arn:aws:s3:::my-bucket/*",
    ]
  }
}

module "s3lo_pod_identity" {
  source  = "terraform-aws-modules/eks-pod-identity/aws"
  version = "1.9.0"

  name                    = "s3lo"
  attach_custom_policy    = true
  source_policy_documents = [data.aws_iam_policy_document.s3lo_access.json]

  association_defaults = {
    namespace       = "s3lo"
    service_account = "s3lo-proxy"
  }

  associations = {
    s3lo = {
      cluster_name = "my-cluster"
    }
  }
}
```

### 5. Use S3 images in your Pods

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: myapp
spec:
  containers:
    - name: myapp
      image: s3.local/my-bucket/myapp:v1.0
```

No CRDs, no annotations, no init containers. Just change the image reference.

## Image Reference Format

```
s3.local/<bucket>/<image>:<tag>
```

Examples:
```yaml
image: s3.local/my-bucket/myapp:v1.0
image: s3.local/my-bucket/api/backend:latest
image: s3.local/my-bucket/org/frontend:sha-abc123
```

## Prerequisites

- EKS cluster with containerd 1.5+ (EKS 1.24+)
- containerd `config_path` includes `/etc/containerd/certs.d` (EKS default)
- S3 bucket with images pushed via [s3lo](https://github.com/OuFinx/s3lo)
- EKS Pod Identity for S3 access

## Helm Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Docker image | `ghcr.io/oufinx/s3lo-operator` |
| `image.tag` | Image tag | Chart appVersion |
| `proxy.port` | Proxy listen port | `5732` |
| `serviceAccount.name` | ServiceAccount name | `s3lo-proxy` |
| `serviceAccount.annotations` | SA annotations (for IRSA if needed) | `{}` |
| `resources.requests.cpu` | CPU request | `50m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `nodeSelector` | Node selector | `{}` |
| `tolerations` | Tolerations | `[]` |

## Architecture

```
┌─────────────────────────────────────────────┐
│                  EKS Node                    │
│                                              │
│  ┌──────────┐    ┌────────────────────────┐  │
│  │ kubelet  │───▶│     containerd         │  │
│  └──────────┘    │  ┌──────────────────┐  │  │
│                  │  │ hosts.toml       │  │  │
│                  │  │ s3.local →       │  │  │
│                  │  │  localhost:5732   │  │  │
│                  │  └────────┬─────────┘  │  │
│                  └───────────┼────────────┘  │
│                              │               │
│                   ┌──────────▼──────────┐    │
│                   │    s3lo-proxy       │    │
│                   │   (DaemonSet Pod)   │    │
│                   │                     │    │
│                   │  GET /v2/.../       │    │
│                   │  manifests/blobs    │    │
│                   └──────────┬──────────┘    │
│                              │               │
└──────────────────────────────┼───────────────┘
                               │
                    ┌──────────▼──────────┐
                    │      AWS S3         │
                    │                     │
                    │  bucket/image/tag/  │
                    │  ├── manifest.json  │
                    │  ├── config.json    │
                    │  └── blobs/sha256/  │
                    └─────────────────────┘
```

## Troubleshooting

### Image pull fails with "not found"

Check proxy logs:
```bash
kubectl logs -n s3lo daemonset/s3lo-operator
```

Verify the image exists in S3:
```bash
s3lo list s3://my-bucket/
s3lo inspect s3://my-bucket/myapp:v1.0
```

### "exec format error" when container starts

You pushed an image built for the wrong platform. Re-push with linux/amd64:
```bash
docker pull --platform linux/amd64 myapp:v1.0
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

### hosts.toml not picked up by containerd

Verify containerd has `config_path` set:
```bash
kubectl debug node/<NODE> -it --image=busybox --profile=sysadmin -- \
  grep config_path /host/etc/containerd/config.toml
```

Expected: `config_path = "/etc/containerd/certs.d:/etc/docker/certs.d"`

### Pod stuck in ImagePullBackOff with "docker.io" in error

Image reference must use `s3.local/` prefix, not `s3/`:
```yaml
# Wrong
image: s3/my-bucket/myapp:v1.0

# Correct
image: s3.local/my-bucket/myapp:v1.0
```

## See Also

- [s3lo](https://github.com/OuFinx/s3lo) — CLI tool for pushing/pulling images to S3

## Contributing

Contributions are welcome! See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

[MIT](LICENSE)
