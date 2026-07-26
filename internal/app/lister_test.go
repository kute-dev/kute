package app

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui/tasks/helmhistory"
)

// recordingCluster stands in for *kube.Cluster at exactly the seams the
// decorator stack sees, recording which cache each read landed in. This is
// the app-layer counterpart of internal/kube's recorded-clientset-actions
// tests (lazy_test.go, helm_secrets_test.go): the question is never "did the
// right rows come back" — both paths decode to the same releases — but "what
// did the app pull down the wire to produce them".
type recordingCluster struct {
	listedKinds []kube.ResourceKind
	helmReads   int
	syncedAsked []kube.ResourceKind
}

func (c *recordingCluster) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	c.listedKinds = append(c.listedKinds, kind)
	return nil, nil
}

func (c *recordingCluster) ListHelmReleaseSecrets(context.Context, string) ([]runtime.Object, error) {
	c.helmReads++
	return nil, nil
}

func (c *recordingCluster) KindSynced(kind kube.ResourceKind) bool {
	c.syncedAsked = append(c.syncedAsked, kind)
	return true
}

func (c *recordingCluster) listed(kind kube.ResourceKind) bool {
	for _, k := range c.listedKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// narrowlessCluster has a per-kind sync signal but no filtered release cache
// — the "optional seam absent" shape helmSecretLister documents.
type narrowlessCluster struct {
	listedKinds []kube.ResourceKind
	syncedAsked []kube.ResourceKind
}

func (c *narrowlessCluster) ListRaw(_ context.Context, kind kube.ResourceKind, _ string) ([]runtime.Object, error) {
	c.listedKinds = append(c.listedKinds, kind)
	return nil, nil
}

func (c *narrowlessCluster) KindSynced(kind kube.ResourceKind) bool {
	c.syncedAsked = append(c.syncedAsked, kind)
	return true
}

// TestListingHelmReleasesReadsTheReleaseCacheNotEverySecret is the
// regression test for the seam that went unforwarded. forwardAwareLister
// embeds the resources.RawLister *interface*, so it promoted only ListRaw;
// helmAwareLister.helmSecrets type-asserted against it, missed, and fell
// through to the shared Secret cache — pulling the largest kind on most
// clusters to find a handful of releases, and leaving the narrow release
// cache cold until the 'h' key started it mid-session.
func TestListingHelmReleasesReadsTheReleaseCacheNotEverySecret(t *testing.T) {
	rec := &recordingCluster{}
	lister := newSessionLister(rec, kube.NewForwardManager())

	if _, err := lister.ListRaw(context.Background(), kube.KindHelmRelease, "default"); err != nil {
		t.Fatalf("ListRaw(KindHelmRelease): %v", err)
	}

	if rec.helmReads != 1 {
		t.Errorf("release cache read %d times, want 1", rec.helmReads)
	}
	if rec.listed(kube.KindSecret) {
		t.Errorf("listing Helm releases read every Secret in the namespace (%v); "+
			"that pulls the largest kind on most clusters through the shared informer, "+
			"which is exactly what the filtered release cache exists to avoid", rec.listedKinds)
	}
}

// TestHelmReleaseSyncStateAsksAboutTheReleaseCache: the loading gate has to
// name the cache the rows actually came from. Asking about KindSecret while
// reading the release cache is how a screen hears "settled" from an informer
// it isn't using and claims "no releases" about one that is still filling.
func TestHelmReleaseSyncStateAsksAboutTheReleaseCache(t *testing.T) {
	rec := &recordingCluster{}
	lister := newSessionLister(rec, kube.NewForwardManager())

	lister.KindSynced(kube.KindHelmRelease)

	if len(rec.syncedAsked) != 1 || rec.syncedAsked[0] != kube.KindHelmRelease {
		t.Fatalf("KindSynced asked about %v, want [%s]", rec.syncedAsked, kube.KindHelmRelease)
	}
}

// TestDecoratedListerFallsBackWithoutTheReleaseCache pins the other half of
// the contract: once a decorator advertises ListHelmReleaseSecrets every
// caller's type assertion succeeds, so the method must never be a dead end.
// With no narrow cache underneath, the shared Secret cache is the documented
// answer — and KindSynced has to name *that* cache to match.
func TestDecoratedListerFallsBackWithoutTheReleaseCache(t *testing.T) {
	plain := &narrowlessCluster{}
	lister := newSessionLister(plain, kube.NewForwardManager())

	if _, err := lister.ListHelmReleaseSecrets(context.Background(), "default"); err != nil {
		t.Fatalf("ListHelmReleaseSecrets should degrade to the shared cache, got: %v", err)
	}
	if len(plain.listedKinds) != 1 || plain.listedKinds[0] != kube.KindSecret {
		t.Fatalf("fallback listed %v, want [%s]", plain.listedKinds, kube.KindSecret)
	}

	lister.KindSynced(kube.KindHelmRelease)
	if len(plain.syncedAsked) != 1 || plain.syncedAsked[0] != kube.KindSecret {
		t.Fatalf("KindSynced asked about %v, want [%s] to match the fallback's source",
			plain.syncedAsked, kube.KindSecret)
	}
}

var (
	_ resources.RawLister          = (*recordingCluster)(nil)
	_ helmhistory.HelmSecretLister = (*recordingCluster)(nil)
	_ resources.RawLister          = (*narrowlessCluster)(nil)
)
