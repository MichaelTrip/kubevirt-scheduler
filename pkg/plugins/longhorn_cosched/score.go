package longhorn_cosched

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Score implements the ScorePlugin interface.
//
// If the pod has the co-scheduling annotation and a Longhorn share-manager pod
// is already running for one of its RWX PVCs, the node where the share-manager
// runs receives the maximum score (100). All other nodes receive 0.
//
// If the pod does not have the annotation, or no share-manager pod is found,
// all nodes receive 0 (neutral — the plugin is a no-op).
func (p *Plugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	if !isOptedIn(pod) || isMigrationTarget(pod) {
		return 0, nil
	}

	shareManagerNode, err := findShareManagerNode(ctx, p.clientset, p.dynClient, pod)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("error looking up share-manager pod: %v", err))
	}

	// No share-manager found yet — neutral score for all nodes.
	if shareManagerNode == "" {
		return 0, nil
	}

	// Give the share-manager's node the maximum score.
	if nodeName == shareManagerNode {
		return framework.MaxNodeScore, nil
	}

	return 0, nil
}

// ScoreExtensions returns nil because this plugin does not implement score extensions.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
