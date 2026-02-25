package longhorn_cosched

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// findShareManagerNode looks up the Longhorn share-manager pod for any of the
// RWX PVCs referenced by the given pod. It returns the node name where the
// share-manager pod is running, or an empty string if none is found.
//
// Longhorn names share-manager pods as: share-manager-<pvc-name>
// in the longhorn-system namespace.
func findShareManagerNode(ctx context.Context, clientset kubernetes.Interface, pod *corev1.Pod) (string, error) {
	// Collect all PVC names referenced by the pod.
	pvcNames := collectPVCNames(pod)
	if len(pvcNames) == 0 {
		return "", nil
	}

	for _, pvcName := range pvcNames {
		node, err := getShareManagerNodeForPVC(ctx, clientset, pod.Namespace, pvcName)
		if err != nil {
			return "", err
		}
		if node != "" {
			return node, nil
		}
	}

	return "", nil
}

// collectPVCNames returns all PVC names referenced by the pod's volumes.
func collectPVCNames(pod *corev1.Pod) []string {
	var names []string
	for _, vol := range pod.Spec.Volumes {
		if vol.PersistentVolumeClaim != nil {
			names = append(names, vol.PersistentVolumeClaim.ClaimName)
		}
	}
	return names
}

// getShareManagerNodeForPVC looks up the share-manager pod for a specific PVC
// and returns the node it is running on. Returns empty string if not found or
// not yet scheduled.
func getShareManagerNodeForPVC(ctx context.Context, clientset kubernetes.Interface, podNamespace, pvcName string) (string, error) {
	// First verify the PVC exists and is RWX (ReadWriteMany).
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(podNamespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		// PVC not found — skip silently.
		return "", nil
	}

	if !isRWX(pvc) {
		// Not an RWX PVC — Longhorn won't create a share-manager for it.
		return "", nil
	}

	// Look up the share-manager pod in the longhorn-system namespace.
	shareManagerName := fmt.Sprintf("%s%s", ShareManagerPrefix, pvcName)
	smPod, err := clientset.CoreV1().Pods(LonghornNamespace).Get(ctx, shareManagerName, metav1.GetOptions{})
	if err != nil {
		// Share-manager pod doesn't exist yet — that's fine.
		return "", nil
	}

	// Only use the node if the share-manager pod is actually running.
	if smPod.Status.Phase == corev1.PodRunning && smPod.Spec.NodeName != "" {
		return smPod.Spec.NodeName, nil
	}

	return "", nil
}

// isRWX returns true if the PVC has ReadWriteMany access mode.
func isRWX(pvc *corev1.PersistentVolumeClaim) bool {
	for _, mode := range pvc.Spec.AccessModes {
		if mode == corev1.ReadWriteMany {
			return true
		}
	}
	return false
}
