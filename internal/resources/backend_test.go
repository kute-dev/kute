package resources

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

func readyPod(name, ns string, labels map[string]string, ready bool) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: labels},
		Status:     corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{{Ready: ready}}},
	}
}

func serviceWithSelector(name, ns string, selector map[string]string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.ServiceSpec{Selector: selector},
	}
}

func TestResolveServiceBackend(t *testing.T) {
	sel := map[string]string{"app": "web"}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindService: {
			serviceWithSelector("web", "default", sel),
			serviceWithSelector("headless", "default", nil),
		},
		kube.KindPod: {
			readyPod("web-1", "default", sel, true),
			readyPod("web-2", "default", sel, false),
			readyPod("other", "default", map[string]string{"app": "other"}, true),
		},
	}}

	tests := []struct {
		name         string
		service      string
		wantExists   bool
		wantReady    int
		wantTotal    int
		wantUnres    bool
		wantGlyph    string
		wantGlyphCls StatusClass
	}{
		{name: "ready and not-ready pods", service: "web", wantExists: true, wantReady: 1, wantTotal: 2, wantGlyph: "●", wantGlyphCls: StatusOK},
		{name: "not found", service: "missing", wantExists: false, wantGlyph: "✕", wantGlyphCls: StatusFail},
		{name: "no selector", service: "headless", wantExists: true, wantUnres: true, wantGlyph: "●", wantGlyphCls: StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := ResolveServiceBackend(context.Background(), lister, "default", tt.service)
			if state.Exists != tt.wantExists {
				t.Fatalf("Exists = %v, want %v", state.Exists, tt.wantExists)
			}
			if state.Ready != tt.wantReady || state.Total != tt.wantTotal {
				t.Fatalf("Ready/Total = %d/%d, want %d/%d", state.Ready, state.Total, tt.wantReady, tt.wantTotal)
			}
			if state.Unresolvable != tt.wantUnres {
				t.Fatalf("Unresolvable = %v, want %v", state.Unresolvable, tt.wantUnres)
			}
			glyph, class := state.Glyph()
			if glyph != tt.wantGlyph || class != tt.wantGlyphCls {
				t.Fatalf("Glyph() = %q/%v, want %q/%v", glyph, class, tt.wantGlyph, tt.wantGlyphCls)
			}
		})
	}
}

func TestResolveServiceBackendZeroReady(t *testing.T) {
	sel := map[string]string{"app": "ghost"}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindService: {serviceWithSelector("ghost-svc", "default", sel)},
	}}
	state := ResolveServiceBackend(context.Background(), lister, "default", "ghost-svc")
	if !state.Exists || state.Ready != 0 || state.Total != 0 {
		t.Fatalf("unexpected state: %+v", state)
	}
	glyph, class := state.Glyph()
	if glyph != "◐" || class != StatusWarn {
		t.Fatalf("Glyph() = %q/%v, want ◐/warn", glyph, class)
	}
}

func TestResolveServiceBackendNilLister(t *testing.T) {
	state := ResolveServiceBackend(context.Background(), nil, "default", "web")
	if state.Exists {
		t.Fatalf("expected zero-value state for a nil lister, got %+v", state)
	}
}
