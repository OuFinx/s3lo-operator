# s3lo-operator

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Kubernetes DaemonSet that enables pulling container images directly from AWS S3. Runs a lightweight OCI-compatible proxy on each node — containerd pulls images as if from a regular registry.

## How It Works

```
Pod: image: s3/my-bucket/myapp:v1.0
  → containerd → hosts.toml → localhost:5732
    → s3lo-proxy (OCI registry API)
      → S3 GetObject
    → containerd stores & mounts layers
  → container starts
```

The proxy implements the OCI Distribution Spec read endpoints and translates them into S3 operations. Containerd is configured via native `hosts.toml` — no patching, no restart needed.

## Prerequisites

- EKS cluster with containerd (1.5+)
- S3 bucket with images pushed via [s3lo](https://github.com/OuFinx/s3lo)
- EKS Pod Identity configured for S3 access

## Install

```bash
helm install s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --create-namespace
```

## Configure AWS Access

```bash
aws eks create-pod-identity-association \
  --cluster-name my-cluster \
  --namespace s3lo \
  --service-account-name s3lo-proxy \
  --role-arn arn:aws:iam::123456789:role/s3lo-role
```

### Minimum IAM Policy

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:HeadObject", "s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": ["arn:aws:s3:::YOUR-BUCKET", "arn:aws:s3:::YOUR-BUCKET/*"]
    }
  ]
}
```

## Usage

Push images with [s3lo](https://github.com/OuFinx/s3lo):

```bash
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

Use in Kubernetes:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: myapp
spec:
  containers:
    - name: myapp
      image: s3/my-bucket/myapp:v1.0
```

No CRDs, no annotations, no init containers.

## Architecture

- **s3lo-proxy**: HTTP server implementing OCI Distribution API (3 endpoints)
- **DaemonSet**: Deploys proxy to every node, writes containerd `hosts.toml`
- **hosts.toml**: Native containerd config — routes `s3/*` to local proxy

## See Also

- [s3lo](https://github.com/OuFinx/s3lo) — CLI tool for pushing/pulling images to S3

## License

[MIT](LICENSE)
