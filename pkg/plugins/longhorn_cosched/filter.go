package longhorn_cosched

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Filter implements the FilterPlugin interface.
//
// If the pod has the co-scheduling annotation and a Longhorn share-manager pod
// is already running for one of its RWX PVCs, only the node where the
// share-manager is running will pass the filter. All other nodes are rejected
// with an Unschedulable status.
//
// If the pod does not have the annotation, or no share-manager pod is found,
// all nodes pass (the plugin is a no-op).
func (p *Plugin) Filter(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeInfo *framework.NodeInfo) *framework.Status {
	if !isOptedIn(pod) {
		return nil
	}

	node := nodeInfo.Node()
	if node == nil {
		return framework.NewStatus(framework.Error, "node not found")
	}

	shareManagerNode, err := findShareManagerNode(ctx, p.clientset, p.dynClient, pod)
	if err != nil {
		return framework.NewStatus(framework.Error, fmt.Sprintf("error looking up share-manager pod: %v", err))
	}

	// No share-manager found yet — allow all nodes (VM schedules freely).
	if shareManagerNode == "" {
		return nil
	}

	// Share-manager is running on a specific node — only allow that node.
	if node.Name != shareManagerNode {
		return framework.NewStatus(
			framework.Unschedulable,
			fmt.Sprintf("node %q rejected: Longhorn share-manager pod is running on node %q", node.Name, shareManagerNode),
		)
	}

	return nil
}
