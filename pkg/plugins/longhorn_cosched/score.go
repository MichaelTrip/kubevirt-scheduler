package longhorn_cosched

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/klog/v2"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// Score implements the ScorePlugin interface.
//
// If the pod has the co-scheduling annotation and a Longhorn share-manager pod
// is already running for one of its RWX PVCs, the node where the share-manager
// runs receives the maximum score (100). All other nodes receive 0.
//
// If the pod does not have the annotation, is a migration target, or no
// share-manager pod is found, all nodes receive 0 (neutral — the plugin is a no-op).
func (p *Plugin) Score(ctx context.Context, state *framework.CycleState, pod *corev1.Pod, nodeName string) (int64, *framework.Status) {
	podKey := klog.KObj(pod)

	if !isOptedIn(pod) {
		klog.V(5).InfoS("LonghornCoSchedule/Score: pod not opted in, skipping", "pod", podKey, "node", nodeName)
		return 0, nil
	}

	if isMigrationTarget(pod) {
		klog.V(4).InfoS("LonghornCoSchedule/Score: migration target pod, skipping (KubeVirt migration controller handles placement)",
			"pod", podKey,
			"node", nodeName,
			"migrationJobUID", pod.Labels[MigrationTargetLabel],
		)
		return 0, nil
	}

	shareManagerNode, err := findShareManagerNode(ctx, p.clientset, p.dynClient, pod)
	if err != nil {
		klog.ErrorS(err, "LonghornCoSchedule/Score: error looking up share-manager", "pod", podKey, "node", nodeName)
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("error looking up share-manager pod: %v", err))
	}

	// No share-manager found yet — neutral score for all nodes.
	if shareManagerNode == "" {
		klog.V(4).InfoS("LonghornCoSchedule/Score: no share-manager found, scoring 0",
			"pod", podKey,
			"node", nodeName,
		)
		return 0, nil
	}

	// Give the share-manager's node the maximum score.
	if nodeName == shareManagerNode {
		klog.V(4).InfoS("LonghornCoSchedule/Score: node matches share-manager, scoring max",
			"pod", podKey,
			"node", nodeName,
			"shareManagerNode", shareManagerNode,
			"score", framework.MaxNodeScore,
		)
		return framework.MaxNodeScore, nil
	}

	klog.V(4).InfoS("LonghornCoSchedule/Score: node does not match share-manager, scoring 0",
		"pod", podKey,
		"node", nodeName,
		"shareManagerNode", shareManagerNode,
	)
	return 0, nil
}

// ScoreExtensions returns nil because this plugin does not implement score extensions.
func (p *Plugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}
