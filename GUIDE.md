# s3lo-operator Guide

Complete reference for s3lo-operator: architecture, configuration, AWS setup, and troubleshooting.

---

## Table of Contents

- [How It Works](#how-it-works)
- [Architecture](#architecture)
- [Image Reference Format](#image-reference-format)
- [Installation](#installation)
- [AWS Access Configuration](#aws-access-configuration)
- [Helm Configuration Reference](#helm-configuration-reference)
- [containerd Integration](#containerd-integration)
- [OCI Distribution API](#oci-distribution-api)
- [v1.0.0 and v1.1.0 Image Compatibility](#v100-and-v110-image-compatibility)
- [Health and Readiness](#health-and-readiness)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)

---

## How It Works

s3lo-operator is a Kubernetes DaemonSet that runs one proxy pod per node. The proxy implements the OCI Distribution Spec (a subset of the Docker Registry v2 API) and translates image pull requests into S3 operations.

When a pod specifies `image: s3.local/my-bucket/myapp:v1.0`, the flow is:

1. kubelet tells containerd to pull the image.
2. containerd looks up `s3.local` in its `hosts.toml` configuration and routes the request to `localhost:5732`.
3. The s3lo-proxy receives a `GET /v2/my-bucket/myapp/manifests/v1.0` request.
4. The proxy fetches `manifests/myapp/v1.0/manifest.json` from S3 bucket `my-bucket`.
5. containerd uses the manifest to request each blob via `GET /v2/my-bucket/myapp/blobs/sha256:<digest>`.
6. The proxy streams each blob directly from S3 to containerd.
7. containerd assembles the image layers and starts the container.

No image is stored on the node beyond what containerd caches normally. No sidecar, no init container, no custom CRD.

---

## Architecture

```
EKS Node
  kubelet
    -> containerd
         /etc/containerd/certs.d/s3.local/hosts.toml
           server = "http://localhost:5732"
         -> s3lo-proxy (DaemonSet Pod, hostNetwork: true)
              GET /v2/...
              -> AWS S3
                   manifests/<image>/<tag>/manifest.json
                   blobs/sha256/<digest>
```

**Key design points:**

- **DaemonSet with hostNetwork** - one proxy per node, listening on the node's loopback interface at port 5732. Using `hostNetwork: true` means containerd (running outside any pod network) can reach `localhost:5732`.

- **No containerd restart** - the `hosts.toml` file is written to `/etc/containerd/certs.d/s3.local/` on startup. containerd reads `hosts.toml` on each image pull request, so no restart is needed.

- **Stateless proxy** - the proxy holds a small in-memory cache for manifest digests (used when containerd requests a manifest by digest rather than by tag), but all image data comes from S3 on each pull.

- **IAM via Pod Identity** - the DaemonSet pod has a service account (`s3lo-proxy`) which is associated with an IAM role via EKS Pod Identity or IRSA. No credentials are stored in the cluster.

---

## Image Reference Format

```
s3.local/<bucket>/<image>:<tag>
```

Where:
- `s3.local` - the registry hostname (fixed, routes via hosts.toml)
- `<bucket>` - your S3 bucket name
- `<image>` - image name, may contain slashes for nested paths
- `<tag>` - image tag

**Examples:**

```yaml
# Simple image name
image: s3.local/my-bucket/myapp:v1.0

# Nested path (mirrors org/repo convention)
image: s3.local/my-bucket/org/frontend:latest

# SHA-based tag from CI
image: s3.local/my-bucket/api/backend:sha-abc1234

# Latest
image: s3.local/my-bucket/worker:latest
```

The `<image>` part of the URL maps directly to the `<image>` part of the S3 key path. If you pushed with `s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0`, use `s3.local/my-bucket/myapp:v1.0` in your pod spec.

**Why s3.local?**

containerd needs a valid registry hostname to route through `hosts.toml`. The `.local` domain ensures it does not accidentally resolve to a real DNS name. The proxy listens on `localhost:5732`, and `hosts.toml` maps `s3.local` to that address.

---

## Installation

### Prerequisites

- EKS cluster running Kubernetes 1.24+ (containerd 1.5+)
- containerd configured with `config_path` pointing to `/etc/containerd/certs.d` (default on EKS 1.24+)
- S3 bucket with images pushed via [s3lo](https://github.com/OuFinx/s3lo)

### Deploy with Helm

```bash
helm install s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --create-namespace
```

To specify a version explicitly:
```bash
helm install s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --create-namespace \
  --set image.tag=1.1.0
```

### Verify the DaemonSet is running

```bash
kubectl get daemonset -n s3lo
kubectl get pods -n s3lo -o wide
```

Every node should have one s3lo-operator pod in `Running` state.

### Verify hosts.toml was written

```bash
# Check on a specific node
kubectl debug node/<node-name> -it --image=busybox --profile=sysadmin -- \
  cat /host/etc/containerd/certs.d/s3.local/hosts.toml
```

Expected output:
```toml
server = "http://localhost:5732"

[host."http://localhost:5732"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
```

---

## AWS Access Configuration

The proxy pod needs S3 read access to the bucket(s) containing your images. The recommended approach is EKS Pod Identity.

### EKS Pod Identity (recommended)

EKS Pod Identity is the modern replacement for IRSA. It does not require annotating the service account.

**Step 1: Create the IAM role**

The role needs a trust policy that allows EKS Pod Identity to assume it:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Service": "pods.eks.amazonaws.com"
      },
      "Action": [
        "sts:AssumeRole",
        "sts:TagSession"
      ]
    }
  ]
}
```

**Step 2: Attach the S3 read policy**

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
        "arn:aws:s3:::my-bucket",
        "arn:aws:s3:::my-bucket/*"
      ]
    }
  ]
}
```

**Step 3: Create the Pod Identity Association**

```bash
aws eks create-pod-identity-association \
  --cluster-name my-cluster \
  --namespace s3lo \
  --service-account-name s3lo-proxy \
  --role-arn arn:aws:iam::123456789:role/s3lo-role
```

**Terraform:**

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
    cluster = {
      cluster_name = "my-cluster"
    }
  }
}
```

### IRSA (Legacy)

If your cluster does not support EKS Pod Identity, use IRSA (IAM Roles for Service Accounts).

**Step 1: Create the IAM role with OIDC trust**

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "Federated": "arn:aws:iam::123456789:oidc-provider/oidc.eks.us-east-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE"
      },
      "Action": "sts:AssumeRoleWithWebIdentity",
      "Condition": {
        "StringEquals": {
          "oidc.eks.us-east-1.amazonaws.com/id/EXAMPLED539D4633E53DE1B71EXAMPLE:sub": "system:serviceaccount:s3lo:s3lo-proxy"
        }
      }
    }
  ]
}
```

**Step 2: Annotate the service account via Helm values**

```yaml
serviceAccount:
  create: true
  name: s3lo-proxy
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789:role/s3lo-role
```

```bash
helm upgrade s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::123456789:role/s3lo-role
```

### Multiple buckets

The IAM role can grant access to multiple buckets:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject", "s3:HeadObject", "s3:ListBucket", "s3:GetBucketLocation"],
      "Resource": [
        "arn:aws:s3:::bucket-a",
        "arn:aws:s3:::bucket-a/*",
        "arn:aws:s3:::bucket-b",
        "arn:aws:s3:::bucket-b/*"
      ]
    }
  ]
}
```

All buckets in the image reference are handled by the same proxy - just change `<bucket>` in the pod image reference.

---

## Helm Configuration Reference

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Proxy image repository | `ghcr.io/oufinx/s3lo-operator` |
| `image.tag` | Proxy image tag | chart `appVersion` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `proxy.port` | Port the proxy listens on | `5732` |
| `serviceAccount.create` | Whether to create the ServiceAccount | `true` |
| `serviceAccount.name` | ServiceAccount name | `s3lo-proxy` |
| `serviceAccount.annotations` | SA annotations (use for IRSA) | `{}` |
| `resources.requests.cpu` | CPU request | `50m` |
| `resources.requests.memory` | Memory request | `64Mi` |
| `resources.limits.cpu` | CPU limit | `200m` |
| `resources.limits.memory` | Memory limit | `128Mi` |
| `nodeSelector` | Node selector labels | `{}` |
| `tolerations` | Pod tolerations | `[]` |

### Changing the proxy port

If port 5732 is already in use on your nodes:

```bash
helm install s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --create-namespace \
  --set proxy.port=5999
```

The `hosts.toml` written to nodes will use the configured port automatically.

### Running on specific node groups

To restrict the DaemonSet to specific nodes (e.g. only application nodes, not system nodes):

```yaml
nodeSelector:
  role: application

tolerations:
  - key: node-role.kubernetes.io/application
    operator: Exists
    effect: NoSchedule
```

---

## containerd Integration

### How hosts.toml works

containerd uses a `hosts.toml` file per registry hostname to configure how it contacts that registry. s3lo-operator writes:

```
/etc/containerd/certs.d/s3.local/hosts.toml
```

Content:
```toml
server = "http://localhost:5732"

[host."http://localhost:5732"]
  capabilities = ["pull", "resolve"]
  skip_verify = true
```

- `server` - the primary URL for the registry
- `capabilities` - declares this host can pull and resolve manifests (no push)
- `skip_verify = true` - skips TLS verification (the proxy uses plain HTTP on localhost)

### When is hosts.toml applied

containerd reads `hosts.toml` on each image pull - no restart is required. As soon as s3lo-operator writes the file, subsequent pulls from `s3.local` will route to the proxy.

The file is written by the proxy on startup. If the DaemonSet pod is restarted, the file is rewritten.

### containerd config_path requirement

For `hosts.toml` to be used, containerd must have `config_path` set to a directory that includes `/etc/containerd/certs.d`. EKS 1.24+ sets this by default.

Verify:
```bash
kubectl debug node/<node-name> -it --image=busybox --profile=sysadmin -- \
  grep config_path /host/etc/containerd/config.toml
```

Expected output (one of these forms):
```
config_path = "/etc/containerd/certs.d"
config_path = "/etc/containerd/certs.d:/etc/docker/certs.d"
```

If `config_path` is not set or points elsewhere, containerd will not pick up `hosts.toml` and pulls from `s3.local` will fail.

---

## OCI Distribution API

s3lo-operator implements a subset of the OCI Distribution Spec sufficient for containerd to pull images.

### Implemented endpoints

| Method | Path | Description |
|--------|------|-------------|
| GET | `/v2/` | API version check |
| GET | `/v2/<bucket>/<image>/manifests/<ref>` | Fetch image manifest by tag or digest |
| GET | `/v2/<bucket>/<image>/blobs/<digest>` | Stream a blob (layer or config) |
| GET | `/healthz` | Health check |
| GET | `/readyz` | Readiness check |

### Manifest resolution

When containerd requests a manifest by tag (e.g. `manifests/v1.0`), the proxy:

1. Tries S3 key `manifests/<image>/<tag>/manifest.json` (v1.1.0 layout).
2. If not found (HTTP 404 or S3 NoSuchKey), tries `<image>/<tag>/manifest.json` (v1.0.0 layout).
3. Returns the manifest with the `Docker-Content-Digest` header set to `sha256:<hash>`.
4. Caches the manifest data by digest in memory.

When containerd subsequently requests the same manifest by digest (e.g. `manifests/sha256:abc...`), the proxy serves it from the in-memory cache. This cache is node-local and in-memory only - it is rebuilt from S3 on pod restart.

### Blob resolution

When containerd requests a blob by digest (e.g. `blobs/sha256:a1b2c3d4...`), the proxy:

1. Tries S3 key `blobs/sha256/<encoded-digest>` (v1.1.0 global store).
2. If not found, looks up the digest in the in-memory DigestCache (which was populated during the manifest fetch for v1.0.0 images).
3. Streams the blob body directly from S3 to containerd.

The blob is streamed - it is not buffered in the proxy. This means memory usage stays low even for large layers.

### Error format

Errors are returned in OCI Distribution Spec format:

```json
{
  "errors": [
    {
      "code": "MANIFEST_UNKNOWN",
      "message": "image not found in S3: s3://my-bucket/myapp:v1.0",
      "detail": "tried s3://my-bucket/manifests/myapp/v1.0/manifest.json and s3://my-bucket/myapp/v1.0/manifest.json"
    }
  ]
}
```

Error codes used:
- `MANIFEST_UNKNOWN` - manifest not found in S3 (wrong image name, tag, or bucket)
- `BLOB_UNKNOWN` - blob not found in S3 (corrupted push, deleted blob)

---

## v1.0.0 and v1.1.0 Image Compatibility

s3lo-operator supports both image storage layouts simultaneously. No configuration is needed.

### v1.1.0 layout (current)

Images pushed with s3lo v1.1.0+ use this layout:
```
s3://my-bucket/
  blobs/sha256/<digest>         <- all blobs here
  manifests/<image>/<tag>/
    manifest.json
    index.json
    oci-layout
```

The proxy fetches `manifests/<image>/<tag>/manifest.json` directly. Blobs are retrieved from `blobs/sha256/<digest>` without any cache lookup.

### v1.0.0 layout (legacy)

Images pushed with s3lo v1.0.0 use this layout:
```
s3://my-bucket/
  <image>/<tag>/
    manifest.json
    blobs/sha256/<digest>
```

The proxy falls back to `<image>/<tag>/manifest.json` when the v1.1.0 manifest key returns 404. It also caches the exact S3 path for each blob from the manifest (needed because the blob path includes the tag prefix in this layout).

### Migration path

You do not need to migrate images before upgrading to s3lo-operator v1.1.0. Existing pods continue to work. When you push new images with s3lo v1.1.0+, they will use the v1.1.0 layout automatically.

To convert old images to v1.1.0 layout (for deduplication benefits):
```bash
s3lo migrate s3://my-bucket/
```

---

## Health and Readiness

The proxy exposes two health endpoints:

- `GET /healthz` - always returns `200 ok` if the proxy is running
- `GET /readyz` - same, returns `200 ok` if the proxy is running

The Helm chart configures liveness and readiness probes using these endpoints.

Check proxy health manually:
```bash
# From inside the cluster
kubectl exec -n s3lo -it daemonset/s3lo-operator -- wget -qO- http://localhost:5732/healthz

# From a node (the proxy uses hostNetwork)
ssh <node>
curl http://localhost:5732/healthz
```

---

## Troubleshooting

### Pod stays in ImagePullBackOff

**Check the event message:**
```bash
kubectl describe pod <pod-name>
```

Look for the `Events` section at the bottom. The failure message from the proxy is passed back as the OCI error detail.

**Check proxy logs:**
```bash
kubectl logs -n s3lo daemonset/s3lo-operator
```

**Verify the image exists in S3:**
```bash
s3lo inspect s3://my-bucket/myapp:v1.0
```

**Verify the image reference format:**
```
s3.local/my-bucket/myapp:v1.0
         ^^^^^^^^^
         exact bucket name
```

The bucket name in the image reference must exactly match the S3 bucket name.

---

### "manifest unknown" error in logs

The proxy could not find the manifest in S3. Causes:

1. Wrong bucket name in the image reference.
2. Wrong image name or tag - verify with `s3lo list s3://my-bucket/`.
3. The image was never pushed to S3.
4. The image was pushed with a different s3lo version and layout detection failed (check the detail field in the error).

---

### "blob not found" error in logs

The manifest was found, but a blob referenced in it does not exist in S3. Causes:

1. The push was interrupted and some blobs were not uploaded.
2. `s3lo gc` deleted a blob that was still referenced (should not happen, but check gc logs).
3. Manual deletion of S3 objects.

Fix: re-push the image.
```bash
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

---

### "exec format error" when container starts

The image was built for the wrong architecture. EKS nodes typically run `linux/amd64`.

```bash
# Check what was pushed
s3lo inspect s3://my-bucket/myapp:v1.0
# Look for: OS/Arch: linux/arm64 (wrong for x86 EKS)

# Re-push the correct platform
docker pull --platform linux/amd64 myapp:v1.0
s3lo push myapp:v1.0 s3://my-bucket/myapp:v1.0
```

---

### hosts.toml not being used

**Verify config_path:**
```bash
kubectl debug node/<node-name> -it --image=busybox --profile=sysadmin -- \
  grep config_path /host/etc/containerd/config.toml
```

If not set or pointing elsewhere, containerd is ignoring `hosts.toml`. This is a containerd configuration issue, not an s3lo-operator issue. For EKS clusters older than 1.24, you may need to upgrade the node group.

**Verify the file exists:**
```bash
kubectl debug node/<node-name> -it --image=busybox --profile=sysadmin -- \
  cat /host/etc/containerd/certs.d/s3.local/hosts.toml
```

If the file is missing, the s3lo-operator pod may not be running on this node, or it failed to write the file (check pod logs).

---

### Pod is running but proxy is unreachable

Since the proxy uses `hostNetwork: true`, it should be reachable at `localhost:5732` from the node.

**Check the proxy is listening:**
```bash
kubectl debug node/<node-name> -it --image=busybox --profile=sysadmin -- \
  wget -qO- http://localhost:5732/healthz
```

If this fails, the proxy pod on that node may be crashed or restarting. Check pod status:
```bash
kubectl get pods -n s3lo -o wide
kubectl logs -n s3lo <pod-name-on-that-node>
```

---

### S3 access denied errors

The proxy pod cannot reach S3. Check:

1. Pod Identity or IRSA is correctly associated with the `s3lo-proxy` service account.
2. The IAM role has the required S3 actions on the correct bucket ARN.
3. The bucket is in the same AWS region as the cluster (or cross-region access is configured).

**Test S3 access from the proxy pod:**
```bash
kubectl exec -n s3lo -it <pod-name> -- env | grep AWS
# Should show AWS_CONTAINER_CREDENTIALS_FULL_URI or similar (from Pod Identity)
```

---

### Image pull works on some nodes but not others

The DaemonSet ensures one proxy per node, but a pod on a new node may fail if:
- The proxy pod on that node is still starting up.
- The proxy pod on that node crashed.
- The node is tainted and the DaemonSet tolerations do not match.

Check which nodes are missing a proxy pod:
```bash
kubectl get pods -n s3lo -o wide
# Compare with: kubectl get nodes
```

If nodes are missing pods, check tolerations in the Helm values.

---

## FAQ

**Q: Does s3lo-operator require CRDs?**

No. There are no custom resources, no admission webhooks, and no cluster-level RBAC needed. s3lo-operator is just a DaemonSet that writes a `hosts.toml` file and runs an HTTP server.

**Q: Can I use s3lo-operator with non-EKS Kubernetes clusters?**

The current version is tested and designed for EKS. It should work on any Kubernetes cluster using containerd 1.5+ with `config_path` configured - but non-EKS support (GKE, AKS, k3s) is planned for v2.1.0. The main variable is whether containerd `config_path` includes the expected directory.

**Q: What happens if the s3lo-operator pod crashes?**

The `hosts.toml` file remains on the node - it was already written on startup and containerd will keep using it. However, the proxy itself is gone, so any new image pulls to `s3.local` will fail (connection refused) until the pod restarts. The DaemonSet controller will restart the pod automatically. Running containers are not affected.

**Q: Can multiple pods pull the same image at the same time?**

Yes. Each pod triggers an image pull on the node it is scheduled to. If two pods on the same node both need the same image and the image is not cached, containerd may deduplicate the pulls internally. The proxy handles concurrent requests - S3 GetObject is safe to call in parallel.

**Q: Does the proxy cache blobs on disk?**

Not in the current version. Every blob request goes directly to S3. Blob caching with LRU eviction is planned for v1.3.0. For frequently pulled images, the main caching layer is containerd's own image store.

**Q: How do I pull from multiple different buckets?**

Just use different bucket names in your image references. The proxy extracts the bucket name from the URL path on each request:
```yaml
# From bucket-a
image: s3.local/bucket-a/frontend:v1.0

# From bucket-b in the same pod spec
initContainers:
  - image: s3.local/bucket-b/tools:latest
```

Both will work as long as the `s3lo-proxy` IAM role has access to both buckets.

**Q: What port does the proxy use and can I change it?**

The proxy listens on port 5732 by default. Change it via the `proxy.port` Helm value. The `hosts.toml` written by the proxy will use whatever port is configured - no manual update needed.

**Q: Is the proxy connection to S3 encrypted?**

Yes. The proxy uses the AWS SDK which communicates with S3 over HTTPS. The connection from containerd to the proxy is plain HTTP on localhost (127.0.0.1), which is acceptable for loopback communication on the same node.

**Q: What happens when a node is replaced (e.g. in a managed node group)?**

New nodes receive a fresh DaemonSet pod. The proxy writes `hosts.toml` on startup, so the new node is configured automatically. No manual intervention needed.

**Q: Can I use digest-based image references?**

containerd typically resolves a tag to a digest first (via `manifests/<tag>`) and then pulls by digest. The proxy supports both. Tag-based requests fetch from S3. Digest-based requests are served from the in-memory manifest cache that was populated during the tag resolution. If the proxy restarts between tag resolution and digest fetch, the cache is cleared and the pull may fail - containerd will retry the full sequence.

**Q: How do I upgrade s3lo-operator?**

```bash
helm upgrade s3lo-operator deploy/helm/s3lo-operator \
  --namespace s3lo \
  --set image.tag=<new-version>
```

The DaemonSet rolls out one pod at a time. During rollout, existing cached images on nodes continue to work. New pulls on nodes where the pod is being updated may pause briefly.

**Q: Is there a metrics endpoint?**

Not yet. Prometheus metrics are planned for v1.4.0. For now, monitor the proxy via log output and the `/healthz` endpoint.

**Q: The proxy logs show "manifest digest not in cache - pull by tag first". What does this mean?**

containerd requested a manifest by digest (`sha256:...`) before ever requesting it by tag. This normally should not happen - containerd always resolves by tag first. If you see this, it may indicate containerd has a stale reference in its metadata store. Try `crictl rmi s3.local/my-bucket/myapp:v1.0` on the node to clear it, then re-pull.
