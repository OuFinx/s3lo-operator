---
name: Bug Report
about: Report a bug in s3lo-operator
title: ""
labels: bug
assignees: ""
---

## Describe the bug

A clear description of the bug.

## To reproduce

1. Deploy s3lo-operator with `helm install ...`
2. Create a Pod with `image: s3/...`
3. See error

## Expected behavior

What you expected to happen.

## Environment

- Kubernetes version: [e.g. 1.30]
- EKS version: [e.g. EKS 1.30]
- containerd version: [output of `containerd --version` on node]
- s3lo-operator version: [Helm chart version or image tag]
- AWS region:

## Logs

```
kubectl logs -n s3lo daemonset/s3lo-operator
```

## Pod Events

```
kubectl describe pod <failing-pod>
```
