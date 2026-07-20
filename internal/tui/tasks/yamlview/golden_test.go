// Golden-file coverage for the two 8a-family states this package actually
// implements today: 8a itself (read-only, folded, syntax-colored YAML) and
// 21a (Secret masking/reveal layered on top of it). Same overall structure
// as setup/golden_test.go (one golden_test.go, prefixed fixture names for
// multiple distinct states in one package) and poddetail/browse's
// goldenDir()+TestGenerateGoldenFixtures+TestGoldenFixtures+truecolor
// pattern.
//
// There is no "edit-*" fixture set. docs/design README.md §17a describes an
// in-app buffer editor reached by 'e' inside 8a — managedFields/status
// stripped (not folded), a purple gutter bar + "#e8c74a" new-value +
// dim "· was …" annotation on changed lines, a change-strip summary line
// above the keybar, ctrl-s dry-run/apply, and a resourceVersion-conflict
// banner. None of that exists in this package: Model has no editing/dirty/
// diff fields, update.go's updateKey switch has no "e" case, and keys.go's
// Keybar() never emits an EDIT pill. The only "edit" verb wired up anywhere
// in the app is a different, simpler mechanism — 'E' on a browse row shells
// out to `kubectl edit` via tea.ExecProcess (browse/edit.go,
// poddetail/edit.go, nodedetail/edit.go's beginEdit — see the doc comment
// on browse/edit.go) and never touches this package at all. Snapshotting an
// "edit mode" for yamlview would mean rendering UI this package cannot
// produce — left out rather than fabricated; see this package's PR/task
// notes for the gap.
package yamlview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// goldenReadYAML is a deterministic Deployment manifest exercising 8a edge
// to edge: quoted string values (YamlStr), bare numbers (Theme.Warn per
// highlight.go's tokenizeValue), nested list/map structure, and a
// multi-line "status:" block that defaultFolds (fold.go) folds by default
// alongside managedFields — the two fold summaries §8a calls out
// ("managedFields and verbose status blocks collapse to one dim line").
var goldenReadYAML = strings.Join([]string{
	"apiVersion: apps/v1",
	"kind: Deployment",
	"metadata:",
	"  name: nva-worker",
	"  namespace: nva-stage",
	"  labels:",
	"    app: nva-worker",
	"    tier: backend",
	"spec:",
	"  replicas: 4",
	"  selector:",
	"    matchLabels:",
	"      app: nva-worker",
	"  template:",
	"    spec:",
	"      containers:",
	"      - name: worker",
	"        image: \"nva/worker:1.42.0\"",
	"        ports:",
	"        - containerPort: 8080",
	"        env:",
	"        - name: LOG_LEVEL",
	"          value: \"info\"",
	"        resources:",
	"          limits:",
	"            cpu: \"500m\"",
	"            memory: \"256Mi\"",
	"status:",
	"  observedGeneration: 4",
	"  replicas: 4",
	"  updatedReplicas: 4",
	"  readyReplicas: 4",
	"  availableReplicas: 4",
	"  conditions:",
	"  - type: Available",
	"    status: \"True\"",
	"    reason: MinimumReplicasAvailable",
	"  - type: Progressing",
	"    status: \"True\"",
	"    reason: NewReplicaSetAvailable",
}, "\n")

// goldenReadDeployment supplies the ManagedFields entries load.go pulls
// from the raw object (kube.ManagedFieldsYAML) — three managers, so the
// spliced-in fold summary reads "managedFields (3 lines folded)".
func goldenReadDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "nva-worker", Namespace: "nva-stage",
			ManagedFields: []metav1.ManagedFieldsEntry{
				{Manager: "kubectl-client-side-apply"},
				{Manager: "deployment-controller"},
				{Manager: "kube-controller-manager"},
			},
		},
	}
}

// goldenReadModel builds a ready 8a screen: cursor parked on the container
// image line (mid-document, past metadata/managedFields, well before the
// folded status block) so the golden pins the cursor-line highlight
// (view.go's SelBg band across gutter + content) landing on a line that
// mixes a Key, Punct, and a quoted String token.
func goldenReadModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := &fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindDeployment: {goldenReadDeployment()},
	}}
	m := New(Config{
		Session: newSession(), Lister: lister,
		YAML:      fakeYAML{text: goldenReadYAML, resourceVersion: "48213"},
		Kind:      kube.KindDeployment,
		Namespace: "nva-stage", Name: "nva-worker",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())

	cursor := -1
	for i, rl := range m.rendered() {
		if strings.Contains(rl.Text, `image: "nva/worker:1.42.0"`) {
			cursor = i
			break
		}
	}
	if cursor == -1 {
		t.Fatalf("expected to find the container image line in rendered output: %+v", m.rendered())
	}
	m.cursor = cursor
	m.clampOffset()
	return m
}

// goldenSecretYAML mirrors secret_test.go's secretYAML shape but with three
// data: keys, so the golden shows a masked entry, a second masked entry,
// and (via goldenSecretModel revealing "username") one revealed entry in
// the same fixture — exactly what §21a's strip line ("N keys · M revealed")
// is meant to describe when the two states coexist.
var goldenSecretYAML = strings.Join([]string{
	"apiVersion: v1",
	"kind: Secret",
	"metadata:",
	"  name: nva-worker-creds",
	"  namespace: nva-stage",
	"data:",
	"  api-token: " + b64("sk-live-4f8a9c2b1d3e"),
	"  db-password: " + b64("S7q!rT2vLp9x"),
	"  username: " + b64("svc-nva-worker"),
	"type: Opaque",
}, "\n")

// goldenSecretModel builds a ready 21a screen with "username" revealed in
// place (plaintext + the bordered "revealed" tag) and cursor parked on the
// still-masked "db-password" entry — so the fixture exercises the masked
// placeholder, the revealed row, and the cursor-highlight band all at once.
func goldenSecretModel(t *testing.T, width, height int) Model {
	t.Helper()
	lister := &fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: {testSecret("nva-worker-creds", "nva-stage")},
	}}
	m := New(Config{
		Session: newSession(), Lister: lister,
		YAML:      fakeYAML{text: goldenSecretYAML, resourceVersion: "9042"},
		Kind:      kube.KindSecret,
		Namespace: "nva-stage", Name: "nva-worker-creds",
	})
	m.SetSize(width, height)
	m = step(t, m, m.Init()())

	m.revealed["username"] = true
	secretDataCursor(t, &m, "db-password")
	m.clampOffset()
	return m
}

func goldenYamlviewFixtures(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"read-120x36.golden":   goldenReadModel(t, 120, 36).Render(),
		"read-80x24.golden":    goldenReadModel(t, 80, 24).Render(),
		"secret-120x36.golden": goldenSecretModel(t, 120, 36).Render(),
		"secret-80x24.golden":  goldenSecretModel(t, 80, 24).Render(),
	}
}

func goldenDir() string {
	return filepath.Join("..", "..", "..", "..", "test", "golden", "yamlview")
}

func TestGenerateGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate yamlview golden fixtures")
	}
	if err := os.MkdirAll(goldenDir(), 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}
	for name, got := range goldenYamlviewFixtures(t) {
		if err := os.WriteFile(filepath.Join(goldenDir(), name), []byte(got), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestGoldenFixtures(t *testing.T) {
	for name, got := range goldenYamlviewFixtures(t) {
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

// truecolorGoldenFixtures renders both states with a forced truecolor
// profile in both themes — the fixtures that actually pin the
// Theme-token-to-cell color mapping (YamlKey/YamlStr/YamlPunct/Warn syntax
// colors, the cursor SelBg band, the masked-vs-revealed secret value
// colors, the "revealed" tag's Warn-filled pill), since the plain goldens
// above render colorless under test. Same pattern as browse's 2a and
// poddetail's 5a (browse/golden_test.go, poddetail/golden_test.go). The
// profile is a global, so this package must not run these in parallel with
// other renders (none of them do).
func truecolorGoldenFixtures(t *testing.T) map[string]string {
	t.Helper()
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(old)

	darkRead := goldenReadModel(t, 120, 36)
	lightRead := goldenReadModel(t, 120, 36)
	lightRead.session.Theme = tui.Light()

	darkSecret := goldenSecretModel(t, 120, 36)
	lightSecret := goldenSecretModel(t, 120, 36)
	lightSecret.session.Theme = tui.Light()

	return map[string]string{
		"read-120x36-dark.golden":    darkRead.Render(),
		"read-120x36-light.golden":   lightRead.Render(),
		"secret-120x36-dark.golden":  darkSecret.Render(),
		"secret-120x36-light.golden": lightSecret.Render(),
	}
}

func TestGenerateTruecolorGoldenFixtures(t *testing.T) {
	if os.Getenv("UPDATE_GOLDEN") != "1" {
		t.Skip("set UPDATE_GOLDEN=1 to regenerate yamlview golden fixtures")
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
