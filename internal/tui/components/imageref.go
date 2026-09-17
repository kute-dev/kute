package components

import "strings"

// ShortImageRef drops a redundant digest suffix (`@sha256:<64 hex chars>`,
// 71 characters no one reads at cell width) whenever the reference also
// carries an explicit tag — the tag is the version signal a reader actually
// wants, and keeping both just to truncate one of them away is worse than
// dropping the redundant one outright. A digest-only reference is untouched:
// then the digest is the version signal.
func ShortImageRef(img string) string {
	if repo, _, ok := strings.Cut(img, "@sha256:"); ok {
		if strings.Contains(repo[strings.LastIndex(repo, "/")+1:], ":") {
			return repo
		}
	}
	return img
}

// TruncateImageRef fits an image reference into width cells, ellipsized from
// the front rather than the back: a long registry/repo path is the part worth
// eliding, since the tag (or, lacking one, the digest) at the end is what
// answers "which version is this" (docs/design README.md §10a).
func TruncateImageRef(img string, width int) string {
	return TruncateFront(ShortImageRef(img), width)
}
