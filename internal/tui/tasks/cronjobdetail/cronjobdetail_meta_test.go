package cronjobdetail

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/kube/fake"
)

func plainMeta(s string) string { return ansi.Strip(s) }

// newFakeLabeledCronJob is newFakeCronJob plus a label — 26a's panel needs a
// real key to show in its LABELS grid.
func newFakeLabeledCronJob(namespace, name string) *fake.Cluster {
	c, cj := newFakeCronJob(namespace, name)
	cj.Labels = map[string]string{"app": name}
	c.Seed(kube.KindCronJob, cj)
	return c
}

// The panel's own behavior is tested in internal/tui/metapanel — what's
// covered here is cronjobdetail's hosting glue: 'm' opens it on the loaded
// CronJob, it captures keys (j/k stop moving the Jobs table, esc closes the
// panel rather than popping the screen), and the keybar swaps to META.
func TestMetaKeyOpensPanelOnCronJob(t *testing.T) {
	c := newFakeLabeledCronJob("default", "nightly")
	m := newModel(t, c, "default", "nightly")

	m = step(t, m, tea.KeyPressMsg{Text: "m"})
	if m.meta == nil {
		t.Fatal("'m' should open the labels/annotations panel")
	}
	if !m.CapturingInput() {
		t.Error("the open panel should capture input (no global g/n/c routing)")
	}
	if kb := m.Keybar(); kb.PillText != "META" {
		t.Errorf("keybar pill = %q, want META", kb.PillText)
	}
	body := plainMeta(m.Body(120, 34))
	if !strings.Contains(body, "LABELS · 1") || !strings.Contains(body, "app") {
		t.Fatalf("panel body missing the labels grid:\n%s", body)
	}
	// The title line stays frozen above the panel (the detail-screen
	// analogue of §26a's selected-row framing).
	if !strings.Contains(body, "nightly") {
		t.Error("panel body should keep the cronjob's own title line above the grid")
	}

	// While open, j/k belong to the panel's row cursor, never the Jobs
	// table's selection, and [/] must not move to a sibling.
	before := m.selectedJob
	m = step(t, m, tea.KeyPressMsg{Text: "j"})
	if m.selectedJob != before {
		t.Error("'j' with the panel open must not move the Jobs table selection")
	}

	// esc closes the panel and stays on the detail screen — it must not
	// bubble into cronjobdetail's own esc-goes-back binding.
	m = step(t, m, tea.KeyPressMsg{Text: "esc"})
	if m.meta != nil {
		t.Fatal("esc should close the panel")
	}
	if !strings.Contains(plainMeta(m.Body(120, 34)), "associated with this cronjob") {
		t.Error("closing the panel should land back on the detail body")
	}
}

func TestMetaKeyIsNoOpWithoutMutator(t *testing.T) {
	c := newFakeLabeledCronJob("default", "nightly")
	m := New(Config{Session: newSession("default"), Lister: c, Namespace: "default", Name: "nightly"})
	m.SetSize(120, 40)
	m = step(t, m, m.load()())

	m = step(t, m, tea.KeyPressMsg{Text: "m"})
	if m.meta != nil {
		t.Error("'m' without a mutator should stay a no-op (read-only session)")
	}
}
