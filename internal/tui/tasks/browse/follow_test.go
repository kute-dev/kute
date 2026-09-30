package browse

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// followInto drives a fresh browse (seeded on Secrets in "qa", as the root's
// routeGoto builds it) through a tui.FollowObjectMsg for secret "db" in
// "stage", draining commands until a load lands or the model hands off.
func followInto(t *testing.T, stage []runtime.Object) (*Model, tea.Model, *string) {
	t.Helper()
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{
		kube.KindSecret: append([]runtime.Object{secret("qa", "db")}, stage...),
	}}
	session := newSession()
	session.Location.Kind = kube.KindSecret
	session.Location.Namespace = "qa"
	opened := new(string)
	m := New(Config{Session: session, Lister: lister, OpenSecretData: func(namespace, name string, _, _ int) (tea.Model, tea.Cmd) {
		*opened = namespace + "/" + name
		return stubTask{}, nil
	}})
	m.SetSize(120, 36)

	var out tea.Model = &m
	queue := []tea.Msg{tui.FollowObjectMsg{Kind: kube.KindSecret, Namespace: "stage", Name: "db"}}
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					queue = append(queue, c())
				}
			}
			continue
		}
		if _, ok := msg.(rowsLoadedMsg); !ok {
			if _, ok := msg.(tui.FollowObjectMsg); !ok {
				continue // spinner ticks, metrics polls: not part of the follow
			}
		}
		var cmd tea.Cmd
		out, cmd = m.Update(msg)
		if _, ok := out.(*Model); !ok {
			break
		}
		if cmd != nil {
			queue = append(queue, cmd())
		}
	}
	return &m, out, opened
}

func TestFollowObjectReopensSameNamedObjectInNewNamespace(t *testing.T) {
	m, out, opened := followInto(t, []runtime.Object{secret("stage", "db"), secret("stage", "api")})
	if _, ok := out.(stubTask); !ok {
		t.Fatalf("expected the followed Secret's Data view, got %T", out)
	}
	if *opened != "stage/db" {
		t.Fatalf("opened %q, want stage/db", *opened)
	}
	if !m.Transient() {
		t.Fatal("expected the intermediate browse to report Transient so esc returns to the original object")
	}
}

func TestFollowObjectMissingStaysOnListWithNote(t *testing.T) {
	m, out, opened := followInto(t, []runtime.Object{secret("stage", "api")})
	if out != tea.Model(m) {
		t.Fatalf("expected to stay on the list, got %T", out)
	}
	if *opened != "" || m.Transient() {
		t.Fatalf("expected nothing opened (opened=%q, transient=%v)", *opened, m.Transient())
	}
	if m.namespace != "stage" || m.state != tui.TaskStateReady {
		t.Fatalf("expected Secrets in stage, Ready; got %q %v", m.namespace, m.state)
	}
	strips := strings.Join(m.Strips(120), "\n")
	if !strings.Contains(strips, "Secret db isn't in stage") {
		t.Fatalf("expected the missing-object note, got:\n%s", strips)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown, Text: "j"})
	if strings.Contains(strings.Join(m.Strips(120), "\n"), "isn't in") {
		t.Fatal("expected the note to clear on the next key")
	}
}
