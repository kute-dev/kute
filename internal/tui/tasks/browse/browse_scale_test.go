package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func hpaTargeting(ns, name, targetKind, targetName string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: targetKind, Name: targetName},
		},
	}
}

func statefulSetObj(ns, name string, replicas, ready int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: ready},
	}
}

func newDeploymentModel(t *testing.T, mut *fakeMutator, replicas int32) Model {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObjReplicas("default", "api", replicas)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	return step(t, m, m.Init()())
}

func deploymentObjReplicas(ns, name string, replicas int32) *appsv1.Deployment {
	d := deploymentObj(ns, name)
	d.Spec.Replicas = &replicas
	d.Status.ReadyReplicas = replicas
	d.Status.UpdatedReplicas = replicas
	d.Status.AvailableReplicas = replicas
	return d
}

func TestPlusOpensScalePromptPrefilledCurrentPlusOne(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 3)

	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	if m.pendingScale == nil {
		t.Fatal("expected pendingScale set after '+'")
	}
	if m.pendingScale.value != "4" {
		t.Fatalf("pendingScale.value = %q, want %q", m.pendingScale.value, "4")
	}
	if !m.CapturingInput() {
		t.Fatal("expected CapturingInput true while the scale prompt is open")
	}
}

func TestMinusPrefillsCurrentMinusOneClampedAtZero(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 0)

	m = step(t, m, tea.KeyPressMsg{Text: "-"})
	if m.pendingScale == nil || m.pendingScale.value != "0" {
		t.Fatalf("expected clamped prefill of 0, got %+v", m.pendingScale)
	}
}

func TestDigitReplacesPrefillThenAppends(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 3)
	m = step(t, m, tea.KeyPressMsg{Text: "+"}) // prefill "4"

	m = step(t, m, tea.KeyPressMsg{Text: "5"})
	if m.pendingScale.value != "5" {
		t.Fatalf("first digit should replace the prefill: value = %q, want %q", m.pendingScale.value, "5")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "0"})
	if m.pendingScale.value != "50" {
		t.Fatalf("second digit should append: value = %q, want %q", m.pendingScale.value, "50")
	}
}

func TestNudgeAfterPrefillIncrementsAndDecrements(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 3)
	m = step(t, m, tea.KeyPressMsg{Text: "+"}) // prefill "4"

	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	if m.pendingScale.value != "5" {
		t.Fatalf("nudge up: value = %q, want %q", m.pendingScale.value, "5")
	}
	m = step(t, m, tea.KeyPressMsg{Text: "-"})
	m = step(t, m, tea.KeyPressMsg{Text: "-"})
	if m.pendingScale.value != "3" {
		t.Fatalf("nudge down: value = %q, want %q", m.pendingScale.value, "3")
	}
}

func TestBackspaceEditsScaleBuffer(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 3)
	m = step(t, m, tea.KeyPressMsg{Text: "+"}) // prefill "4"
	m = step(t, m, tea.KeyPressMsg{Text: "backspace"})
	if m.pendingScale.value != "" {
		t.Fatalf("expected buffer cleared after backspace, got %q", m.pendingScale.value)
	}
}

func TestEscCancelsScalePrompt(t *testing.T) {
	mut := &fakeMutator{}
	m := newDeploymentModel(t, mut, 3)
	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.pendingScale != nil {
		t.Fatal("expected pendingScale cleared after esc")
	}
	if len(mut.scaled) != 0 {
		t.Fatalf("expected no scale call after cancel, got %v", mut.scaled)
	}
}

func TestEnterCommitsScaleThroughMutator(t *testing.T) {
	mut := &fakeMutator{}
	m := newDeploymentModel(t, mut, 3)

	m = step(t, m, tea.KeyPressMsg{Text: "+"}) // prefill "4"
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})

	if m.pendingScale != nil {
		t.Fatal("expected pendingScale cleared after enter")
	}
	if len(mut.scaled) != 1 || mut.scaled[0] != 4 {
		t.Fatalf("scaled = %v, want [4]", mut.scaled)
	}
	if m.actions.Active() {
		t.Fatal("scale is TierNone and should execute immediately, not show a confirm")
	}
}

func TestScaleWillRunLineNamesTheKubectlCommand(t *testing.T) {
	m := newDeploymentModel(t, &fakeMutator{}, 3)
	m = step(t, m, tea.KeyPressMsg{Text: "+"})

	kb := m.Keybar()
	if kb.PillText != "SCALE" {
		t.Fatalf("PillText = %q, want %q", kb.PillText, "SCALE")
	}
	want := "kubectl scale deploy/api --replicas=4 -n default"
	if !strings.Contains(kb.RightNote, want) {
		t.Fatalf("RightNote = %q, want it to contain %q", kb.RightNote, want)
	}
}

// TestScalePromptShowsHPAWarningWhenManaged pins 17b (docs/design
// README.md:252: "HPA-managed workloads show managed by hpa/<name> —
// scaling overridden on next sync as a yellow note instead of blocking"):
// an HPA whose scaleTargetRef points at the selected Deployment surfaces
// that warning in the keybar; a Deployment with no matching HPA doesn't.
func TestScalePromptShowsHPAWarningWhenManaged(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment:              {deploymentObjReplicas("default", "api", 3)},
		kube.KindHorizontalPodAutoscaler: {hpaTargeting("default", "api-hpa", "Deployment", "api")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "+"})

	kb := m.Keybar()
	want := "managed by hpa/api-hpa — scaling overridden on next sync"
	if kb.RightWarnNote != want {
		t.Fatalf("RightWarnNote = %q, want %q", kb.RightWarnNote, want)
	}
}

// TestScalePromptNoHPAWarningWhenUnmanaged is the converse: a Deployment
// with no matching HPA (even when other HPAs exist for other workloads)
// must show no warning at all — never a false positive.
func TestScalePromptNoHPAWarningWhenUnmanaged(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment:              {deploymentObjReplicas("default", "api", 3)},
		kube.KindHorizontalPodAutoscaler: {hpaTargeting("default", "other-hpa", "Deployment", "other")},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister, Mutator: &fakeMutator{}})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())
	m = step(t, m, tea.KeyPressMsg{Text: "+"})

	if kb := m.Keybar(); kb.RightWarnNote != "" {
		t.Fatalf("RightWarnNote = %q, want empty (no HPA targets this Deployment)", kb.RightWarnNote)
	}
}

func TestScaleAppliesToStatefulSets(t *testing.T) {
	mut := &fakeMutator{}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindStatefulSet: {statefulSetObj("default", "db", 2, 2)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindStatefulSet
	m := New(Config{Session: session, Lister: lister, Mutator: mut})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "-"}) // prefill "1"
	m = step(t, m, tea.KeyPressMsg{Text: "enter"})
	if len(mut.scaled) != 1 || mut.scaled[0] != 1 {
		t.Fatalf("scaled = %v, want [1]", mut.scaled)
	}
}

func TestPlusNoOpsWithoutMutator(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {deploymentObjReplicas("default", "api", 3)},
	}}
	session := newSession()
	session.Location.Kind = kube.KindDeployment
	m := New(Config{Session: session, Lister: lister})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	m = step(t, m, tea.KeyPressMsg{Text: "+"})
	if m.pendingScale != nil {
		t.Fatal("expected '+' to no-op without a mutator wired")
	}
}
