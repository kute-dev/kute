package kube

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestEncodeDecodeHelmReleaseSecretRoundTrip(t *testing.T) {
	t.Parallel()
	want := HelmRelease{
		Name: "postgresql", Namespace: "production",
		Chart: "postgresql", ChartVersion: "12.1.9", AppVersion: "15.4.0",
		Revision: 3, Status: "deployed",
		Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Values:  "auth:\n  enablePostgresUser: true\n",
		Hooks: []HelmHook{{
			Name: "postgresql-migrate", Kind: "Job", Events: []string{"pre-install"},
			DeletePolicies: []string{"hook-succeeded"}, Weight: -5,
			LastRun: HelmHookRun{StartedAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC), Phase: "Running"},
		}},
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
	if len(got.Hooks) != 1 || got.Hooks[0].Name != "postgresql-migrate" || got.Hooks[0].LastRun.Phase != "Running" || got.Hooks[0].Weight != -5 {
		t.Fatalf("Hooks not preserved across round trip: %+v", got.Hooks)
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
	pending := HelmRelease{Status: "pending-install", StatusReason: "Initial install underway"}
	if got, want := pending.StatusCell(), "pending-install · Initial install underway"; got != want {
		t.Fatalf("StatusCell() = %q, want %q", got, want)
	}
}

func TestHelmReleaseObjectsIncludesObjectsThatWereNeverCreated(t *testing.T) {
	r := HelmRelease{Namespace: "timesheet", Manifest: `apiVersion: v1
kind: Service
metadata:
  name: api
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  namespace: elsewhere
`}
	refs := HelmReleaseObjects(r)
	if len(refs) != 2 || refs[0].Namespace != "timesheet" || refs[1].Namespace != "elsewhere" || refs[1].Kind != "Deployment" {
		t.Fatalf("HelmReleaseObjects() = %+v", refs)
	}
}

func TestHelmReleaseObjectDeepCopiesHookSlices(t *testing.T) {
	original := NewHelmReleaseObject(HelmRelease{Hooks: []HelmHook{{Events: []string{"pre-install"}, DeletePolicies: []string{"hook-succeeded"}}}})
	copy := original.DeepCopyObject().(*HelmReleaseObject)
	copy.Release.Hooks[0].Events[0] = "post-install"
	copy.Release.Hooks[0].DeletePolicies[0] = "hook-failed"
	if original.Release.Hooks[0].Events[0] != "pre-install" || original.Release.Hooks[0].DeletePolicies[0] != "hook-succeeded" {
		t.Fatalf("DeepCopyObject aliased hook slices: %+v", original.Release.Hooks)
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

// TestHelmReleaseSecretsForNarrowsBeforeDecode covers the pre-decode filter
// 18a's history screen runs. The release cache is cluster-wide and the
// screen re-reads it on every Secret change, so the one release it displays
// has to be picked out before the expensive part.
func TestHelmReleaseSecretsForNarrowsBeforeDecode(t *testing.T) {
	t.Parallel()
	labelled := func(namespace, name string, revision int) *corev1.Secret {
		return EncodeHelmReleaseSecret(HelmRelease{Namespace: namespace, Name: name, Revision: revision})
	}
	// A revision Secret written without Helm's own labels — matched on the
	// object-name convention instead.
	unlabelled := labelled("production", "postgresql", 4)
	unlabelled.Labels = nil

	objs := []runtime.Object{
		labelled("production", "postgresql", 1),
		labelled("production", "postgresql", 2),
		unlabelled,
		labelled("production", "redis", 7),
		labelled("staging", "postgresql", 9),
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "tls", Namespace: "production"}},
	}

	got := HelmReleaseSecretsFor(objs, "production", "postgresql")
	if len(got) != 3 {
		t.Fatalf("HelmReleaseSecretsFor returned %d secrets, want 3 (revisions 1, 2 and the unlabelled 4)", len(got))
	}
	for _, obj := range got {
		secret := obj.(*corev1.Secret)
		if secret.Namespace != "production" || !strings.HasPrefix(secret.Name, "sh.helm.release.v1.postgresql.v") {
			t.Errorf("unexpected secret %s/%s", secret.Namespace, secret.Name)
		}
	}

	// The filter must not change what the screen ends up showing.
	history := HelmReleaseHistory(DecodeHelmReleases(got), "production", "postgresql")
	if len(history) != 3 || history[0].Revision != 4 {
		t.Fatalf("history after filtering = %+v, want 3 revisions newest-first", history)
	}
}

// zipBombSecret is a release Secret whose payload is small on the wire and
// huge once gunzipped: `size` bytes of a repeated character, which gzip
// stores in a few hundred.
//
// The payload is a *valid* release document, not noise. A bomb that also
// happens to be malformed JSON would be rejected by the unmarshal step even
// with no cap in place, so the test asserting the cap would pass whether or
// not the cap exists — the exact thing this file's other tests exist to
// avoid.
func zipBombSecret(t *testing.T, size int) *corev1.Secret {
	t.Helper()
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	write := func(b []byte) {
		if _, err := gz.Write(b); err != nil {
			t.Fatalf("building the bomb: %v", err)
		}
	}
	write([]byte(`{"name":"bomb","namespace":"production","version":1,"manifest":"`))
	chunk := bytes.Repeat([]byte("A"), 64<<10)
	for written := 0; written < size; written += len(chunk) {
		write(chunk[:min(len(chunk), size-written)])
	}
	write([]byte(`"}`))
	if err := gz.Close(); err != nil {
		t.Fatalf("closing the bomb: %v", err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.bomb.v1", Namespace: "production"},
		Type:       HelmReleaseSecretType,
		Data:       map[string][]byte{"release": []byte(base64.StdEncoding.EncodeToString(gzBuf.Bytes()))},
	}
}

// TestDecodeHelmReleaseSecretBoundsTheDecompressedPayload covers the one
// place kute's memory use is decided by data it didn't produce.
//
// The apiserver caps a Secret at 1 MiB of *compressed* data, and gzip runs
// to ~1000:1 on repetitive input, so a Secret comfortably inside that limit
// can gunzip to hundreds of megabytes. Nothing checks that helm wrote a
// `helm.sh/release.v1` Secret before kute decodes it — browsing the
// namespace is enough — so an unbounded read here is an out-of-memory kill
// triggered by an object anyone with write access to a namespace can create.
func TestDecodeHelmReleaseSecretBoundsTheDecompressedPayload(t *testing.T) {
	t.Parallel()
	// 4 MiB of payload against a 1 MiB cap: the ratio that matters, at a
	// size the test can afford. The real cap is maxHelmReleasePayload.
	secret := zipBombSecret(t, 4<<20)
	if wire := len(secret.Data["release"]); wire > 64<<10 {
		t.Fatalf("the bomb is %d bytes on the wire; it is supposed to be small", wire)
	}

	if _, err := decodeHelmReleaseSecret(secret, 1<<20); err == nil {
		t.Fatal("a 4 MiB payload decoded against a 1 MiB cap; the read is unbounded")
	} else if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should name the limit, got: %v", err)
	}

	// The list read has to survive one, not just refuse it: 18a renders
	// every release in the namespace, and one oversized Secret must not cost
	// the user the rest of the screen.
	good := EncodeHelmReleaseSecret(HelmRelease{Name: "postgresql", Namespace: "production", Revision: 1})
	got := decodeHelmReleases([]runtime.Object{secret, good}, 1<<20)
	if len(got) != 1 || got[0].Name != "postgresql" {
		t.Fatalf("decodeHelmReleases returned %+v, want just the good release", got)
	}
}

// TestDecodeHelmReleaseSecretAcceptsAPayloadUpToTheLimit is the other half:
// the cap must not be a truncation. A payload that ends exactly at the limit
// is legitimate and has to decode, not come back as malformed JSON.
func TestDecodeHelmReleaseSecretAcceptsAPayloadUpToTheLimit(t *testing.T) {
	t.Parallel()
	secret := EncodeHelmReleaseSecret(HelmRelease{
		Name: "postgresql", Namespace: "production", Revision: 2,
		Manifest: strings.Repeat("m", 32<<10),
	})
	raw, err := base64.StdEncoding.DecodeString(string(secret.Data["release"]))
	if err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("gunzipping the fixture: %v", err)
	}
	payload, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	got, err := decodeHelmReleaseSecret(secret, int64(len(payload)))
	if err != nil {
		t.Fatalf("a payload exactly at the cap failed to decode: %v", err)
	}
	if got.Name != "postgresql" || got.Revision != 2 {
		t.Fatalf("decoded %+v, want the fixture back", got)
	}
}
