// golden_states_test.go pins the browse skeleton's other rendered states
// beyond 2a's resting Pods list (golden_test.go): 4a/4b/6b/9a/10c/11a/13c/
// 14a/14b/15a/18a/20a all share this one package (docs/design README.md:
// "9a — not a new screen", "13c — a registry kind, not a bespoke screen"),
// so each gets its own goldenXModel builder plus fixtures named
// "<state>-<W>x<H>.golden", following setup/golden_test.go's
// multiple-states-in-one-file convention.
package browse

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metaerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/config"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
)

func goldenStateDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "browse")
}

// --- 4a: connection lost mid-session ---

func goldenOfflineModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			goldenPod("api-7d9f6c8-abcde", corev1.PodRunning, true, 0, "", "node-a"),
			goldenPod("worker-0", corev1.PodRunning, false, 6, "CrashLoopBackOff", "node-a"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	// m.fetchedAt/m.now are both real wall-clock reads (load()/the
	// ConnStateMsg handler each call time.Now()) — pinned to a fixed instant
	// here so the stale strip's absolute "showing snapshot from HH:MM:SS"
	// text and "· Ns old"/"next in Ns" countdowns don't drift between the
	// UPDATE_GOLDEN run and every later comparison run.
	fixedNow := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	m.fetchedAt = fixedNow.Add(-94 * time.Second)
	m = step(t, m, kube.ConnStateMsg{
		Phase:       kube.ConnReconnecting,
		Err:         "dial tcp 10.0.0.5:16443: i/o timeout",
		Attempt:     3,
		NextRetryAt: fixedNow.Add(4 * time.Second),
	})
	m.now = fixedNow
	return m
}

// --- 4b: RBAC / API error on one kind (403) ---

func goldenPermissionDeniedModel(t *testing.T, width, height int) Model {
	t.Helper()
	msg := `User "dev-readonly" cannot list resource "secrets" in namespace "nva-stage"`
	lister := forbiddenLister{
		kind: kube.KindSecret,
		err:  metaerrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "", errors.New(msg)),
	}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Location.Kind = kube.KindSecret
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 6b: all-namespaces mode ---

func goldenAllNSModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("nva-prod", "api-1"),
			crashPod("nva-stage", "worker-0"),
			pod("nva-stage", "cache-0"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tui.SwitchNamespaceMsg{Namespace: ""})
	return m
}

// --- 9a: deployments list (exemplar for every non-pod kind) ---

func goldenDeploymentsModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {
			deploymentObj("nva-stage", "nva-worker"),
			deploymentObj("nva-stage", "nva-gateway"),
		},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Location.Kind = kube.KindDeployment
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 10c: empty namespace (connected, zero pods) ---

func goldenEmptyModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("nva-stage", "api-1"),
			pod("nva-stage", "api-2"),
		},
		kube.KindNamespace: {
			namespace("default"),
			namespace("nva-stage"),
		},
		kube.KindConfigMap: {
			configMap("default", "app-config"),
			configMap("default", "other-config"),
		},
		kube.KindSecret: {secret("default", "app-secret")},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 11a: nodes list (cluster-scoped) ---

func goldenNodeObj(name string, ready bool, cordoned bool, cpu, mem string) *corev1.Node {
	status := corev1.ConditionTrue
	if !ready {
		status = corev1.ConditionFalse
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: "v1.30.1"},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse(cpu),
				corev1.ResourceMemory: resource.MustParse(mem),
				corev1.ResourcePods:   resource.MustParse("110"),
			},
		},
	}
}

func goldenNodesModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindNode: {
			goldenNodeObj("node-a", true, false, "4", "16Gi"),
			goldenNodeObj("node-b", true, false, "4", "16Gi"),
			goldenNodeObj("node-c", false, false, "4", "16Gi"),
			goldenNodeObj("node-d", true, true, "4", "16Gi"),
		},
		kube.KindPod: {
			pod("default", "api-1"),
			pod("default", "api-2"),
		},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindNode
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 13c: forwards manager ---
//
// Forwards flow through the same resources.List/Project pipeline as real
// API objects (kube.ForwardObject implements runtime.Object) — so the
// golden model injects *kube.ForwardObject rows straight into fakeLister
// rather than driving the real *kube.ForwardManager, which would need a
// live dialer goroutine and non-deterministic timing to settle into a
// steady state.
func goldenForwardsModel(t *testing.T, width, height int) Model {
	t.Helper()
	active := kube.ForwardSession{
		ID:          "1",
		Target:      kube.ForwardTarget{Kind: kube.KindPod, Namespace: "nva-stage", Name: "nva-worker-9k2ss"},
		ResolvedPod: "nva-worker-9k2ss",
		LocalPort:   8080, RemotePort: 80,
		State:          kube.ForwardActive,
		StartedAt:      time.Now().Add(-41 * time.Minute),
		LastActivityAt: time.Now().Add(-12 * time.Minute),
	}
	reconnecting := kube.ForwardSession{
		ID:          "2",
		Target:      kube.ForwardTarget{Kind: kube.KindService, Namespace: "nva-prod", Name: "postgres"},
		ResolvedPod: "postgres-0",
		LocalPort:   5432, RemotePort: 5432,
		State:       kube.ForwardReconnecting,
		Err:         "pod restarted",
		Attempt:     2,
		NextRetryAt: time.Now().Add(4 * time.Second),
		StartedAt:   time.Now().Add(-2 * time.Hour),
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindForward: {
			&kube.ForwardObject{Session: active},
			&kube.ForwardObject{Session: reconnecting},
		},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindForward
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 14a: custom resource list (exemplar: Certificates) ---

func goldenCRDInstancesModel(t *testing.T, width, height int) Model {
	t.Helper()
	dk := kube.DiscoveredKind{
		Kind: "Certificate", Plural: "certificates", Group: "cert-manager.io",
		GVR:           schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: "certificates"},
		Versions:      []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped: false, Established: true,
	}
	reg, _ := resources.BuildDiscoveredRegistry([]kube.DiscoveredKind{dk}, nil)
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.ResourceKind("Certificate"): {
			certificateInstance("api-tls", "nva-stage"),
			certificateInstance("gateway-tls", "nva-stage"),
		},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Registry = reg
	sess.Location.Kind = kube.ResourceKind("Certificate")
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 14b: CustomResourceDefinitions list ---

func goldenCRDDefRow(plural, group string, established bool) *unstructured.Unstructured {
	condStatus := "True"
	if !established {
		condStatus = "False"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata":   map[string]any{"name": plural + "." + group},
		"spec": map[string]any{
			"group":    group,
			"names":    map[string]any{"kind": plural, "plural": plural},
			"scope":    "Namespaced",
			"versions": []any{map[string]any{"name": "v1", "served": true, "storage": true}},
		},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Established", "status": condStatus}},
		},
	}}
}

func goldenCRDListModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindCustomResourceDefinition: {
			goldenCRDDefRow("certificates", "cert-manager.io", true),
			goldenCRDDefRow("certificaterequests", "cert-manager.io", true),
			goldenCRDDefRow("httproutes", "gateway.networking.k8s.io", false),
		},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindCustomResourceDefinition
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 15a: loading a kind ---

func goldenLoadingModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(width, height)
	return m
}

// --- 18a: Helm releases list ---

func goldenHelmModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHelmRelease: {
			helmRelease("nva-stage", "postgresql", "postgresql", "12.1.9", "15.4.0", "deployed", 3),
			helmRelease("nva-stage", "redis", "redis", "18.1.5", "7.2.4", "deployed", 2),
			helmRelease("nva-stage", "nva-gateway", "kube-prometheus-stack", "58.2.1", "0.73.0", "pending-upgrade", 2),
			kube.NewHelmReleaseObject(kube.HelmRelease{
				Namespace: "nva-stage", Name: "broken-app", Chart: "mychart", ChartVersion: "1.0.0",
				AppVersion: "2.1.0", Revision: 2, Status: "failed", StatusReason: "hook timeout",
			}),
		},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Location.Kind = kube.KindHelmRelease
	m := New(Config{Session: sess, Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	return m
}

// --- 8b: destructive-action confirm (ctrl-d delete) ---
//
// Both friction tiers render inline in this same package's Body()/keybar —
// TierInline never overrides Body (the table stays visible under the y/N
// keybar prompt), TierModal's components.TypeNameModal does cover it (the
// one red-bordered surface). Both are pinned here rather than in a separate
// component-level golden file since the confirming state only ever exists
// wired into a real task.

func goldenConfirmInlineModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {pod("nva-stage", "api-0")},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	return m
}

func goldenConfirmModalModel(t *testing.T, width, height int) Model {
	t.Helper()
	grace := int64(30)
	podWithGrace := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "nva-worker-9k2ss", Namespace: "nva-stage"},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{{Name: "worker"}},
			TerminationGracePeriodSeconds: &grace,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Ready: true}}},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {podWithGrace},
	}}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	sess.Config = config.Config{ProdContexts: []string{sess.Location.Context}}
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tea.KeyPressMsg{Text: "ctrl+d"})
	// Type a partial name so the modal's "N/M" progress indicator and
	// partial-match text both render, rather than an empty input row.
	for _, r := range "nva-worker" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	return m
}

// --- 20a: bulk operations (marked set) ---

func goldenMarksModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindPod: {
			pod("default", "api-0"),
			pod("default", "api-1"),
			pod("default", "worker-0"),
		},
	}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tea.KeyPressMsg{Text: "space"})
	m = step(t, m, tea.KeyPressMsg{Text: "space"})
	return m
}

// --- 24a: set-image inline editor ---

func goldenSetImageModel(t *testing.T, width, height int) Model {
	t.Helper()
	dep := twoContainerDeployment("default", "aim-worker", "registry.aim.dev/aim-worker:3.4.2")
	rsOldest := replicaSetRevision("default", "aim-worker-r41", "aim-worker", "registry.aim.dev/aim-worker:3.4.0", 41, 23*24*time.Hour)
	rsOld := replicaSetRevision("default", "aim-worker-r42", "aim-worker", "registry.aim.dev/aim-worker:3.4.1", 42, 21*24*time.Hour)
	rsCur := replicaSetRevision("default", "aim-worker-r43", "aim-worker", "registry.aim.dev/aim-worker:3.4.2", 43, 2*24*time.Hour)
	sighting := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "aim-worker", Namespace: "aim-prod", CreationTimestamp: setImageAge(40 * time.Minute)},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "worker", Image: "registry.aim.dev/aim-worker:3.4.3"}}},
		}},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep, sighting},
		kube.KindReplicaSet: {rsOldest, rsOld, rsCur},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindDeployment
	m := New(Config{Session: sess, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tea.KeyPressMsg{Text: "i"})
	return m
}

// --- 25a: resources inline editor ---

// goldenSetResourcesModel builds the exact scenario docs/design/
// v.0.2.0.dc.html's 25a mockup illustrates: a single-container "worker"
// Deployment whose live pod OOMKilled ~4m ago, cpu/mem request+limit
// matching the mockup's own CURRENT values (250m/1, 512Mi/512Mi), and live
// per-container usage that pins mem at its limit (Bad) while cpu stays
// comfortably under (neutral/Warn depending on the row) — exercising every
// bar-color state in one screenshot. The cursor is moved to the mem limit
// row and nudged up by 64Mi four times (512Mi -> 768Mi) to also exercise the
// edited-NEW-cell/will-run-line rendering, matching the mockup's own
// "768Mi" edited example.
func goldenSetResourcesModel(t *testing.T, width, height int) Model {
	t.Helper()
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "aim-worker", Namespace: "default", Generation: 1, CreationTimestamp: setImageAge(30 * 24 * time.Hour)},
		Spec: appsv1.DeploymentSpec{Replicas: replicasPtr(4), Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "worker", Image: "registry.aim.dev/aim-worker:3.4.2", Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("512Mi")},
				}},
			}},
		}},
		Status: appsv1.DeploymentStatus{Replicas: 4, ReadyReplicas: 4, UpdatedReplicas: 4, AvailableReplicas: 4, ObservedGeneration: 1},
	}
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: "aim-worker-r43", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: "aim-worker"}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "aim-worker-r43-x8z2p", Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: rs.Name}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  "worker",
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
					Reason: "OOMKilled", FinishedAt: setImageAge(4 * time.Minute),
				}},
			}},
		},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep},
		kube.KindReplicaSet: {rs},
		kube.KindPod:        {pod},
	}}
	metrics := fakeMetrics{containerMetrics: map[string]map[string]kube.PodMetrics{
		pod.Name: {"worker": {CPU: "182m", MEM: "512Mi", CPUMilli: 182, MemBytes: 512 * 1024 * 1024}},
	}}
	sess := newSession()
	sess.Location.Kind = kube.KindDeployment
	m := New(Config{Session: sess, Lister: lister, Metrics: metrics, Mutator: &fakeMutator{}})
	m.SetSize(width, height)
	m = step(t, m, m.load()())
	m = step(t, m, tea.KeyPressMsg{Text: "R"})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	for range 4 {
		m = step(t, m, tea.KeyPressMsg{Text: "+"})
	}
	return m
}

// goldenStates maps each fixture-name prefix to its model builder — shared
// by the plain and (for the color-heaviest states) truecolor fixture maps
// below.
var goldenStatePrefixes = []string{
	"offline", "denied", "allns", "deployments", "empty", "nodes",
	"forwards", "crd-instances", "crd-list", "loading", "helm", "marks",
	"confirm-inline", "confirm-modal", "set-image", "set-resources",
}

func goldenStateModel(t *testing.T, prefix string, width, height int) Model {
	t.Helper()
	switch prefix {
	case "offline":
		return goldenOfflineModel(t, width, height)
	case "denied":
		return goldenPermissionDeniedModel(t, width, height)
	case "allns":
		return goldenAllNSModel(t, width, height)
	case "deployments":
		return goldenDeploymentsModel(t, width, height)
	case "empty":
		return goldenEmptyModel(t, width, height)
	case "nodes":
		return goldenNodesModel(t, width, height)
	case "forwards":
		return goldenForwardsModel(t, width, height)
	case "crd-instances":
		return goldenCRDInstancesModel(t, width, height)
	case "crd-list":
		return goldenCRDListModel(t, width, height)
	case "loading":
		return goldenLoadingModel(t, width, height)
	case "helm":
		return goldenHelmModel(t, width, height)
	case "marks":
		return goldenMarksModel(t, width, height)
	case "confirm-inline":
		return goldenConfirmInlineModel(t, width, height)
	case "confirm-modal":
		return goldenConfirmModalModel(t, width, height)
	case "set-image":
		return goldenSetImageModel(t, width, height)
	case "set-resources":
		return goldenSetResourcesModel(t, width, height)
	default:
		t.Fatalf("unknown golden state prefix %q", prefix)
		return Model{}
	}
}

func goldenStateFixtures(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, prefix := range goldenStatePrefixes {
		out[prefix+"-120x36.golden"] = goldenStateModel(t, prefix, 120, 36).Render()
		out[prefix+"-80x24.golden"] = goldenStateModel(t, prefix, 80, 24).Render()
	}
	return out
}

func TestGenerateGoldenStateFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate browse golden fixtures")
	}
	for name, got := range goldenStateFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenStateDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenStateFixtures(t *testing.T) {
	for name, got := range goldenStateFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenStateDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}

// truecolorStatePrefixes are the states worth pinning at the per-cell color
// level — the ones with the richest status-color semantics (red/yellow/
// blue mixes, selection highlighting, mode pills). "empty"/"loading"/
// "crd-list"/"crd-instances" are left plain-only: mostly text, and their
// color tokens (health glyphs, selection bar) are already pinned by other
// truecolor fixtures in this package.
var truecolorStatePrefixes = []string{
	"offline", "denied", "allns", "deployments", "nodes", "forwards", "helm", "marks",
	"confirm-inline", "confirm-modal", "set-image", "set-resources",
}

func truecolorStateFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	out := map[string]string{}
	for _, prefix := range truecolorStatePrefixes {
		dark := goldenStateModel(t, prefix, 120, 36)
		light := goldenStateModel(t, prefix, 120, 36)
		light.session.Theme = tui.Light()
		out[prefix+"-120x36-dark.golden"] = dark.Render()
		out[prefix+"-120x36-light.golden"] = light.Render()
	}
	return out
}

func TestGenerateTruecolorStateFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate browse golden fixtures")
	}
	for name, got := range truecolorStateFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenStateDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorStateFixtures(t *testing.T) {
	for name, got := range truecolorStateFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenStateDir(), name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden %s: %v", path, err)
			}
			if got != string(want) {
				t.Fatalf("golden mismatch for %s\nwant:\n%s\ngot:\n%s", name, string(want), got)
			}
		})
	}
}
