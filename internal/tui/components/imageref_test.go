package components

import (
	"strings"
	"testing"
)

// TestTruncateImageRefDropsRedundantDigest pins the shared image-ref trim
// rule (moved here from execpicker when the helper was promoted): a digest
// is dropped when a tag already names the version, and whatever's left
// ellipsizes from the front so the tag stays visible.
func TestTruncateImageRefDropsRedundantDigest(t *testing.T) {
	t.Parallel()
	const img = "quay.io/prometheus-operator/prometheus-config-reloader:v0.91.0@sha256:7d9e4eea5f1139e602508871f422b011"
	got := TruncateImageRef(img, 40)
	if strings.Contains(got, "sha256") {
		t.Errorf("TruncateImageRef(%q) = %q, want the redundant digest dropped", img, got)
	}
	if !strings.HasSuffix(got, "v0.91.0") {
		t.Errorf("TruncateImageRef(%q) = %q, want the tag to survive at the end", img, got)
	}
	if !strings.HasPrefix(got, "…") {
		t.Errorf("TruncateImageRef(%q) = %q, want it ellipsized from the front", got, got)
	}

	// No tag: the digest is the only version signal, so it's left alone
	// (still ellipsized from the front if it doesn't fit, same as any other
	// too-long reference — the point is it's never dropped outright).
	const digestOnly = "gcr.io/distroless/static@sha256:7d9e4eea5f1139e602508871f422b011"
	if got := TruncateImageRef(digestOnly, 100); got != digestOnly {
		t.Errorf("TruncateImageRef(%q, 100) = %q, want the untrimmed digest kept when there's no tag", digestOnly, got)
	}
}

func TestTruncateFront(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		in    string
		width int
		want  string
	}{
		{"fits untouched", "abc", 5, "abc"},
		{"elides from the front", "abcdefgh", 5, "…efgh"},
		{"zero width", "abc", 0, ""},
		{"narrow width drops the ellipsis", "abcdefgh", 3, "fgh"},
		{"exact fit", "abcde", 5, "abcde"},
		{"multi-line goes line-by-line", "abcdefgh\nij", 5, "…efgh\nij"},
		{"wide runes measured in cells", "日本語テスト", 5, "…スト"},
	}
	for _, tc := range cases {
		if got := TruncateFront(tc.in, tc.width); got != tc.want {
			t.Errorf("%s: TruncateFront(%q, %d) = %q, want %q", tc.name, tc.in, tc.width, got, tc.want)
		}
	}
}
