package main

import (
	"encoding/json"
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

// TestReleaseVersionsAgree keeps the verify page's worked example in step
// with the same commands in the docs. The page used to hard-code the version
// in four places; they are one value now, but that value still has to match
// what docs/verifying-releases.md and README.md tell people to run, and a
// release bump that updates one and not the others is the failure this
// catches. Both files are the prose version of the same commands.
func TestReleaseVersionsAgree(t *testing.T) {
	var s site
	if err := jsonUnmarshalFile(t, "website/site.json", &s); err != nil {
		t.Fatal(err)
	}
	if s.ReleaseVersion == "" || s.SignedFrom == "" {
		t.Fatal("site.json must set releaseVersion and signedFrom")
	}

	for _, f := range []string{"docs/verifying-releases.md", "README.md"} {
		body := readFile(t, f)
		if want := "VERSION=" + s.ReleaseVersion; !strings.Contains(body, want) {
			t.Errorf("%s does not contain %q — site.json says releaseVersion=%s",
				f, want, s.ReleaseVersion)
		}
	}
	if body := readFile(t, "docs/verifying-releases.md"); !strings.Contains(body, s.SignedFrom) {
		t.Errorf("docs/verifying-releases.md does not mention %s, the release site.json calls the first signed one",
			s.SignedFrom)
	}
}

func jsonUnmarshalFile(t *testing.T, rel string, v any) error {
	t.Helper()
	return json.Unmarshal([]byte(readFile(t, rel)), v)
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

// TestSocialCardResolves checks the one asset no link checker sees. og:image
// and twitter:image are absolute URLs in content= attributes, so they are
// neither an internal path the link test follows nor an <a> the outbound
// check visits — a missing card would just render as a blank social preview.
func TestSocialCardResolves(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}
	var s site
	if err := jsonUnmarshalFile(t, "website/site.json", &s); err != nil {
		t.Fatal(err)
	}

	body := string(mustRead(t, filepath.Join(out, "index.html")))
	re := regexp.MustCompile(`(?:property|name)="(og:image|twitter:image)" content="([^"]+)"`)
	found := re.FindAllStringSubmatch(body, -1)
	if len(found) != 2 {
		t.Fatalf("expected og:image and twitter:image on the landing page, got %d", len(found))
	}
	for _, m := range found {
		tag, url := m[1], m[2]
		if !strings.HasPrefix(url, s.SiteURL) {
			t.Errorf("%s is %q, which is not absolute under %s — scrapers require an absolute URL", tag, url, s.SiteURL)
			continue
		}
		rel := strings.TrimPrefix(url, s.SiteURL)
		if _, err := os.Stat(filepath.Join(repoRoot, "website", rel)); err != nil {
			t.Errorf("%s points at %s, which is not a file in website/", tag, rel)
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
