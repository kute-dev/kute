package kube

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// fuzzReleaseNamespace is the Secret's own namespace for every fuzz case, so
// the decoded-Namespace-is-never-empty assertion below is about the
// decoder's fallback rather than about the fixture.
const fuzzReleaseNamespace = "production"

// FuzzDecodeHelmReleases feeds arbitrary bytes through the one place kute
// parses data it did not produce.
//
// Every other read in this package is a typed API object the apiserver
// validated. A `helm.sh/release.v1` Secret is different: its `release` key
// is an opaque blob that kute base64-decodes, gunzips and JSON-parses
// itself, and it is read the moment someone browses a namespace — nothing
// asks first whether helm wrote it. A Secret of that type carrying anything
// else (a truncated release, an unrelated payload someone parked under the
// helm type, a half-written revision) must degrade to "not a release I can
// read" rather than take the TUI down with it.
//
// The property is the tolerance DecodeHelmReleases already documents: skip
// what doesn't decode, panic on nothing, and never hand the list screen a
// row it can't address.
func FuzzDecodeHelmReleases(f *testing.F) {
	// A real release, so the fuzzer starts from something that reaches the
	// JSON layer rather than bouncing off base64 every time.
	valid := EncodeHelmReleaseSecret(HelmRelease{
		Name: "postgresql", Namespace: fuzzReleaseNamespace,
		Chart: "postgresql", ChartVersion: "12.1.9", Revision: 3, Status: "deployed",
	})
	f.Add(valid.Data["release"])

	// One seed per layer the decoder peels, so each failure mode is on the
	// corpus from the start.
	f.Add([]byte(nil))                              // no payload at all
	f.Add([]byte("not base64 !!"))                  // fails base64
	f.Add([]byte(base64.StdEncoding.EncodeToString( // valid base64, not gzip
		[]byte("plain text, never gzipped"))))
	f.Add([]byte(base64.StdEncoding.EncodeToString(gzipped([]byte("{not json")))))
	f.Add([]byte(base64.StdEncoding.EncodeToString(gzipped([]byte(`{"version":-1}`)))))
	f.Add([]byte(base64.StdEncoding.EncodeToString(gzipped([]byte(
		`{"name":"x","namespace":"","chart":{"metadata":null},"config":{"a":[1,{"b":null}]}}`)))))
	// A gzip header with a truncated body — a half-written revision Secret.
	f.Add([]byte(base64.StdEncoding.EncodeToString(gzipped([]byte(`{"name":"x"}`))[:10]))) //nolint:gosec // seed only

	f.Fuzz(func(t *testing.T, payload []byte) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "sh.helm.release.v1.x.v1", Namespace: fuzzReleaseNamespace},
			Type:       HelmReleaseSecretType,
			Data:       map[string][]byte{"release": payload},
		}

		got := DecodeHelmReleases([]runtime.Object{secret})
		if len(got) > 1 {
			t.Fatalf("one Secret decoded to %d releases", len(got))
		}
		if len(got) == 0 {
			return // undecodable, skipped — the documented behaviour
		}

		// A release that did decode has to be addressable: the 18a list
		// renders a row per release and its verbs (history, rollback) key
		// off namespace/name, so an empty namespace would produce a row
		// pointing at nothing. DecodeHelmReleaseSecret falls back to the
		// Secret's own namespace for exactly this reason.
		if got[0].Namespace == "" {
			t.Errorf("decoded release has no namespace (payload %q)", payload)
		}

		// Whatever came out has to survive the same trip again — the
		// history screen re-decodes on every Secret change event and every
		// cache-sync retry, so a release that decodes differently the
		// second time would make a row flicker between two states.
		again, err := DecodeHelmReleaseSecret(EncodeHelmReleaseSecret(got[0]))
		if err != nil {
			t.Fatalf("re-encoding a decoded release made it undecodable: %v", err)
		}
		if again.Name != got[0].Name || again.Namespace != got[0].Namespace ||
			again.Revision != got[0].Revision || again.Status != got[0].Status {
			t.Errorf("decode is not stable:\nfirst  %+v\nsecond %+v", got[0], again)
		}
	})
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}
