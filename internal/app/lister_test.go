package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/helmrepo"
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
	errorAsked  []kube.ResourceKind
}

// errStalledCache stands in for the reason a cache stopped filling — on the
// cluster this was diagnosed against, a release LIST that could not complete
// inside the API server's request window.
var errStalledCache = errors.New("stream error when reading response body")

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

func (c *recordingCluster) KindError(kind kube.ResourceKind) error {
	c.errorAsked = append(c.errorAsked, kind)
	return errStalledCache
}

func (c *recordingCluster) listed(kind kube.ResourceKind) bool {
	for _, k := range c.listedKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// releaseCluster hands back one real release Secret, so the decorator's own
// decode-and-annotate path runs end to end.
type releaseCluster struct {
	release kube.HelmRelease
}

func (c *releaseCluster) ListRaw(context.Context, kube.ResourceKind, string) ([]runtime.Object, error) {
	return nil, nil
}

func (c *releaseCluster) ListHelmReleaseSecrets(context.Context, string) ([]runtime.Object, error) {
	return []runtime.Object{kube.EncodeHelmReleaseSecret(c.release)}, nil
}

// chartCache writes a one-repo Helm cache offering chart at version, and
// returns a Cache over it.
func chartCache(t *testing.T, chart, version string) *helmrepo.Cache {
	t.Helper()
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "repositories.yaml")
	if err := os.WriteFile(configPath, []byte("repositories:\n- name: testrepo\n  url: https://example.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := "apiVersion: v1\nentries:\n  " + chart + ":\n  - name: " + chart + "\n    version: " + version + "\n"
	if err := os.WriteFile(filepath.Join(cacheDir, "testrepo-index.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return helmrepo.NewCache(helmrepo.Loader{ConfigPath: configPath, CachePath: cacheDir})
}

// TestListedReleasesCarryTheAvailableChartVersion pins 18a's outdated signal
// to the one place releases are decoded. Annotating here rather than in each
// screen is what makes the LATEST column, the strip's outdated count and the
// 19a overview agree without three copies of the lookup.
func TestListedReleasesCarryTheAvailableChartVersion(t *testing.T) {
	cluster := &releaseCluster{release: kube.HelmRelease{
		Namespace: "default", Name: "certs", Chart: "cert-manager", ChartVersion: "1.14.4",
		Revision: 3, Status: "deployed",
	}}
	lister := newSessionLister(cluster, kube.NewForwardManager(), chartCache(t, "cert-manager", "1.16.2"))

	objs, err := lister.ListRaw(context.Background(), kube.KindHelmRelease, "default")
	if err != nil {
		t.Fatalf("ListRaw(KindHelmRelease): %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("got %d releases, want 1", len(objs))
	}
	release := objs[0].(*kube.HelmReleaseObject).Release
	if release.LatestVersion != "1.16.2" {
		t.Errorf("LatestVersion = %q, want 1.16.2", release.LatestVersion)
	}
	if release.LatestRepo != "testrepo" {
		t.Errorf("LatestRepo = %q, want testrepo", release.LatestRepo)
	}
	if !release.Outdated() {
		t.Error("Outdated() = false for 1.14.4 against an available 1.16.2")
	}
	if status := lister.ChartIndexStatus(); !status.Configured || status.Repos != 1 {
		t.Errorf("ChartIndexStatus() = %+v, want a configured single-repo cache", status)
	}
}

// TestListedReleasesWithoutAChartIndex covers the common case of no Helm repo
// cache at all: the release still lists, and simply carries no opinion about
// what's available. An unknown must never masquerade as "current".
func TestListedReleasesWithoutAChartIndex(t *testing.T) {
	cluster := &releaseCluster{release: kube.HelmRelease{
		Namespace: "default", Name: "certs", Chart: "cert-manager", ChartVersion: "1.14.4",
		Revision: 3, Status: "deployed",
	}}
	lister := newSessionLister(cluster, kube.NewForwardManager(), nil)

	objs, err := lister.ListRaw(context.Background(), kube.KindHelmRelease, "default")
	if err != nil {
		t.Fatalf("ListRaw(KindHelmRelease): %v", err)
	}
	release := objs[0].(*kube.HelmReleaseObject).Release
	if release.LatestVersion != "" || release.Outdated() {
		t.Errorf("release carries %+v with no chart index, want no opinion", release)
	}
	if lister.ChartIndexStatus().Configured {
		t.Error("ChartIndexStatus().Configured = true with no chart index")
	}
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
	lister := newSessionLister(rec, kube.NewForwardManager(), nil)

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
	lister := newSessionLister(rec, kube.NewForwardManager(), nil)

	lister.KindSynced(kube.KindHelmRelease)

	if len(rec.syncedAsked) != 1 || rec.syncedAsked[0] != kube.KindHelmRelease {
		t.Fatalf("KindSynced asked about %v, want [%s]", rec.syncedAsked, kube.KindHelmRelease)
	}
}

// TestHelmReleaseErrorAsksAboutTheReleaseCache: KindError travels with
// KindSynced and has to name the same cache. A screen told "settled" by one
// informer and "no problem" by another renders an empty cluster over a read
// that failed — the spinner-that-never-ends traded for a lie.
func TestHelmReleaseErrorAsksAboutTheReleaseCache(t *testing.T) {
	rec := &recordingCluster{}
	lister := newSessionLister(rec, kube.NewForwardManager(), nil)

	if err := lister.KindError(kube.KindHelmRelease); err == nil {
		t.Fatal("KindError returned nil; the seam is not forwarded through the decorator stack")
	}
	if len(rec.errorAsked) != 1 || rec.errorAsked[0] != kube.KindHelmRelease {
		t.Fatalf("KindError asked about %v, want [%s]", rec.errorAsked, kube.KindHelmRelease)
	}
}

// TestForwardsNeverStall: forwards are in-process state with no cache to
// fail, and the decorator answers for them itself rather than passing the
// question down to a kind the cluster has never heard of.
func TestForwardsNeverStall(t *testing.T) {
	rec := &recordingCluster{}
	lister := newSessionLister(rec, kube.NewForwardManager(), nil)

	if err := lister.KindError(kube.KindForward); err != nil {
		t.Fatalf("KindError(Forward) = %v, want nil", err)
	}
	if len(rec.errorAsked) != 0 {
		t.Fatalf("the forward question reached the cluster as %v", rec.errorAsked)
	}
}

// TestDecoratedListerFallsBackWithoutTheReleaseCache pins the other half of
// the contract: once a decorator advertises ListHelmReleaseSecrets every
// caller's type assertion succeeds, so the method must never be a dead end.
// With no narrow cache underneath, the shared Secret cache is the documented
// answer — and KindSynced has to name *that* cache to match.
func TestDecoratedListerFallsBackWithoutTheReleaseCache(t *testing.T) {
	plain := &narrowlessCluster{}
	lister := newSessionLister(plain, kube.NewForwardManager(), nil)

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
