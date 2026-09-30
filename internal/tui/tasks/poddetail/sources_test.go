package poddetail

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

func secretKeyEnv(name, secret, key string) corev1.EnvVar {
	return corev1.EnvVar{Name: name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: secret}, Key: key,
	}}}
}

// configuredPod references config every way a spec can: a Secret key and a
// ConfigMap key via env, a whole Secret via envFrom (with a prefix), a
// Secret volume, the injected kube-api-access projected volume, and an
// image pull Secret. A second container ("sidecar") has its own env so the
// ENV & MOUNTS section can be shown to follow the selection.
func configuredPod(name, ns string) *corev1.Pod {
	pod := runningPod(name, ns, "node-a")
	pod.Spec.Containers[0].Env = []corev1.EnvVar{
		secretKeyEnv("ConnectionStrings__TimeSheetDb", "timesheet-backend-db", "connectionString"),
		{Name: "LOG_LEVEL", Value: "debug"},
		{Name: "FLAGS", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"}, Key: "flags",
		}}},
	}
	pod.Spec.Containers[0].EnvFrom = []corev1.EnvFromSource{{
		Prefix:    "SMTP_",
		SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "smtp"}},
	}}
	pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{
		{Name: "tls", MountPath: "/etc/tls", ReadOnly: true},
		{Name: "kube-api-access-x1", MountPath: "/var/run/secrets/kubernetes.io/serviceaccount", ReadOnly: true},
	}
	pod.Spec.Containers = append(pod.Spec.Containers, corev1.Container{
		Name: "sidecar", Image: "sidecar:1",
		Env: []corev1.EnvVar{secretKeyEnv("Email__ResendApiKey", "resend", "apiKey")},
	})
	pod.Status.ContainerStatuses = append(pod.Status.ContainerStatuses, corev1.ContainerStatus{
		Name: "sidecar", Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
	})
	pod.Spec.Volumes = []corev1.Volume{
		{Name: "tls", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "api-tls"}}},
		{Name: "kube-api-access-x1", VolumeSource: corev1.VolumeSource{Projected: &corev1.ProjectedVolumeSource{
			Sources: []corev1.VolumeProjection{
				{ServiceAccountToken: &corev1.ServiceAccountTokenProjection{Path: "token"}},
				{ConfigMap: &corev1.ConfigMapProjection{LocalObjectReference: corev1.LocalObjectReference{Name: "kube-root-ca.crt"}}},
			},
		}}},
	}
	pod.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "regcred"}, {Name: "api-tls"}}
	return pod
}

func TestPodConfigRefsCollectsEverySourceSecretsFirst(t *testing.T) {
	got := podConfigRefs(configuredPod("api-0", "default").Spec)
	want := []configRef{
		{Kind: kube.KindSecret, Name: "api-tls"},
		{Kind: kube.KindSecret, Name: "regcred"},
		{Kind: kube.KindSecret, Name: "resend"},
		{Kind: kube.KindSecret, Name: "smtp"},
		{Kind: kube.KindSecret, Name: "timesheet-backend-db"},
		{Kind: kube.KindConfigMap, Name: "app-config"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("podConfigRefs = %+v\nwant %+v", got, want)
	}
}

// recordingLister records every kind read, so a test can prove poddetail
// never touches the Secret or ConfigMap caches: a Secret read would start the
// cluster-wide Secret informer.
type recordingLister struct {
	fakeLister
	kinds *[]kube.ResourceKind
}

func (r recordingLister) ListRaw(ctx context.Context, kind kube.ResourceKind, ns string) ([]runtime.Object, error) {
	*r.kinds = append(*r.kinds, kind)
	return r.fakeLister.ListRaw(ctx, kind, ns)
}

func TestRelatedListsConfigRefsWithoutReadingTheirCaches(t *testing.T) {
	var kinds []kube.ResourceKind
	lister := recordingLister{
		fakeLister: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {configuredPod("api-0", "default")}}},
		kinds:      &kinds,
	}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 60)
	m = step(t, m, m.Init()())

	if slices.Contains(kinds, kube.KindSecret) || slices.Contains(kinds, kube.KindConfigMap) {
		t.Fatalf("poddetail read %v; Secret/ConfigMap caches must never be read", kinds)
	}
	var labels []string
	for _, item := range m.related {
		labels = append(labels, item.Label)
	}
	if !slices.Contains(labels, "Secret/timesheet-backend-db") || !slices.Contains(labels, "ConfigMap/app-config") {
		t.Fatalf("related = %v, want Secret/timesheet-backend-db and ConfigMap/app-config", labels)
	}
	if slices.Contains(labels, "ConfigMap/kube-root-ca.crt") {
		t.Fatalf("related = %v, the injected kube-root-ca.crt must not be listed", labels)
	}
	if view := plain(m.Render()); !strings.Contains(view, "Secret/timesheet-backend-db ↗") {
		t.Fatalf("RELATED should render the Secret link:\n%s", view)
	}
}

type stubTask struct{ namespace, name string }

func (stubTask) Init() tea.Cmd                         { return nil }
func (s stubTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return s, nil }
func (stubTask) View() tea.View                        { return tea.NewView("") }

func relatedIndex(t *testing.T, m Model, kind kube.ResourceKind, name string) int {
	t.Helper()
	for i, item := range m.related {
		if item.Kind == kind && item.Name == name {
			return i
		}
	}
	t.Fatalf("no RELATED entry %s/%s in %+v", kind, name, m.related)
	return -1
}

func TestRelatedSecretOpensItsDataView(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {configuredPod("api-0", "default")}}}
	m := New(Config{
		Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0",
		OpenSecretData: func(namespace, name string, _, _ int) (tea.Model, tea.Cmd) {
			return stubTask{namespace: namespace, name: name}, nil
		},
	})
	m.SetSize(120, 60)
	m = step(t, m, m.Init()())

	idx := relatedIndex(t, m, kube.KindSecret, "timesheet-backend-db")
	key := string(rune('1' + idx))
	task, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	got, ok := task.(stubTask)
	if !ok || got.namespace != "default" || got.name != "timesheet-backend-db" {
		t.Fatalf("digit %s returned %#v, want the Secret Data view for default/timesheet-backend-db", key, task)
	}

	// A ConfigMap entry with no OpenConfigMapData wired falls back to the
	// goto jump every other RELATED entry uses.
	idx = relatedIndex(t, m, kube.KindConfigMap, "app-config")
	key = string(rune('1' + idx))
	task, cmd := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})
	if _, ok := task.(*Model); !ok || cmd == nil {
		t.Fatalf("digit %s returned (%T, %v), want poddetail itself plus a goto cmd", key, task, cmd)
	}
	if msg, ok := cmd().(tui.GotoResourceMsg); !ok || msg.Kind != kube.KindConfigMap || msg.Name != "app-config" {
		t.Fatalf("goto cmd produced %#v, want GotoResourceMsg for ConfigMap/app-config", cmd())
	}
}

func TestEnvMountsToggleFollowsContainerSelection(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {configuredPod("api-0", "default")}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(140, 80)
	m = step(t, m, m.Init()())

	if !hasKeyHint(m.Keybar().Groups[0], verbs.EnvMounts.Key) {
		t.Fatalf("keybar should offer %q when the pod has env/mount sources: %+v", verbs.EnvMounts.Key, m.Keybar().Groups)
	}
	if strings.Contains(plain(m.Render()), "ENV & MOUNTS") {
		t.Fatal("ENV & MOUNTS must stay hidden until toggled")
	}

	m = step(t, m, tea.KeyPressMsg{Code: 'v', Text: "v"})
	view := plain(m.Render())
	for _, want := range []string{
		"ENV & MOUNTS · app",
		"ConnectionStrings__TimeSheetDb",
		"Secret/timesheet-backend-db · connectionString",
		"SMTP_*",
		"Secret/smtp · all keys",
		"ConfigMap/app-config · flags",
		"/etc/tls (ro)",
		"Secret/api-tls",
		"projected · serviceaccount token, ConfigMap/kube-root-ca.crt",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("ENV & MOUNTS missing %q:\n%s", want, view)
		}
	}
	// A literal env value is never shown, not even its name.
	if strings.Contains(view, "LOG_LEVEL") {
		t.Fatalf("literal env vars must not be listed:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Code: 'j', Text: "j"})
	view = plain(m.Render())
	if !strings.Contains(view, "ENV & MOUNTS · sidecar") || !strings.Contains(view, "Secret/resend · apiKey") {
		t.Fatalf("section should follow the selection to sidecar:\n%s", view)
	}
	if strings.Contains(view, "ConnectionStrings__TimeSheetDb") {
		t.Fatalf("app's rows should be gone once sidecar is selected:\n%s", view)
	}

	m = step(t, m, tea.KeyPressMsg{Code: 'v', Text: "v"})
	if strings.Contains(plain(m.Render()), "ENV & MOUNTS") {
		t.Fatal("second v should hide the section")
	}
}

func TestEnvMountsHintHiddenWithoutSources(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {runningPod("api-0", "default", "node-a")}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 40)
	m = step(t, m, m.Init()())
	for _, group := range m.Keybar().Groups {
		if hasKeyHint(group, verbs.EnvMounts.Key) {
			t.Fatalf("no env/mount sources, so no %q hint: %+v", verbs.EnvMounts.Key, m.Keybar().Groups)
		}
	}
}

func TestConfigErrorBannerNamesTheMissingReference(t *testing.T) {
	pod := configuredPod("api-0", "default")
	pod.Status.Phase = corev1.PodPending
	pod.Status.ContainerStatuses[0].Ready = false
	pod.Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
		Reason:  "CreateContainerConfigError",
		Message: `couldn't find key connectionString in Secret default/timesheet-backend-db`,
	}}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {pod}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "default", Name: "api-0"})
	m.SetSize(120, 60)
	m = step(t, m, m.Init()())

	view := plain(m.Render())
	for _, want := range []string{
		"Can't start container",
		"CreateContainerConfigError",
		"Container app: couldn't find key connectionString in Secret default/timesheet-backend-db",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("config-error banner missing %q:\n%s", want, view)
		}
	}
	// The banner is promoted above the meta grid, like the termination one.
	if strings.Index(view, "Can't start container") > strings.Index(view, "CONTROLLER") {
		t.Fatalf("banner should sit above the meta grid:\n%s", view)
	}
}
