package routetable

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

type fakeLister struct {
	objs map[kube.ResourceKind][]runtime.Object
}

func (f fakeLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	return f.objs[kind], nil
}

func plain(s string) string { return ansi.Strip(s) }

func newSession() *tui.Session {
	return &tui.Session{Theme: tui.Dark(), Location: tui.Location{Context: "test-cluster"}}
}

func step(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				m = step(t, m, c())
			}
		}
		return m
	}
	updated, cmd := m.Update(msg)
	next := *updated.(*Model)
	if cmd != nil {
		if msg := cmd(); msg != nil {
			return step(t, next, msg)
		}
	}
	return next
}

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

func testIngress() *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: "web.local",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{Path: "/", PathType: &pathType, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "web", Port: networkingv1.ServiceBackendPort{Number: 80}}}},
							{Path: "/admin", PathType: &pathType, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "missing", Port: networkingv1.ServiceBackendPort{Number: 80}}}},
						},
					},
				},
			}},
		},
	}
}

func TestLoadIngressResolvesBackendsAndTLS(t *testing.T) {
	sel := map[string]string{"app": "web"}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindIngress: {testIngress()},
		kube.KindService: {serviceWithSelector("web", "default", sel)},
		kube.KindPod:     {readyPod("web-1", "default", sel, true)},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindIngress, Namespace: "default", Name: "web"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.rows))
	}
	if m.rows[0].glyph != "●" || m.rows[0].class != "ok" {
		t.Fatalf("expected the resolved backend row to be ●/ok, got %+v", m.rows[0])
	}
	if m.rows[1].glyph != "✕" || m.rows[1].class != "fail" {
		t.Fatalf("expected the missing-service row to be ✕/fail, got %+v", m.rows[1])
	}
	if m.rows[0].url != "http://web.local/" {
		t.Fatalf("unexpected url: %q", m.rows[0].url)
	}

	view := plain(m.Render())
	for _, want := range []string{"web.local /", "web:80", "missing:80"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

func TestLoadIngressTLSExpiry(t *testing.T) {
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{Hosts: []string{"web.local"}, SecretName: "web-tls"}},
		},
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindIngress: {ing},
		kube.KindSecret:  {&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "web-tls", Namespace: "default"}}},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindIngress, Namespace: "default", Name: "web"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if len(m.tlsFacts) != 1 || m.tlsFacts[0].expiry != "no cert data" {
		t.Fatalf("unexpected tlsFacts: %+v", m.tlsFacts)
	}
}

// TestTabTogglesTLSFocusAndArrowsMoveWithinIt pins 23a's "tab" toggle
// (docs/design README.md:285: "a strip above the keybar names each secret —
// ↵ there jumps to it"): 'tab' moves focus onto the TLS strip instead of the
// main table, and up/down move the focused fact instead of the main table's
// own selection while focused.
func TestTabTogglesTLSFocusAndArrowsMoveWithinIt(t *testing.T) {
	m := Model{flavor: flavorIngress, tlsFacts: []tlsFact{{secretName: "a"}, {secretName: "b"}}}
	m.SetSize(120, 36)

	if m.tlsFocused {
		t.Fatal("expected the TLS strip to start unfocused")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Text: "tab"})
	m = *updated.(*Model)
	if !m.tlsFocused {
		t.Fatal("expected 'tab' to focus the TLS strip")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "down"})
	m = *updated.(*Model)
	if m.tlsSelected != 1 {
		t.Fatalf("tlsSelected = %d, want 1 (down moved within the TLS strip, not the main table)", m.tlsSelected)
	}
	if m.selected != 0 {
		t.Fatalf("main table selected = %d, want unchanged at 0 while TLS-focused", m.selected)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Text: "tab"})
	m = *updated.(*Model)
	if m.tlsFocused {
		t.Fatal("expected a second 'tab' to unfocus the TLS strip")
	}
}

// TestOpenSelectedTLSSecretJumpsToReferencedSecret pins 23a's ↵ behavior
// once the TLS strip has focus, mirroring TestOpenSelectedEnterJumpsToBackendService's
// direct-Model style.
func TestOpenSelectedTLSSecretJumpsToReferencedSecret(t *testing.T) {
	m := Model{
		flavor: flavorIngress, namespace: "default",
		tlsFocused: true, tlsSelected: 0,
		tlsFacts: []tlsFact{{secretName: "web-tls"}},
	}
	cmd, ok := m.openSelectedTLSSecret()
	if !ok || cmd == nil {
		t.Fatalf("expected a jump cmd for a focused TLS fact with a secret name")
	}

	m = Model{flavor: flavorIngress, tlsFocused: true, tlsSelected: 0, tlsFacts: nil}
	if _, ok := m.openSelectedTLSSecret(); ok {
		t.Fatal("expected no jump with no TLS facts")
	}
}

func testHTTPRoute(name, parent, condStatus, message string, backends []map[string]any) *unstructured.Unstructured {
	cond := map[string]any{"type": "Accepted", "status": condStatus}
	if message != "" {
		cond["message"] = message
	}
	refs := make([]any, len(backends))
	for i, b := range backends {
		refs[i] = b
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": parent}},
			"rules": []any{
				map[string]any{
					"matches":     []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}},
					"backendRefs": refs,
				},
			},
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{"parentRef": map[string]any{"name": parent, "sectionName": "https"}, "conditions": []any{cond}},
			},
		},
	}}
}

func TestLoadHTTPRouteWeightedSplitAndParent(t *testing.T) {
	sel := map[string]string{"app": "web"}
	route := testHTTPRoute("web-route", "public", "True", "", []map[string]any{
		{"name": "web", "port": int64(80), "weight": int64(90)},
		{"name": "web-canary", "port": int64(80), "weight": int64(10)},
	})
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHTTPRoute: {route},
		kube.KindService:   {serviceWithSelector("web", "default", sel)},
		kube.KindPod:       {readyPod("web-1", "default", sel, true)},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindHTTPRoute, Namespace: "default", Name: "web-route"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(m.rows))
	}
	if m.rows[0].match == "" || m.rows[0].weightPct != "90%" {
		t.Fatalf("unexpected first row: %+v", m.rows[0])
	}
	if m.rows[1].match != "" || m.rows[1].weightPct != "10%" {
		t.Fatalf("expected the second row to be a same-match continuation, got %+v", m.rows[1])
	}
	if !m.parentAttached || m.parentGatewayName != "public" {
		t.Fatalf("expected an accepted parent pointing at 'public', got attached=%v name=%q", m.parentAttached, m.parentGatewayName)
	}

	view := plain(m.Render())
	if !strings.Contains(view, "same match") {
		t.Fatalf("view missing continuation row marker:\n%s", view)
	}
}

func TestLoadHTTPRouteNotAccepted(t *testing.T) {
	route := testHTTPRoute("orphan", "public", "False", "no matching listener hostname", []map[string]any{
		{"name": "web", "port": int64(80)},
	})
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindHTTPRoute: {route}}}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindHTTPRoute, Namespace: "default", Name: "orphan"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.parentAttached {
		t.Fatalf("expected a rejected route to report parentAttached=false")
	}
	view := plain(m.Render())
	if !strings.Contains(view, "not accepted") {
		t.Fatalf("view missing rejection text:\n%s", view)
	}
}

func testGateway(name string, attachedHTTPS int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": name, "namespace": "default"},
		"spec": map[string]any{
			"gatewayClassName": "nginx",
			"listeners": []any{
				map[string]any{"name": "https", "protocol": "HTTPS", "port": int64(443), "hostname": "*.demo.local"},
				map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80)},
			},
		},
		"status": map[string]any{
			"listeners": []any{
				map[string]any{"name": "https", "attachedRoutes": attachedHTTPS},
				map[string]any{"name": "http", "attachedRoutes": int64(0)},
			},
		},
	}}
}

func TestLoadGatewayListeners(t *testing.T) {
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindGateway: {testGateway("public", 3)},
	}}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindGateway, Namespace: "default", Name: "public"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateReady {
		t.Fatalf("state = %s, want ready (feedback %q)", m.state, m.feedback)
	}
	if len(m.listeners) != 2 {
		t.Fatalf("listeners = %d, want 2", len(m.listeners))
	}
	if m.listeners[0].name != "https" || m.listeners[0].attached != 3 {
		t.Fatalf("unexpected https listener: %+v", m.listeners[0])
	}
	if m.listeners[0].hostname != "*.demo.local" || m.listeners[1].hostname != "*" {
		t.Fatalf("unexpected hostnames: %+v / %+v", m.listeners[0], m.listeners[1])
	}
}

func TestNotFoundRendersError(t *testing.T) {
	lister := fakeLister{}
	m := New(Config{Session: newSession(), Lister: lister, Kind: kube.KindIngress, Namespace: "default", Name: "missing"})
	m.SetSize(120, 36)
	m = step(t, m, m.Init()())

	if m.state != tui.TaskStateError {
		t.Fatalf("state = %s, want error", m.state)
	}
	if !strings.Contains(m.feedback, "not found") {
		t.Fatalf("unexpected feedback: %q", m.feedback)
	}
}

func TestOpenSelectedEnterJumpsToBackendService(t *testing.T) {
	m := Model{flavor: flavorIngress, rows: []routeRow{{backendNS: "default", backendName: "web"}}}
	cmd, ok := m.openSelectedEnter()
	if !ok || cmd == nil {
		t.Fatalf("expected an enter jump for a resolved backend row")
	}

	m = Model{flavor: flavorIngress, rows: nil}
	if _, ok := m.openSelectedEnter(); ok {
		t.Fatalf("expected no jump with no rows")
	}
}

// TestSelectedListenerRouteFilterUsesHostnameOrFallsBackToGateway pins 23b
// (docs/design README.md:292: "↵ on a listener filters to attached routes"):
// a listener with its own hostname filters by that hostname; a wildcard
// listener (no hostname) falls back to this Gateway's own ATTACHED-cell
// text so it still narrows to this Gateway's routes rather than showing
// every HTTPRoute in the namespace.
func TestSelectedListenerRouteFilterUsesHostnameOrFallsBackToGateway(t *testing.T) {
	m := Model{flavor: flavorGateway, name: "public", listeners: []listenerRow{{name: "https", hostname: "api.example.com"}}}
	filter, ok := m.selectedListenerRouteFilter()
	if !ok || filter != "api.example.com" {
		t.Fatalf("filter = %q, ok=%v, want \"api.example.com\", true", filter, ok)
	}

	m = Model{flavor: flavorGateway, name: "public", listeners: []listenerRow{{name: "http", hostname: ""}}}
	filter, ok = m.selectedListenerRouteFilter()
	if !ok || filter != "gw/public" {
		t.Fatalf("filter = %q, ok=%v, want \"gw/public\", true (wildcard listener fallback)", filter, ok)
	}

	m = Model{flavor: flavorGateway}
	if _, ok := m.selectedListenerRouteFilter(); ok {
		t.Fatal("expected no filter with no selected listener")
	}
}

func TestOpenSelectedEnterGatewayJumpsToHTTPRouteKind(t *testing.T) {
	m := Model{flavor: flavorGateway, listeners: []listenerRow{{name: "https"}}}
	cmd, ok := m.openSelectedEnter()
	if !ok || cmd == nil {
		t.Fatalf("expected a gateway enter jump when listeners exist")
	}

	m = Model{flavor: flavorGateway}
	if _, ok := m.openSelectedEnter(); ok {
		t.Fatalf("expected no jump with no listeners")
	}
}

func TestOpenParentGatewayOnlyForRouteFlavor(t *testing.T) {
	m := Model{flavor: flavorRoute, parentGatewayNS: "default", parentGatewayName: "public"}
	if _, ok := m.openParentGateway(); !ok {
		t.Fatalf("expected a parent-gateway jump")
	}

	m = Model{flavor: flavorIngress, parentGatewayNS: "default", parentGatewayName: "public"}
	if _, ok := m.openParentGateway(); ok {
		t.Fatalf("expected no parent-gateway jump outside the route flavor")
	}

	m = Model{flavor: flavorRoute}
	if _, ok := m.openParentGateway(); ok {
		t.Fatalf("expected no parent-gateway jump with no resolved parent")
	}
}

func TestCopySelectedURLOnlyForIngressFlavor(t *testing.T) {
	m := Model{flavor: flavorIngress, rows: []routeRow{{url: "https://web.local/"}}}
	if _, ok := m.copySelectedURL(); !ok {
		t.Fatalf("expected a copy for an ingress row with a url")
	}

	m = Model{flavor: flavorRoute, rows: []routeRow{{url: ""}}}
	if _, ok := m.copySelectedURL(); ok {
		t.Fatalf("expected no copy outside the ingress flavor")
	}
}

func TestKeybarPillAndHints(t *testing.T) {
	m := Model{state: tui.TaskStateReady, flavor: flavorIngress, rows: []routeRow{{}}}
	kb := m.Keybar()
	if kb.PillText != "ROUTES" {
		t.Fatalf("PillText = %q, want ROUTES", kb.PillText)
	}

	m = Model{state: tui.TaskStateReady, flavor: flavorGateway, listeners: []listenerRow{{}}}
	kb = m.Keybar()
	if kb.PillText != "GATEWAY" {
		t.Fatalf("PillText = %q, want GATEWAY", kb.PillText)
	}
}
