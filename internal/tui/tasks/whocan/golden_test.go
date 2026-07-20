package whocan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenWhoCanResult is §22a's fullest worked example: the query pre-filled
// from a 403 card ("who can list secrets in nva-stage"), a couple of
// granted subjects resolving through different binding shapes — a plain
// namespace-scoped role/rolebinding chain and a cluster-scoped, aggregated
// ClusterRole chain (already flattened to its effective rule server-side,
// per rbac.go's own WhoCanSubject.Via doc) — plus the pinned current user,
// denied, with the closest-miss VIA §22a itself quotes verbatim.
func goldenWhoCanResult() kube.WhoCanResult {
	return kube.WhoCanResult{
		Subjects: []kube.WhoCanSubject{
			{
				Name: "sre-admins", Kind: "Group",
				Via:          "clusterrole/admin (aggregated) ← clusterrolebinding/sre-admins",
				ClusterScope: true,
				BindingKind:  kube.KindClusterRoleBinding,
				BindingName:  "sre-admins",
			},
			{
				Name: "bob", Kind: "User",
				Via:              "role/secret-reader ← rolebinding/secret-readers",
				BindingKind:      kube.KindRoleBinding,
				BindingNamespace: "nva-stage",
				BindingName:      "secret-readers",
			},
		},
		CurrentUser:        "dev-readonly",
		CurrentUserGranted: false,
		CurrentUserVia:     "role/viewer grants get, list on pods — not secrets",
	}
}

// goldenWhoCanModel builds §22a's fullest case: verb=list, resource=secrets,
// namespace=nva-stage — the design doc's own worked example question — with
// the pinned denied current-user row plus a couple of granted subjects
// selected so the SUBJECT/KIND/VIA/SCOPE table shows both a namespace and a
// cluster scoped row.
func goldenWhoCanModel(t *testing.T, width, height int) Model {
	t.Helper()
	rbac := &fakeRBAC{result: goldenWhoCanResult()}
	sess := newSession()
	sess.Location.Namespace = "nva-stage"
	m := New(Config{
		Session: sess, RBAC: rbac,
		Verb: "list", Resource: "secrets", Namespace: "nva-stage",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())
	return m
}

func goldenWhoCanFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"120x36.golden": goldenWhoCanModel(t, 120, 36).Render(),
		"80x24.golden":  goldenWhoCanModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "whocan")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate whocan golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenWhoCanFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenWhoCanFixtures(t) {
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

// truecolorGoldenFixtures renders 22a with a forced truecolor profile in
// both themes, pinning the per-cell color mapping (pinned-row red ✕, group
// scope blue vs namespace dim, VIA dim text) that the plain profile-less
// goldens above can't see. The profile swap is global, so these tests must
// not run parallel with other renders in this package (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	dark := goldenWhoCanModel(t, 120, 36)
	light := goldenWhoCanModel(t, 120, 36)
	light.session.Theme = tui.Light()
	return map[string]string{
		"120x36-dark.golden":  dark.Render(),
		"120x36-light.golden": light.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate whocan golden fixtures")
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
