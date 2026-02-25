package longhorn_cosched

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

// makeVM creates a minimal pod that simulates a KubeVirt virt-launcher pod.
func makeVM(name, namespace string, annotated bool, pvcNames ...string) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PodSpec{},
	}
	if annotated {
		pod.Annotations = map[string]string{
			AnnotationKey: AnnotationValue,
		}
	}
	for _, pvc := range pvcNames {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: pvc,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvc,
				},
			},
		})
	}
	return pod
}

// makePVC creates a minimal RWX PVC.
func makePVC(name, namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteMany},
		},
	}
}

// makeShareManagerPod creates a minimal Longhorn share-manager pod.
func makeShareManagerPod(pvcName, nodeName string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShareManagerPrefix + pvcName,
			Namespace: LonghornNamespace,
		},
		Spec: corev1.PodSpec{
			NodeName: nodeName,
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
}

// makeNodeInfo creates a NodeInfo for testing.
func makeNodeInfo(name string) *framework.NodeInfo {
	ni := framework.NewNodeInfo()
	ni.SetNode(&corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
	})
	return ni
}

// --- isOptedIn tests ---

func TestIsOptedIn(t *testing.T) {
	tests := []struct {
		name      string
		pod       *corev1.Pod
		wantOptIn bool
	}{
		{
			name:      "no annotations",
			pod:       makeVM("vm", "default", false),
			wantOptIn: false,
		},
		{
			name:      "annotation present with correct value",
			pod:       makeVM("vm", "default", true),
			wantOptIn: true,
		},
		{
			name: "annotation present with wrong value",
			pod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{AnnotationKey: "false"},
				},
			},
			wantOptIn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isOptedIn(tt.pod)
			if got != tt.wantOptIn {
				t.Errorf("isOptedIn() = %v, want %v", got, tt.wantOptIn)
			}
		})
	}
}

// --- findShareManagerNode tests ---

func TestFindShareManagerNode(t *testing.T) {
	const (
		vmNamespace = "default"
		pvcName     = "my-rwx-pvc"
		targetNode  = "node-2"
	)

	tests := []struct {
		name     string
		pod      *corev1.Pod
		objects  []runtime.Object
		wantNode string
		wantErr  bool
	}{
		{
			name:     "no PVCs on pod",
			pod:      makeVM("vm", vmNamespace, true),
			objects:  nil,
			wantNode: "",
		},
		{
			name:     "PVC exists but no share-manager pod",
			pod:      makeVM("vm", vmNamespace, true, pvcName),
			objects:  []runtime.Object{makePVC(pvcName, vmNamespace)},
			wantNode: "",
		},
		{
			name: "share-manager pod running on target node",
			pod:  makeVM("vm", vmNamespace, true, pvcName),
			objects: []runtime.Object{
				makePVC(pvcName, vmNamespace),
				makeShareManagerPod(pvcName, targetNode),
			},
			wantNode: targetNode,
		},
		{
			name: "share-manager pod exists but not running",
			pod:  makeVM("vm", vmNamespace, true, pvcName),
			objects: []runtime.Object{
				makePVC(pvcName, vmNamespace),
				func() *corev1.Pod {
					p := makeShareManagerPod(pvcName, targetNode)
					p.Status.Phase = corev1.PodPending
					return p
				}(),
			},
			wantNode: "",
		},
		{
			name: "PVC is not RWX — share-manager should be ignored",
			pod:  makeVM("vm", vmNamespace, true, pvcName),
			objects: []runtime.Object{
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: vmNamespace},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					},
				},
				makeShareManagerPod(pvcName, targetNode),
			},
			wantNode: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.objects...)
			got, err := findShareManagerNode(context.Background(), clientset, tt.pod)
			if (err != nil) != tt.wantErr {
				t.Errorf("findShareManagerNode() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantNode {
				t.Errorf("findShareManagerNode() = %q, want %q", got, tt.wantNode)
			}
		})
	}
}

// --- Filter tests ---

func TestFilter(t *testing.T) {
	const (
		vmNamespace = "default"
		pvcName     = "my-rwx-pvc"
		targetNode  = "node-2"
		otherNode   = "node-1"
	)

	tests := []struct {
		name        string
		pod         *corev1.Pod
		objects     []runtime.Object
		nodeName    string
		wantSuccess bool
	}{
		{
			name:        "pod not opted in — all nodes pass",
			pod:         makeVM("vm", vmNamespace, false, pvcName),
			objects:     []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:    otherNode,
			wantSuccess: true,
		},
		{
			name:        "opted in, no share-manager — all nodes pass",
			pod:         makeVM("vm", vmNamespace, true, pvcName),
			objects:     []runtime.Object{makePVC(pvcName, vmNamespace)},
			nodeName:    otherNode,
			wantSuccess: true,
		},
		{
			name:        "opted in, share-manager on target — target node passes",
			pod:         makeVM("vm", vmNamespace, true, pvcName),
			objects:     []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:    targetNode,
			wantSuccess: true,
		},
		{
			name:        "opted in, share-manager on target — other node rejected",
			pod:         makeVM("vm", vmNamespace, true, pvcName),
			objects:     []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:    otherNode,
			wantSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.objects...)
			plugin := &Plugin{clientset: clientset}
			nodeInfo := makeNodeInfo(tt.nodeName)
			status := plugin.Filter(context.Background(), nil, tt.pod, nodeInfo)
			if tt.wantSuccess && status != nil && !status.IsSuccess() {
				t.Errorf("Filter() returned non-success status: %v", status.Message())
			}
			if !tt.wantSuccess && (status == nil || status.IsSuccess()) {
				t.Errorf("Filter() expected non-success status but got success")
			}
		})
	}
}

// --- Score tests ---

func TestScore(t *testing.T) {
	const (
		vmNamespace = "default"
		pvcName     = "my-rwx-pvc"
		targetNode  = "node-2"
		otherNode   = "node-1"
	)

	tests := []struct {
		name      string
		pod       *corev1.Pod
		objects   []runtime.Object
		nodeName  string
		wantScore int64
	}{
		{
			name:      "pod not opted in — score 0",
			pod:       makeVM("vm", vmNamespace, false, pvcName),
			objects:   []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:  targetNode,
			wantScore: 0,
		},
		{
			name:      "opted in, no share-manager — score 0 for all nodes",
			pod:       makeVM("vm", vmNamespace, true, pvcName),
			objects:   []runtime.Object{makePVC(pvcName, vmNamespace)},
			nodeName:  targetNode,
			wantScore: 0,
		},
		{
			name:      "opted in, share-manager on target — target node gets max score",
			pod:       makeVM("vm", vmNamespace, true, pvcName),
			objects:   []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:  targetNode,
			wantScore: framework.MaxNodeScore,
		},
		{
			name:      "opted in, share-manager on target — other node gets 0",
			pod:       makeVM("vm", vmNamespace, true, pvcName),
			objects:   []runtime.Object{makePVC(pvcName, vmNamespace), makeShareManagerPod(pvcName, targetNode)},
			nodeName:  otherNode,
			wantScore: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tt.objects...)
			plugin := &Plugin{clientset: clientset}
			score, status := plugin.Score(context.Background(), nil, tt.pod, tt.nodeName)
			if status != nil && !status.IsSuccess() {
				t.Errorf("Score() returned error status: %v", status.Message())
			}
			if score != tt.wantScore {
				t.Errorf("Score() = %d, want %d", score, tt.wantScore)
			}
		})
	}
}
