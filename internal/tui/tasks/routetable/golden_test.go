package routetable

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
	"github.com/kute-dev/kute/internal/tui"
)

// sentinelTask is a minimal tea.Model stand-in for the OpenEvents seam —
// golden fixtures only need the seam wired (non-nil) so Keybar renders its
// full "Y copy yaml · e events" hint group; the stub is never actually
// invoked by a render.
type sentinelTask struct{}

func (sentinelTask) Init() tea.Cmd                       { return nil }
func (sentinelTask) Update(tea.Msg) (tea.Model, tea.Cmd) { return sentinelTask{}, nil }
func (sentinelTask) View() tea.View                      { return tea.NewView("") }

func openEventsStub(kind kube.ResourceKind, ns, name string, w, h int) (tea.Model, tea.Cmd) {
	return sentinelTask{}, nil
}

// fakeYAML satisfies YAMLReader with an empty body — wired only so Keybar's
// "Y copy yaml" hint renders; 'Y' is never pressed in these fixtures.
type fakeYAML struct{}

func (fakeYAML) GetYAML(context.Context, kube.ResourceKind, string, string) (string, string, error) {
	return "", "", nil
}

func strPtr(s string) *string { return &s }

// demoCertPEM self-signs a throwaway certificate valid from one year before
// notAfter through notAfter — mirrors kube/fake/fixtures.go's demoTLSCert,
// duplicated here since that helper is unexported outside the fake package.
func demoCertPEM(t *testing.T, notAfter time.Time) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "demo"},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// goldenIngress builds 23a's mock shape: three rules under one host, each
// landing on a different backend-health outcome — "api" resolves (● 1
// ready), "old-svc" doesn't exist (✕ service not found), "beta-svc" exists
// but has no ready pods matching its selector (◐ 0 ready). A single TLS
// block covers the host with a secret whose cert expires inside 30 days
// (§23a: "yellow <30d").
func goldenIngress() *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "nva-stage"},
		Spec: networkingv1.IngressSpec{
			IngressClassName: strPtr("nginx"),
			TLS:              []networkingv1.IngressTLS{{Hosts: []string{"nva.example.com"}, SecretName: "nva-tls"}},
			Rules: []networkingv1.IngressRule{{
				Host: "nva.example.com",
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{Path: "/api", PathType: &pathType, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "api", Port: networkingv1.ServiceBackendPort{Number: 8080}}}},
							{Path: "/legacy", PathType: &pathType, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "old-svc", Port: networkingv1.ServiceBackendPort{Number: 80}}}},
							{Path: "/beta", PathType: &pathType, Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "beta-svc", Port: networkingv1.ServiceBackendPort{Number: 8080}}}},
						},
					},
				},
			}},
		},
	}
}

func goldenIngressLister(t *testing.T) fakeLister {
	t.Helper()
	apiSel := map[string]string{"app": "api"}
	betaSel := map[string]string{"app": "beta"}
	return fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindIngress: {goldenIngress()},
		kube.KindService: {
			serviceWithSelector("api", "nva-stage", apiSel),
			// beta-svc has a selector but no matching ready pod, so
			// ResolveServiceBackend resolves it to Ready=0 -> the ◐ "0
			// ready" grammar (docs/design README.md §23a).
			serviceWithSelector("beta-svc", "nva-stage", betaSel),
			// old-svc is intentionally absent -> ✕ "service not found".
		},
		kube.KindPod: {readyPod("api-1", "nva-stage", apiSel, true)},
		kube.KindSecret: {&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "nva-tls", Namespace: "nva-stage"},
			Data:       map[string][]byte{"tls.crt": demoCertPEM(t, time.Now().Add(20*24*time.Hour))},
		}},
	}}
}

func goldenIngressModel(t *testing.T, width, height int) Model {
	t.Helper()
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	m := New(Config{
		Session: sess, Lister: goldenIngressLister(t), YAML: fakeYAML{}, OpenEvents: openEventsStub,
		Kind: kube.KindIngress, Namespace: "nva-stage", Name: "web",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 8 * time.Millisecond})
	return m
}

// goldenRoute builds 23b's mock shape: an HTTPRoute accepted by Gateway
// "public" on its "https" listener/section, one rule matching "/" with a
// 90/10 weighted split -- "web" resolves (● ready), "web-canary" has no
// ready pods (◐ 0 ready, stacked "└ same match" under the first row, its
// weight rendered in the canary-weight-yellow tone per §23b).
func goldenRoute() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata":   map[string]any{"name": "web-route", "namespace": "production"},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": "public", "sectionName": "https"}},
			"hostnames":  []any{"web.demo.local"},
			"rules": []any{
				map[string]any{
					"matches": []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}},
					"backendRefs": []any{
						map[string]any{"name": "web", "port": int64(80), "weight": int64(90)},
						map[string]any{"name": "web-canary", "port": int64(80), "weight": int64(10)},
					},
				},
			},
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{
					"parentRef":  map[string]any{"name": "public", "sectionName": "https"},
					"conditions": []any{map[string]any{"type": "Accepted", "status": "True"}},
				},
			},
		},
	}}
}

// goldenGateway is web-route's accepted parent: an HTTPS listener (TLS via
// secret "gw-tls") with 2 routes attached, plus a plain HTTP listener with
// none -- the resolved parent-listener footer strip (§23b's "below-table
// parent line") reads this back via resolveParentListenerDetail.
func goldenGateway() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata":   map[string]any{"name": "public", "namespace": "production"},
		"spec": map[string]any{
			"gatewayClassName": "nginx",
			"listeners": []any{
				map[string]any{
					"name": "https", "protocol": "HTTPS", "port": int64(443), "hostname": "*.demo.local",
					"tls": map[string]any{"certificateRefs": []any{map[string]any{"name": "gw-tls"}}},
				},
				map[string]any{"name": "http", "protocol": "HTTP", "port": int64(80), "hostname": "*.demo.local"},
			},
		},
		"status": map[string]any{
			"listeners": []any{
				map[string]any{"name": "https", "attachedRoutes": int64(2)},
				map[string]any{"name": "http", "attachedRoutes": int64(0)},
			},
		},
	}}
}

func goldenRouteLister(t *testing.T) fakeLister {
	t.Helper()
	webSel := map[string]string{"app": "web"}
	canarySel := map[string]string{"app": "web-canary"}
	return fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindHTTPRoute: {goldenRoute()},
		kube.KindGateway:   {goldenGateway()},
		kube.KindService: {
			serviceWithSelector("web", "production", webSel),
			serviceWithSelector("web-canary", "production", canarySel),
		},
		kube.KindPod: {readyPod("web-1", "production", webSel, true)},
		kube.KindSecret: {&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "gw-tls", Namespace: "production"},
			Data:       map[string][]byte{"tls.crt": demoCertPEM(t, time.Now().Add(200*24*time.Hour))},
		}},
	}}
}

func goldenRouteModel(t *testing.T, width, height int) Model {
	t.Helper()
	sess := newSession()
	sess.Location.Namespace = "production"
	m := New(Config{
		Session: sess, Lister: goldenRouteLister(t), YAML: fakeYAML{}, OpenEvents: openEventsStub,
		Kind: kube.KindHTTPRoute, Namespace: "production", Name: "web-route",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	m = step(t, m, kube.ConnStateMsg{Phase: kube.ConnConnected, Latency: 8 * time.Millisecond})
	return m
}

func goldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"ingress-120x36.golden": goldentest.Plain(goldenIngressModel(t, 120, 36).Render()),
		"ingress-80x24.golden":  goldentest.Plain(goldenIngressModel(t, 80, 24).Render()),
		"route-120x36.golden":   goldentest.Plain(goldenRouteModel(t, 120, 36).Render()),
		"route-80x24.golden":    goldentest.Plain(goldenRouteModel(t, 80, 24).Render()),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "routetable")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate routetable golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
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

// truecolorGoldenFixtures renders 23a's Ingress flavor with a forced
// truecolor profile in both themes, pinning the Theme-token-to-cell color
// mapping the plain goldens above can't see (the yellow <30d TLS cell/strip,
// the ✕/◐ backend glyphs, the selected-row background) -- same pattern as
// browse's 2a and setup's 4c (browse/golden_test.go, setup/golden_test.go).
// Scoped to the ingress flavor only, matching setup's precedent of
// truecolor'ing just its "unreachable" state and not "noconfig". The
// profile swap is global, so this package must not run these in parallel
// with other renders (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	dark := goldenIngressModel(t, 120, 36)
	light := goldenIngressModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"ingress-120x36-dark.golden":  goldentest.Truecolor(dark.Render()),
		"ingress-120x36-light.golden": goldentest.Truecolor(light.Render()),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate routetable golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range truecolorGoldenFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestTruecolorGoldenFixtures(t *testing.T) {
	for name, got := range truecolorGoldenFixtures(t) {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(goldenDir(), name)
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
