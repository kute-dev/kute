package browse

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// TestKeybarNoDuplicateKeysPerKind is the regression lint v.0.3.0.dc.html
// §29a's header note calls for: RolloutRestart and SetResources once both
// bound 'R' on the same Deployment row (fixed by moving RolloutRestart to
// 'r') and nothing guarded against it recurring. This renders the real,
// fully-wired Keybar() — mutator, forwards, and every openXxx callback
// present, so every conditional group in keys.go is live — for each kind
// that composes more than a bare "open" hint, and fails if any two hints in
// the flattened Groups (plus RightHints) share a Key.
func TestKeybarNoDuplicateKeysPerKind(t *testing.T) {
	t.Parallel()

	stub := func(kube.Pod, []string, int, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }
	openLogs := func(kube.Pod, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }
	openForward := func(kube.ForwardTarget, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }
	openNodeDetail := func(string, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }
	openHelmValues := func(kube.HelmRelease, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }
	openHelmHistory := func(string, string, int, int) (tea.Model, tea.Cmd) { return stubTask{}, nil }

	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}}

	tests := []struct {
		kind   kube.ResourceKind
		lister fakeLister
		mut    bool
	}{
		{kube.KindPod, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: {pod("default", "api-1")}}}, true},
		{kube.KindNode, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindNode: {nodeObj("node-a", true, false)}}}, true},
		{kube.KindDeployment, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindDeployment: {deploymentObj("default", "api")}}}, true},
		{kube.KindStatefulSet, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindStatefulSet: {statefulSetObj("default", "db", 3, 3)}}}, true},
		{kube.KindDaemonSet, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindDaemonSet: {daemonSetObj("default", "agent", 3, 3)}}}, true},
		{kube.KindService, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindService: {svc}}}, true},
		{kube.KindHelmRelease, fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindHelmRelease: {helmRelease("default", "redis", "redis", "18.1.5", "7.2.4", "deployed", 2)}}}, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.kind), func(t *testing.T) {
			session := newSession()
			session.Location.Kind = tt.kind
			cfg := Config{
				Session:         session,
				Lister:          tt.lister,
				OpenPodDetail:   stub,
				OpenLogs:        openLogs,
				OpenForward:     openForward,
				OpenNodeDetail:  openNodeDetail,
				OpenHelmValues:  openHelmValues,
				OpenHelmHistory: openHelmHistory,
			}
			if tt.mut {
				cfg.Mutator = &fakeMutator{}
			}
			m := New(cfg)
			m.SetSize(120, 36)
			m = step(t, m, m.Init()())

			if m.state != tui.TaskStateReady {
				t.Fatalf("%s: state = %s, want ready", tt.kind, m.state)
			}

			seen := map[string]string{}
			for _, group := range m.Keybar().Groups {
				for _, hint := range group {
					if owner, dup := seen[hint.Key]; dup {
						t.Fatalf("%s: key %q bound to both %q and %q on the same row", tt.kind, hint.Key, owner, hint.Label)
					}
					seen[hint.Key] = hint.Label
				}
			}
		})
	}
}
