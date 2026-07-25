package browse

import (
	"context"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// recordingLister notes every kind read, so a test can assert what a screen
// warmed rather than what it rendered.
type recordingLister struct {
	mu    sync.Mutex
	kinds []kube.ResourceKind
	inner fakeLister
}

func (l *recordingLister) ListRaw(ctx context.Context, kind kube.ResourceKind, ns string) ([]runtime.Object, error) {
	l.mu.Lock()
	l.kinds = append(l.kinds, kind)
	l.mu.Unlock()
	return l.inner.ListRaw(ctx, kind, ns)
}

func (l *recordingLister) read(kind kube.ResourceKind) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range l.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// TestAuxKindsReloadTheList: browse subscribed only to the kind it displays,
// but several lists are built from more than one cache. A Deployment's IMAGE
// cell compares against ReplicaSets, so a rollout has to reload the list.
func TestAuxKindsReloadTheList(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindReplicaSet})
	if cmd == nil || m.reloadEpoch == before {
		t.Fatal("a ReplicaSet change must reload the Deployments list — it feeds the IMAGE column")
	}
}

func TestUnrelatedKindDoesNotReloadTheList(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment

	before := m.reloadEpoch
	_, cmd := m.Update(kube.ResourceChangedMsg{Kind: kube.KindCronJob})
	if cmd != nil || m.reloadEpoch != before {
		t.Fatal("a CronJob change has nothing to do with the Deployments list")
	}
}

func TestAuxKindOfIsDirectional(t *testing.T) {
	t.Parallel()
	if !auxKindOf(kube.KindPod, kube.KindNode) {
		t.Error("the Pods health strip counts Nodes")
	}
	if !auxKindOf(kube.KindNode, kube.KindPod) {
		t.Error("the Nodes list counts Pods per node")
	}
	if auxKindOf(kube.KindSecret, kube.KindNode) {
		t.Error("Secrets don't read Nodes")
	}
}

// TestPrefetchWarmsAuxCaches: with informers starting on first read, the
// synchronous prompt handlers (rollout history, the scale prompt) would
// otherwise hit an empty cache on first keypress and render a plausible
// wrong answer instead of the truth.
func TestPrefetchWarmsAuxCaches(t *testing.T) {
	lister := &recordingLister{inner: fakeLister{objs: map[kube.ResourceKind][]runtime.Object{}}}
	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	m.kind = kube.KindDeployment
	m.desc, _ = m.session.Registry.Descriptor(kube.KindDeployment)

	cmd := m.resetAndLoad()
	if cmd == nil {
		t.Fatal("expected resetAndLoad to return commands")
	}
	drain(cmd)

	for _, kind := range []kube.ResourceKind{kube.KindReplicaSet, kube.KindHorizontalPodAutoscaler} {
		if !lister.read(kind) {
			t.Errorf("opening the Deployments list did not warm the %s cache", kind)
		}
	}
}

// drain runs a command and any batched children, so prefetch side effects
// land before assertions.
func drain(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var wg sync.WaitGroup
		for _, c := range batch {
			if c == nil {
				continue
			}
			wg.Add(1)
			go func(c tea.Cmd) { defer wg.Done(); c() }(c)
		}
		wg.Wait()
	}
}
