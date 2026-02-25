# kubevirt-scheduler

A custom Kubernetes scheduler that co-locates **KubeVirt VM pods** with their **Longhorn RWX share-manager pods** on the same node, eliminating cross-node NFS traffic.

## Problem

When KubeVirt VMs use Longhorn RWX volumes, Longhorn creates a `share-manager` pod (NFS server) per PVC in the `longhorn-system` namespace. By default this pod is scheduled independently of the VM, causing NFS traffic to cross node boundaries — adding latency and network overhead.

## Solution

`kubevirt-scheduler` is a custom Kubernetes scheduler built with the [Scheduling Framework](https://kubernetes.io/docs/concepts/scheduling-eviction/scheduling-framework/). It implements two extension points:

| Plugin | Behaviour |
|---|---|
| **Filter** | If a share-manager pod is already running for the VM's PVC, only the node where it runs passes the filter |
| **Score** | The share-manager's node receives the maximum score (100); all others receive 0 |

The scheduler is **opt-in** via a pod annotation — only pods that explicitly request it are affected.

## How It Works

```
VM Pod created
  │
  ├─ No annotation → default scheduling (no-op)
  │
  └─ annotation kubevirt-scheduler/co-schedule: "true"
       │
       ├─ No share-manager pod found yet
       │    └─ VM schedules freely on best node
       │
       └─ share-manager pod running on node-X
            └─ Filter: only node-X passes
               Score: node-X gets score 100
               → VM scheduled on node-X (co-located)
```

## Installation

### 1. Build and push the image

```bash
docker build -t ghcr.io/<your-org>/kubevirt-scheduler:latest .
docker push ghcr.io/<your-org>/kubevirt-scheduler:latest
```

Update the `image:` field in [`manifests/deployment.yaml`](manifests/deployment.yaml) to match.

### 2. Apply the manifests

```bash
kubectl apply -f manifests/rbac.yaml
kubectl apply -f manifests/scheduler-config.yaml
kubectl apply -f manifests/deployment.yaml
```

### 3. Verify the scheduler is running

```bash
kubectl -n kube-system get pods -l app=kubevirt-scheduler
```

## Usage

### Opt-in a VirtualMachine

Add the annotation and set `schedulerName` in your `VirtualMachine` spec:

```yaml
apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: my-vm
spec:
  template:
    metadata:
      annotations:
        kubevirt-scheduler/co-schedule: "true"   # opt-in to co-scheduling
    spec:
      schedulerName: kubevirt-scheduler           # use our custom scheduler
      domain:
        # ... your VM spec ...
      volumes:
        - name: datavol
          persistentVolumeClaim:
            claimName: my-rwx-pvc                # must be a Longhorn RWX PVC
```

KubeVirt propagates annotations from the `VirtualMachine` template to the `virt-launcher` pod automatically.

### How share-manager pods are discovered

Longhorn names share-manager pods as:
```
longhorn-system/share-manager-<pvc-name>
```

The plugin:
1. Lists all PVCs referenced by the VM pod
2. Checks each PVC is `ReadWriteMany`
3. Looks up `longhorn-system/share-manager-<pvc-name>`
4. If found and `Running`, uses that pod's node for Filter/Score

## Configuration

| Item | Value |
|---|---|
| Opt-in annotation key | `kubevirt-scheduler/co-schedule` |
| Opt-in annotation value | `true` |
| Scheduler name | `kubevirt-scheduler` |
| Share-manager namespace | `longhorn-system` |
| Share-manager pod name pattern | `share-manager-<pvc-name>` |

## Development

### Prerequisites

- Go 1.24+
- Access to a Kubernetes cluster with KubeVirt and Longhorn installed

### Build

```bash
go build -o kubevirt-scheduler ./cmd/scheduler
```

### Test

```bash
go test ./pkg/...
```

### Project Structure

```
kubevirt-scheduler/
├── cmd/scheduler/main.go                        # Entry point
├── pkg/plugins/longhorn_cosched/
│   ├── plugin.go                                # Plugin registration & constants
│   ├── filter.go                                # Filter extension point
│   ├── score.go                                 # Score extension point
│   ├── sharemanager.go                          # Share-manager pod lookup
│   └── plugin_test.go                           # Unit tests
├── manifests/
│   ├── rbac.yaml                                # RBAC permissions
│   ├── scheduler-config.yaml                    # KubeSchedulerConfiguration
│   └── deployment.yaml                          # Scheduler Deployment
└── Dockerfile
```

## License

See [LICENSE](LICENSE).
