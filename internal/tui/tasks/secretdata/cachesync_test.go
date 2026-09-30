package secretdata

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// fillingLister is a lazily started cache: empty and unsynced until filled,
// the state a first read of a kind is in against a live cluster.
type fillingLister struct {
	synced bool
	objs   map[kube.ResourceKind][]runtime.Object
}

func (f *fillingLister) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	if !f.synced {
		return nil, nil
	}
	return f.objs[kind], nil
}

func (f *fillingLister) KindSynced(kube.ResourceKind, string) bool { return f.synced }

// Opening the Data view straight from pod detail's RELATED is often the first
// read of this kind, so the first load sees an empty cache. It must keep
// loading, not claim the object doesn't exist.
func TestUnsyncedCacheKeepsLoadingInsteadOfNotFound(t *testing.T) {
	lister := &fillingLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindSecret: {&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "app"}, Data: map[string][]byte{"connectionString": []byte("x")}}}}}
	m := New(Config{Session: newSession(), Lister: lister, Namespace: "app", Name: "db"})
	m.SetSize(120, 36)

	updated, cmd := m.Update(m.load()())
	m = *updated.(*Model)
	if m.state != tui.TaskStateLoading {
		t.Fatalf("state = %v (%q), want loading while the cache fills", m.state, m.feedback)
	}
	if strings.Contains(plain(m.Render()), "not found") {
		t.Fatalf("an unsynced cache must not render not found:\n%s", plain(m.Render()))
	}
	if cmd == nil {
		t.Fatal("an unsynced cache must schedule a retry")
	}

	lister.synced = true
	updated, cmd = m.Update(tui.CacheSyncRetryMsg{Gen: m.reloadEpoch})
	m = *updated.(*Model)
	if cmd == nil {
		t.Fatal("the retry should reload")
	}
	updated, _ = m.Update(cmd())
	m = *updated.(*Model)
	if m.state != tui.TaskStateReady || !strings.Contains(plain(m.Render()), "connectionString") {
		t.Fatalf("state = %v, want the secret's keys once the cache has filled:\n%s", m.state, plain(m.Render()))
	}
}
