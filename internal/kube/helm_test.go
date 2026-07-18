package kube

import (
	"testing"
	"time"
)

func TestEncodeDecodeHelmReleaseSecretRoundTrip(t *testing.T) {
	t.Parallel()
	want := HelmRelease{
		Name: "postgresql", Namespace: "production",
		Chart: "postgresql", ChartVersion: "12.1.9", AppVersion: "15.4.0",
		Revision: 3, Status: "deployed",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Values:  "auth:\n  enablePostgresUser: true\n",
	}
	secret := EncodeHelmReleaseSecret(want)
	if secret.Type != HelmReleaseSecretType {
		t.Fatalf("secret.Type = %q, want %q", secret.Type, HelmReleaseSecretType)
	}

	got, err := DecodeHelmReleaseSecret(secret)
	if err != nil {
		t.Fatalf("DecodeHelmReleaseSecret: %v", err)
	}
	if got.Name != want.Name || got.Namespace != want.Namespace || got.Chart != want.Chart ||
		got.ChartVersion != want.ChartVersion || got.AppVersion != want.AppVersion ||
		got.Revision != want.Revision || got.Status != want.Status {
		t.Fatalf("round trip mismatch: got %+v, want %+v", got, want)
	}
	if !got.Updated.Equal(want.Updated) {
		t.Fatalf("Updated = %v, want %v", got.Updated, want.Updated)
	}
	if got.Values == "" {
		t.Fatalf("Values not preserved across round trip")
	}
}

func TestDecodeHelmReleaseSecretRejectsWrongType(t *testing.T) {
	t.Parallel()
	secret := EncodeHelmReleaseSecret(HelmRelease{Name: "x", Namespace: "ns", Revision: 1})
	secret.Type = "Opaque"
	if _, err := DecodeHelmReleaseSecret(secret); err == nil {
		t.Fatal("expected an error decoding a non-helm-release secret")
	}
}

func TestStatusCellCarriesFailureReason(t *testing.T) {
	t.Parallel()
	r := HelmRelease{Status: "failed", StatusReason: "hook timeout"}
	if got, want := r.StatusCell(), "failed · hook timeout"; got != want {
		t.Fatalf("StatusCell() = %q, want %q", got, want)
	}
	deployed := HelmRelease{Status: "deployed"}
	if got, want := deployed.StatusCell(), "deployed"; got != want {
		t.Fatalf("StatusCell() = %q, want %q", got, want)
	}
}

func TestLatestHelmReleasesPicksHighestRevision(t *testing.T) {
	t.Parallel()
	all := []HelmRelease{
		{Namespace: "production", Name: "postgresql", Revision: 1, Status: "superseded"},
		{Namespace: "production", Name: "postgresql", Revision: 3, Status: "deployed"},
		{Namespace: "production", Name: "postgresql", Revision: 2, Status: "superseded"},
		{Namespace: "production", Name: "redis", Revision: 1, Status: "deployed"},
	}
	latest := LatestHelmReleases(all)
	if len(latest) != 2 {
		t.Fatalf("LatestHelmReleases returned %d releases, want 2", len(latest))
	}
	byName := map[string]HelmRelease{}
	for _, r := range latest {
		byName[r.Name] = r
	}
	if byName["postgresql"].Revision != 3 {
		t.Fatalf("postgresql latest revision = %d, want 3", byName["postgresql"].Revision)
	}
	if byName["redis"].Revision != 1 {
		t.Fatalf("redis latest revision = %d, want 1", byName["redis"].Revision)
	}
}

func TestHelmReleaseHistorySortsNewestFirst(t *testing.T) {
	t.Parallel()
	all := []HelmRelease{
		{Namespace: "production", Name: "postgresql", Revision: 1},
		{Namespace: "production", Name: "postgresql", Revision: 3},
		{Namespace: "production", Name: "postgresql", Revision: 2},
		{Namespace: "production", Name: "other", Revision: 9},
	}
	history := HelmReleaseHistory(all, "production", "postgresql")
	if len(history) != 3 {
		t.Fatalf("HelmReleaseHistory returned %d entries, want 3", len(history))
	}
	for i, want := range []int{3, 2, 1} {
		if history[i].Revision != want {
			t.Fatalf("history[%d].Revision = %d, want %d", i, history[i].Revision, want)
		}
	}
}

func TestHelmRollbackCommandString(t *testing.T) {
	t.Parallel()
	if got, want := HelmRollbackCommandString("production", "postgresql", 0), "helm rollback postgresql -n production"; got != want {
		t.Fatalf("HelmRollbackCommandString(0) = %q, want %q", got, want)
	}
	if got, want := HelmRollbackCommandString("production", "postgresql", 2), "helm rollback postgresql 2 -n production"; got != want {
		t.Fatalf("HelmRollbackCommandString(2) = %q, want %q", got, want)
	}
}
