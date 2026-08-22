package kube

import (
	"slices"
	"strings"
	"testing"
)

// FuzzHelmReleaseWorkloads extends the existing FuzzDecodeHelmReleases past
// the Secret decode and into the rendered manifest, which is the other half of
// the same untrusted payload: whoever installs a chart chooses what its
// templates render, and kute reads that on every Helm list load.
//
// The properties:
//   - nothing panics, on any manifest;
//   - splitting is lossless — every document's lines come back, so a `---`
//     inside a block scalar cannot silently swallow half a document;
//   - every workload reported carries a name, and a namespace (falling back to
//     the release's own), since a ref missing either can never match a real
//     object and would show as a phantom rollout;
//   - the iterator and the slice form agree, so an early-exiting caller sees
//     the same prefix a collecting one does.
func FuzzHelmReleaseWorkloads(f *testing.F) {
	seeds := []string{
		"",
		"---",
		"kind: Deployment\nmetadata:\n  name: web\n",
		"kind: Deployment\nmetadata:\n  name: web\n  namespace: other\n",
		"kind: ConfigMap\ndata:\n  x: |\n    ---\n    nested\n---\nkind: StatefulSet\nmetadata:\n  name: db\n",
		"kind: DaemonSet\nmetadata:\n  name: agent\n---\nkind: Service\nmetadata:\n  name: svc\n",
		"kind: Deployment\nmetadata:\n  name:\n", // workload kind, no name
		"kind: \"Deployment\"\nmetadata:\n  name: quoted\n",
		"  kind: Deployment\n", // indented: not the document's own kind
		"kind: Deployment\n\tmetadata: {\n",
		strings.Repeat("---\n", 200),
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, manifest string) {
		docs := slices.Collect(splitYAMLDocuments(manifest))

		// Lossless: concatenating the documents back recovers every non-empty
		// line of the input, in order.
		var got []string
		for _, d := range docs {
			for line := range strings.SplitSeq(d, "\n") {
				if line != "" {
					got = append(got, line)
				}
			}
		}
		var want []string
		for line := range strings.SplitSeq(manifest, "\n") {
			if line != "" && strings.TrimRight(line, " \t\r") != "---" {
				want = append(want, line)
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("splitYAMLDocuments lost or reordered lines:\n got %q\nwant %q", got, want)
		}

		r := HelmRelease{Name: "rel", Namespace: "rel-ns", Manifest: manifest}
		refs := HelmReleaseWorkloads(r)
		for _, ref := range refs {
			if ref.Name == "" {
				t.Fatalf("workload ref with no name: %+v", ref)
			}
			if ref.Namespace == "" {
				t.Fatalf("workload ref with no namespace (should fall back to the release's): %+v", ref)
			}
		}

		// The iterator and the slice must agree — rolloutPending ranges the
		// former and stops early, so a divergence would be invisible in the
		// tests that only use the latter.
		if seq := slices.Collect(HelmReleaseWorkloadsSeq(r)); !slices.Equal(seq, refs) {
			t.Fatalf("iterator and slice disagree:\n seq %+v\nslice %+v", seq, refs)
		}
	})
}
