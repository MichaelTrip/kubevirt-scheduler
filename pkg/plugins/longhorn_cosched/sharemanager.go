package longhorn_cosched

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// shareManagerGVR is the GroupVersionResource for the Longhorn ShareManager CRD.
var shareManagerGVR = schema.GroupVersionResource{
	Group:    "longhorn.io",
	Version:  "v1beta2",
	Resource: "sharemanagers",
}

// findShareManagerNode looks up the node where the Longhorn share-manager for
// any of the RWX PVCs referenced by the given pod is running (or assigned).
//
// It first queries the ShareManager CRD (status.ownerID), which is set by
// Longhorn before the share-manager pod reaches Running phase. This avoids the
// chicken-and-egg problem where the pod hasn't started yet when the VM is
// being scheduled.
//
// If the CRD lookup yields nothing, it falls back to inspecting the
// share-manager pod directly (for compatibility with non-standard setups).
func findShareManagerNode(ctx context.Context, clientset kubernetes.Interface, dynClient dynamic.Interface, pod *corev1.Pod) (string, error) {
	pvcNames := collectPVCNames(pod)
	if len(pvcNames) == 0 {
		return "", nil
	}

	for _, pvcName := range pvcNames {
		node, err := getShareManagerNodeForPVC(ctx, clientset, dynClient, pod.Namespace, pvcName)
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

// getShareManagerNodeForPVC resolves the node for the share-manager of a
// specific PVC. It tries the ShareManager CRD first, then falls back to the
// share-manager pod.
func getShareManagerNodeForPVC(ctx context.Context, clientset kubernetes.Interface, dynClient dynamic.Interface, podNamespace, pvcName string) (string, error) {
	// Verify the PVC exists and is RWX.
	pvc, err := clientset.CoreV1().PersistentVolumeClaims(podNamespace).Get(ctx, pvcName, metav1.GetOptions{})
	if err != nil {
		return "", nil // PVC not found — skip silently.
	}

	if !isRWX(pvc) {
		return "", nil // Not RWX — Longhorn won't create a share-manager.
	}

	pvName := pvc.Spec.VolumeName
	if pvName == "" {
		return "", nil // PVC not yet bound.
	}

	// --- Primary: query the ShareManager CRD (status.ownerID) ---
	// The ShareManager CRD is named after the PV (e.g. pvc-<uid>) and lives in
	// longhorn-system. Longhorn sets status.ownerID as soon as it assigns the
	// share-manager to a node — well before the pod reaches Running phase.
	if dynClient != nil {
		node, err := getShareManagerNodeFromCRD(ctx, dynClient, pvName)
		if err != nil {
			// Log but don't fail — fall through to pod-based lookup.
			_ = fmt.Errorf("ShareManager CRD lookup failed for %s: %w", pvName, err)
		} else if node != "" {
			return node, nil
		}
	}

	// --- Fallback: inspect the share-manager pod directly ---
	return getShareManagerNodeFromPod(ctx, clientset, pvName)
}

// getShareManagerNodeFromCRD reads the ShareManager CRD for the given PV name
// and returns status.ownerID if the share-manager is in a running state.
func getShareManagerNodeFromCRD(ctx context.Context, dynClient dynamic.Interface, pvName string) (string, error) {
	obj, err := dynClient.Resource(shareManagerGVR).Namespace(LonghornNamespace).Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	// status.ownerID holds the node name assigned by Longhorn.
	status, ok := obj.Object["status"].(map[string]interface{})
	if !ok {
		return "", nil
	}

	ownerID, _ := status["ownerID"].(string)
	if ownerID == "" {
		return "", nil
	}

	// Only use the ownerID if the share-manager is in a usable state.
	// Longhorn states: stopped, starting, running, error
	state, _ := status["state"].(string)
	switch state {
	case "running", "starting":
		return ownerID, nil
	default:
		return "", nil
	}
}

// getShareManagerNodeFromPod looks up the share-manager pod for a PV and
// returns the node it is running on. Returns empty string if not found or
// not yet scheduled.
func getShareManagerNodeFromPod(ctx context.Context, clientset kubernetes.Interface, pvName string) (string, error) {
	shareManagerName := fmt.Sprintf("%s%s", ShareManagerPrefix, pvName)
	smPod, err := clientset.CoreV1().Pods(LonghornNamespace).Get(ctx, shareManagerName, metav1.GetOptions{})
	if err != nil {
		return "", nil // Pod doesn't exist yet — that's fine.
	}

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
