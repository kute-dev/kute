package browse

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// resourcesDeployment is "aim-worker": a single "worker" container with
// cpu/mem request+limit already set, so 25a's panel has real CURRENT values
// to prefill and nudge from.
func resourcesDeployment(ns, name string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 1, CreationTimestamp: setImageAge(30 * 24 * time.Hour)},
		Spec: appsv1.DeploymentSpec{Replicas: replicasPtr(2), Template: corev1.PodTemplateSpec{
			Spec: corev1.PodSpec{Containers: []corev1.Container{
				{Name: "worker", Image: "aim-worker:1.0", Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
					Limits:   corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("1"), corev1.ResourceMemory: resource.MustParse("512Mi")},
				}},
			}},
		}},
		Status: appsv1.DeploymentStatus{Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2, AvailableReplicas: 2, ObservedGeneration: 1},
	}
}

// resourcesOwnerChain builds the ReplicaSet + one live Pod (owned by that
// ReplicaSet, in turn owned by deployment) workloadPods needs to walk —
// the Pod's worker container carries an OOMKilled LastTerminationState so
// the strip's failure callout has something real to render.
func resourcesOwnerChain(ns, deployment string) (*appsv1.ReplicaSet, *corev1.Pod) {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: deployment + "-abc123", Namespace: ns,
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: deployment}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: deployment + "-abc123-xyz", Namespace: ns,
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
	return rs, pod
}

func resourcesLister(dep *appsv1.Deployment, rs *appsv1.ReplicaSet, pod *corev1.Pod) fakeLister {
	return fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {dep},
		kube.KindReplicaSet: {rs},
		kube.KindPod:        {pod},
	}}
}

func TestBeginSetResourcesPrefillsFieldsFromContainerSpec(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if !m.beginSetResources() {
		t.Fatal("beginSetResources returned false")
	}
	f := m.pendingSetResources.fields
	if f[fieldCPURequest].current != "250m" || f[fieldCPURequest].buffer != "250m" {
		t.Errorf("cpu request = %+v, want current/buffer 250m", f[fieldCPURequest])
	}
	if f[fieldCPULimit].current != "1" {
		t.Errorf("cpu limit current = %q, want 1", f[fieldCPULimit].current)
	}
	if f[fieldMEMRequest].current != "512Mi" || f[fieldMEMLimit].current != "512Mi" {
		t.Errorf("mem request/limit = %+v / %+v, want 512Mi/512Mi", f[fieldMEMRequest], f[fieldMEMLimit])
	}
	if !m.pendingSetResources.oomOK {
		t.Error("expected the OOMKilled fact to be found for the worker container")
	}
}

func TestNudgeAdjustsBySaneStepsAndClampsAtZero(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	// cpu request field is selected by default (fieldIdx 0): +50m from 250m.
	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	if got := m.pendingSetResources.fields[fieldCPURequest].buffer; got != "300m" {
		t.Fatalf("cpu request after +: %q, want 300m", got)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "-"})
	m = step(t, m, tea.KeyPressMsg{Text: "-"})
	if got := m.pendingSetResources.fields[fieldCPURequest].buffer; got != "200m" {
		t.Fatalf("cpu request after +/--: %q, want 200m", got)
	}

	// Move to mem request (fieldIdx 2) and nudge by 64Mi.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	if got := m.pendingSetResources.fields[fieldMEMRequest].buffer; got != "576Mi" {
		t.Fatalf("mem request after +: %q, want 576Mi", got)
	}

	// Clamp at zero: nudge cpu request down from 200m by 50m four times.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	for range 6 {
		m = step(t, m, tea.KeyPressMsg{Text: "-"})
	}
	if got := m.pendingSetResources.fields[fieldCPURequest].buffer; got != "0" {
		t.Fatalf("cpu request after repeated -: %q, want clamped to 0", got)
	}
}

func TestUnsetFieldThenTypingClearsUnset(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	m = step(t, m, tea.KeyPressMsg{Text: "u"})
	if f := m.pendingSetResources.fields[fieldCPURequest]; !f.unset || f.buffer != "" {
		t.Fatalf("after u: %+v, want unset with empty buffer", f)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "1"})
	if f := m.pendingSetResources.fields[fieldCPURequest]; f.unset || f.buffer != "1" {
		t.Fatalf("after typing post-unset: %+v, want unset cleared, buffer '1'", f)
	}
}

func TestLeftRightArrowsMoveCursorForMidStringEdits(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	// cpu request prefilled to "250m", cursor parked at the end (len 4).
	f := m.pendingSetResources.fields[fieldCPURequest]
	if f.buffer != "250m" || f.cursor != 4 {
		t.Fatalf("prefill = %+v, want buffer 250m cursor 4", f)
	}

	// Left three times lands the cursor right after "2", before "50m".
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := m.pendingSetResources.fields[fieldCPURequest].cursor; got != 1 {
		t.Fatalf("cursor after 3x left = %d, want 1", got)
	}

	// Typing "9" inserts at the cursor rather than appending at the end.
	m = step(t, m, tea.KeyPressMsg{Text: "9"})
	if got := m.pendingSetResources.fields[fieldCPURequest].buffer; got != "2950m" {
		t.Fatalf("buffer after mid-string insert = %q, want 2950m", got)
	}
	if got := m.pendingSetResources.fields[fieldCPURequest].cursor; got != 2 {
		t.Fatalf("cursor after insert = %d, want 2", got)
	}

	// Right moves it back toward the end; backspace at that position deletes
	// the rune just before the cursor, not the last rune in the buffer.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyRight})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := m.pendingSetResources.fields[fieldCPURequest].buffer; got != "295m" {
		t.Fatalf("buffer after right,right,backspace = %q, want 295m", got)
	}
}

func TestInvalidQuantityBlocksApply(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	for _, r := range "not-a-quantity" {
		m = step(t, m, tea.KeyPressMsg{Text: string(r)})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pendingSetResources == nil {
		t.Fatal("expected the panel to stay open after an invalid quantity blocks apply")
	}
	if !m.pendingSetResources.fields[fieldCPURequest].invalid {
		t.Error("expected the cpu request field to be marked invalid")
	}
	if len(mut.setResources) != 0 {
		t.Fatalf("expected SetResources not called, got %v", mut.setResources)
	}
}

func TestRequestGreaterThanLimitBlocksApply(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	// cpu request field (idx 0), current 250m, limit is 1 (1000m) — push
	// the request past the limit via repeated nudges (14 * 50m = 700m + 250m
	// starting point = 950m, one more push crosses 1000m... use enough steps
	// to clear 1 comfortably).
	for range 20 {
		m = step(t, m, tea.KeyPressMsg{Text: "+"})
	}
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.pendingSetResources == nil {
		t.Fatal("expected the panel to stay open after request>limit blocks apply")
	}
	if !m.pendingSetResources.fields[fieldCPURequest].invalid || !m.pendingSetResources.fields[fieldCPULimit].invalid {
		t.Error("expected both cpu request and cpu limit fields marked invalid")
	}
	if len(mut.setResources) != 0 {
		t.Fatalf("expected SetResources not called, got %v", mut.setResources)
	}
}

func TestOnlyChangedFieldsGoIntoEdits(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	// Move to mem limit (idx 3) and change it — the only touched field.
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = step(t, m, tea.KeyPressMsg{Text: "+"})

	edits := m.pendingSetResources.edits()
	if edits.CPURequest != nil || edits.CPULimit != nil || edits.MEMRequest != nil {
		t.Fatalf("edits = %+v, want only MEMLimit set", edits)
	}
	if edits.MEMLimit == nil || *edits.MEMLimit != "576Mi" {
		t.Fatalf("MEMLimit = %v, want 576Mi", edits.MEMLimit)
	}
}

func TestContainerTabSwitchRecomputesFields(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, corev1.Container{
		Name: "metrics-sidecar", Image: "sidecar:0.9.1",
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("128Mi")},
		},
	})
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	if got := m.pendingSetResources.fields[fieldMEMLimit].current; got != "512Mi" {
		t.Fatalf("worker mem limit = %q, want 512Mi", got)
	}
	m = step(t, m, tea.KeyPressMsg{Text: "tab"})
	if got := m.pendingSetResources.fields[fieldMEMLimit].current; got != "128Mi" {
		t.Fatalf("sidecar mem limit after tab = %q, want 128Mi", got)
	}
	if m.pendingSetResources.fields[fieldCPURequest].current != "" {
		t.Fatalf("sidecar cpu request = %q, want empty (unset on the container spec)", m.pendingSetResources.fields[fieldCPURequest].current)
	}
}

func TestSetResourcesCommitsThroughMutatorNonProd(t *testing.T) {
	dep := resourcesDeployment("default", "aim-worker")
	rs, pod := resourcesOwnerChain("default", "aim-worker")
	lister := resourcesLister(dep, rs, pod)
	mut := &fakeMutator{}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m.beginSetResources()

	m = step(t, m, tea.KeyPressMsg{Text: "+"}) // cpu request 250m -> 300m
	m = step(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})

	// One dry-run call (commitSetResources' pre-flight) plus one real call
	// (actions.Controller.execute()'s "set-resources" case, dryRun=false).
	if len(mut.setResources) != 2 {
		t.Fatalf("expected exactly two SetResources calls (dry-run + real), got %v", mut.setResources)
	}
	if mut.dryRun {
		t.Fatal("expected the last recorded call to be the real (non-dry-run) apply")
	}
	if m.pendingSetResources != nil {
		t.Fatal("expected the panel to close after a successful dry-run")
	}
	if m.actions.Active() {
		t.Fatal("set-resources is TierNone outside PROD and should execute immediately, not show a confirm")
	}
}
