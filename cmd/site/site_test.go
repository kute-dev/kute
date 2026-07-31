package main

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// repoRoot is two levels up from cmd/site.
const repoRoot = "../.."

func readFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

var (
	varRe   = regexp.MustCompile(`(--[\w-]+):\s*(#[0-9a-fA-F]{6})`)
	lightRe = regexp.MustCompile(`(?s):root\[data-theme="light"\] \{(.*?)\n\}`)
	mediaRe = regexp.MustCompile(`(?s)@media \(prefers-color-scheme: light\) \{\s*:root:not\(\[data-theme="dark"\]\) \{(.*?)\n  \}`)
	darkRe  = regexp.MustCompile(`(?s):root \{(.*?)\n\}`)
)

func paletteFrom(block string) map[string]string {
	out := map[string]string{}
	for _, m := range varRe.FindAllStringSubmatch(block, -1) {
		out[m[1]] = strings.ToLower(m[2])
	}
	return out
}

// TestLightPaletteBlocksMatch pins the one piece of duplication left in the
// stylesheet. The light palette is written twice — once for an explicit
// data-theme="light" and once for prefers-color-scheme — because plain CSS
// cannot share a declaration block between a selector and an at-rule, and the
// alternatives all cost something worse: light-dark() would drop light theming
// entirely on browsers that lack it. Duplication is tolerable; drifting apart
// silently is not, which is what this test prevents.
func TestLightPaletteBlocksMatch(t *testing.T) {
	css := readFile(t, "website/assets/styles.css")

	lm := lightRe.FindStringSubmatch(css)
	mm := mediaRe.FindStringSubmatch(css)
	if lm == nil || mm == nil {
		t.Fatal("could not locate both light palette blocks; did their selectors change?")
	}
	attr, media := paletteFrom(lm[1]), paletteFrom(mm[1])
	if len(attr) == 0 {
		t.Fatal("light palette block parsed as empty")
	}

	for name, want := range attr {
		if got, ok := media[name]; !ok {
			t.Errorf("%s is set for [data-theme=light] but missing from the prefers-color-scheme block", name)
		} else if got != want {
			t.Errorf("%s differs: [data-theme=light]=%s prefers-color-scheme=%s", name, want, got)
		}
	}
	for name := range media {
		if _, ok := attr[name]; !ok {
			t.Errorf("%s is set in the prefers-color-scheme block but missing from [data-theme=light]", name)
		}
	}
}

func channel(c float64) float64 {
	c /= 255
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func luminance(hex string) float64 {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil {
		return 0
	}
	r := channel(float64((v >> 16) & 0xff))
	g := channel(float64((v >> 8) & 0xff))
	b := channel(float64(v & 0xff))
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func contrast(fg, bg string) float64 {
	a, b := luminance(fg), luminance(bg)
	if a < b {
		a, b = b, a
	}
	return (a + 0.05) / (b + 0.05)
}

// TestTextContrastMeetsAA guards the quiet text tiers. They were raised off
// the TUI's values precisely because they carried real prose below 4.5:1 —
// --text-ghost was at 2.08:1 — and nothing but this test stops someone
// re-syncing them to the terminal palette and undoing it. See the note at the
// top of styles.css.
func TestTextContrastMeetsAA(t *testing.T) {
	css := readFile(t, "website/assets/styles.css")

	text := []string{
		"--text", "--text-primary", "--text-secondary",
		"--text-dim", "--text-faint", "--text-ghost",
		"--accent", "--good", "--warn", "--bad", "--info",
	}

	for _, tc := range []struct {
		name  string
		block *regexp.Regexp
	}{
		{"dark", darkRe},
		{"light", lightRe},
	} {
		m := tc.block.FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("%s: palette block not found", tc.name)
		}
		pal := paletteFrom(m[1])
		bg, ok := pal["--bg"]
		if !ok {
			t.Fatalf("%s: --bg not found", tc.name)
		}
		names := append([]string(nil), text...)
		sort.Strings(names)
		for _, n := range names {
			fg, ok := pal[n]
			if !ok {
				continue
			}
			if r := contrast(fg, bg); r < 4.5 {
				t.Errorf("%s: %s (%s) on %s is %.2f:1, below the 4.5:1 body-text minimum",
					tc.name, n, fg, bg, r)
			}
		}
	}
}

var linkRe = regexp.MustCompile(`(?:href|src|poster)="([^"]+)"`)

// TestGeneratedSiteLinksResolve renders the site and follows every internal
// reference. Nothing else checks these: a renamed page or a mistyped asset
// path would otherwise reach production and 404 there, which is how the site
// carried a stale claim long enough to be tracked in three separate docs.
func TestGeneratedSiteLinksResolve(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}

	// Mirrors the staging in .github/workflows/deploy-pages.yml: generated
	// pages sit at the root next to website/, and the demo recordings are
	// merged in from docs/assets.
	resolve := func(p string) []string {
		return []string{
			filepath.Join(out, p),
			filepath.Join(repoRoot, "website", p),
			filepath.Join(repoRoot, "docs", "assets", strings.TrimPrefix(p, "assets/")),
		}
	}

	pages, err := filepath.Glob(filepath.Join(out, "*.html"))
	if err != nil || len(pages) == 0 {
		t.Fatalf("no pages rendered (%v)", err)
	}

	anchors := map[string]map[string]bool{} // page -> ids present
	for _, page := range pages {
		body := string(mustRead(t, page))
		ids := map[string]bool{}
		for _, m := range regexp.MustCompile(`id="([^"]+)"`).FindAllStringSubmatch(body, -1) {
			ids[m[1]] = true
		}
		anchors[filepath.Base(page)] = ids
	}

	for _, page := range pages {
		name := filepath.Base(page)
		body := string(mustRead(t, page))
		for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
			ref := m[1]
			switch {
			case strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"),
				strings.HasPrefix(ref, "mailto:"), strings.HasPrefix(ref, "data:"):
				continue // external; not this test's job
			case strings.HasPrefix(ref, "#"):
				if id := strings.TrimPrefix(ref, "#"); id != "" && !anchors[name][id] {
					t.Errorf("%s: #%s does not exist on the page", name, id)
				}
				continue
			case !strings.HasPrefix(ref, "/"):
				t.Errorf("%s: %q is relative; the shell must use root-absolute paths so 404.html works at any depth", name, ref)
				continue
			}

			target, frag, _ := strings.Cut(strings.TrimPrefix(ref, "/"), "#")
			if target == "" {
				continue
			}
			found := ""
			for _, cand := range resolve(target) {
				if _, err := os.Stat(cand); err == nil {
					found = cand
					break
				}
			}
			if found == "" {
				t.Errorf("%s: %s does not resolve to a file", name, ref)
				continue
			}
			if frag != "" && strings.HasSuffix(target, ".html") {
				if !anchors[filepath.Base(target)][frag] {
					t.Errorf("%s: %s points at #%s, which %s does not define", name, ref, frag, target)
				}
			}
		}
	}
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return b
}
