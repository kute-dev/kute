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

	"github.com/kute-dev/kute/internal/tui/verbs"
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

// TestKeyboardReferenceCoversVerbs holds the guide's key table to the app's
// own command registry. The site makes a claim about what the keys do, and a
// verb added, renamed or rebound in internal/tui/verbs has nothing else
// telling anyone the published reference has gone stale — the failure mode is
// a page that stays plausible while being wrong, which is worse than a
// missing one. Verbs the guide deliberately leaves out are listed here, so
// omitting one is a decision someone wrote down rather than an oversight.
func TestKeyboardReferenceCoversVerbs(t *testing.T) {
	guide := readFile(t, "website/pages/guide.html")

	// Anything deliberately left out belongs here with its reason.
	omitted := map[string]string{
		// These service-address copy actions are intentionally available only
		// in the Services list keybar and are not part of the public guide's
		// general keyboard reference.
		"copy-service-cluster-ip":  "service-list-only action",
		"copy-service-external-ip": "service-list-only action",
		// 0.8.0 plan Phase 3 registers these ahead of the screen that uses
		// them (tasks/cronjobschedule, Phase 6) so the registry, not the
		// screen, is the single source of truth for their key/label from the
		// start. Documenting them in the public guide before the screen
		// ships would describe a key that doesn't do anything yet — add both
		// to the keyboard reference once Phase 6 lands.
		"cronjob-focus-timezone":     "0.8.0 Phase 6 — tasks/cronjobschedule not yet built",
		"cronjob-schedule-full-edit": "0.8.0 Phase 6 — tasks/cronjobschedule not yet built",
	}

	for _, v := range verbs.All {
		if _, ok := omitted[v.ID]; ok {
			continue
		}
		if !strings.Contains(guide, v.Label) {
			t.Errorf("guide.html never mentions %q (verb %s, key %q) — either document it in the keyboard reference or add it to the omitted list with a reason",
				v.Label, v.ID, v.Key)
		}
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

func TestPagesUseEditorialShell(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, name := range []string{"index.html", "guide.html", "install.html", "verify.html", "404.html"} {
		body := string(mustRead(t, filepath.Join(out, name)))
		for _, want := range []string{
			`class="editorial-page`,
			`family=Geist`,
			`<span class="footer-license">Apache License 2.0</span>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%s does not contain %q", name, want)
			}
		}
		for _, unwanted := range []string{`hero-aurora`, `hero-sunburst`, `<div class="footer-legal">`} {
			if strings.Contains(body, unwanted) {
				t.Errorf("%s still contains %q", name, unwanted)
			}
		}
	}

	install := string(mustRead(t, filepath.Join(out, "install.html")))
	for _, want := range []string{
		`<h1>Install kute <em>in one command.</em></h1>`,
		`id="macos-linux" data-install-platform="unix"`,
		`id="windows" data-install-platform="windows"`,
	} {
		if !strings.Contains(install, want) {
			t.Errorf("install page does not contain %q", want)
		}
	}
	if got := strings.Count(install, `<span class="platform-match-label" hidden>Your platform</span>`); got != 2 {
		t.Errorf("install page has %d platform labels, want one for each detected platform section", got)
	}
	for _, unwanted := range []string{
		`<section class="install"`,
		`<h2>Install kute.</h2>`,
	} {
		if strings.Contains(install, unwanted) {
			t.Errorf("install page still contains %q", unwanted)
		}
	}

	home := string(mustRead(t, filepath.Join(out, "index.html")))
	if !strings.Contains(home, `<body class="editorial-page home-page">`) {
		t.Error("homepage lost its editorial and home page classes")
	}
	guide := string(mustRead(t, filepath.Join(out, "guide.html")))
	for _, want := range []string{
		`<span class="home-kicker"><span aria-hidden="true"></span>Using kute`,
		`<h1>Everything you need <em>to drive kute.</em></h1>`,
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("guide page does not contain %q", want)
		}
	}

	verify := string(mustRead(t, filepath.Join(out, "verify.html")))
	for _, want := range []string{
		`<span class="home-kicker"><span aria-hidden="true"></span>Signed releases`,
		`<h1>Verify a download <em>is really ours.</em></h1>`,
		`<section class="home-install">`,
	} {
		if !strings.Contains(verify, want) {
			t.Errorf("verify page does not contain %q", want)
		}
	}

	notFound := string(mustRead(t, filepath.Join(out, "404.html")))
	for _, want := range []string{
		`<span class="home-kicker"><span aria-hidden="true"></span>404 · page not found</span>`,
		`<h1>No object <em>of that name.</em></h1>`,
		`<div class="guide-layout wrap">`,
		`<nav class="guide-rail" aria-label="On this site">`,
	} {
		if !strings.Contains(notFound, want) {
			t.Errorf("404 page does not contain %q", want)
		}
	}
}

func TestHomeExplorerTabsHavePanels(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(mustRead(t, filepath.Join(out, "index.html")))
	tabRe := regexp.MustCompile(`<button id="(explorer-tab-[^"]+)" class="explorer-tab"[^>]+aria-selected="(true|false)" aria-controls="([^"]+)"`)
	tabs := tabRe.FindAllStringSubmatch(body, -1)
	if len(tabs) != 8 {
		t.Fatalf("homepage has %d explorer tabs, want 8", len(tabs))
	}
	wantTabs := []string{
		"explorer-tab-incident",
		"explorer-tab-navigation",
		"explorer-tab-batch",
		"explorer-tab-helm",
		"explorer-tab-routing",
		"explorer-tab-gitops",
		"explorer-tab-actions",
		"explorer-tab-safety",
	}

	selected := 0
	for i, tab := range tabs {
		if tab[1] != wantTabs[i] {
			t.Errorf("homepage explorer tab %d = %s, want %s", i, tab[1], wantTabs[i])
		}
		if tab[2] == "true" {
			selected++
		}
		panel := `id="` + tab[3] + `" class="explorer-panel" role="tabpanel" aria-labelledby="` + tab[1] + `"`
		if !strings.Contains(body, panel) {
			t.Errorf("%s controls %s, but that labelled panel is missing", tab[1], tab[3])
		}
	}
	if selected != 1 {
		t.Errorf("homepage has %d initially selected explorer tabs, want 1", selected)
	}

	if got := strings.Count(body, `class="explorer-subpanel" role="tabpanel"`); got != 21 {
		t.Errorf("homepage has %d explorer subpanels, want 21 grouped screens", got)
	}
	for _, id := range []string{
		"incident-panel-cluster", "incident-panel-pod", "incident-panel-certificate", "incident-panel-timeline",
		"navigation-panel-goto", "navigation-panel-namespace", "navigation-panel-context",
		"batch-panel-cronjob", "batch-panel-attempts",
		"helm-panel-releases", "helm-panel-history",
		"routing-panel-ingress", "routing-panel-http",
		"gitops-panel-flux", "gitops-panel-argo",
		"actions-panel-scale", "actions-panel-setimage", "actions-panel-resources", "actions-panel-forward",
		"safety-panel-nonprod", "safety-panel-prod",
	} {
		if !strings.Contains(body, `id="`+id+`" class="explorer-subpanel" role="tabpanel"`) {
			t.Errorf("homepage explorer is missing %s", id)
		}
	}
	for _, action := range []string{
		`Scale <code>+/−</code>`,
		`Resources <code>r</code>`,
		`Port-forward <code>f</code>`,
	} {
		if !strings.Contains(body, action) {
			t.Errorf("homepage explorer is missing action label %q", action)
		}
	}
}

func TestHomeScreenshotsHaveThemePairs(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}
	body := string(mustRead(t, filepath.Join(out, "index.html")))

	stems := []string{
		"home-hero",
		"home-triage",
		"home-navigation-goto",
		"home-navigation-namespace",
		"home-navigation-context",
		"home-pod-detail",
		"home-timeline",
		"home-batch-cronjob",
		"home-batch-attempts",
		"home-helm-releases",
		"home-helm-history",
		"home-routing-ingress",
		"home-routing-http",
		"home-flux",
		"home-argo",
		"home-nonprod-confirm",
		"home-prod-confirm",
		"home-certificate-failure",
		"home-actions-scale",
		"home-actions-setimage",
		"home-actions-resources",
		"home-actions-forward",
	}
	for _, stem := range stems {
		dark := `class="theme-shot theme-shot-dark" src="/assets/` + stem + `.png"`
		light := `class="theme-shot theme-shot-light" src="/assets/` + stem + `-light.png"`
		if !strings.Contains(body, dark) {
			t.Errorf("homepage does not render the dark %s screenshot", stem)
		}
		if !strings.Contains(body, light) {
			t.Errorf("homepage does not render the light %s screenshot", stem)
		}
	}

	hero := regexp.MustCompile(`(?s)<div class="hero-shot reveal">.*?home-hero\.png.*?home-hero-light\.png.*?</div>`)
	if !hero.MatchString(body) {
		t.Error("homepage hero does not use the dark and light home-hero captures")
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

func TestSupportingPagesUseHomePlatformInstallPanelWithoutSelfLink(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}

	panel := func(page string) string {
		t.Helper()
		body := string(mustRead(t, filepath.Join(out, page)))
		start := strings.Index(body, `<section class="home-install">`)
		if start < 0 {
			t.Fatalf("%s: platform install panel not found", page)
		}
		end := strings.Index(body[start:], "</section>")
		if end < 0 {
			t.Fatalf("%s: platform install panel is not closed", page)
		}
		return body[start : start+end+len("</section>")]
	}

	home := panel("index.html")
	guide := panel("guide.html")
	verify := panel("verify.html")
	guideLink := `<a href="/guide.html">Read the guide <span aria-hidden="true">→</span></a>`
	if !strings.Contains(home, guideLink) {
		t.Fatal("index.html: platform install panel is missing the guide link")
	}
	if strings.Contains(guide, guideLink) {
		t.Fatal("guide.html: platform install panel links to the page it is already on")
	}
	if want := strings.Replace(home, guideLink, "", 1); guide != want {
		t.Fatal("guide.html: platform install panel differs from index.html beyond the omitted guide link")
	}
	if verify != guide {
		t.Fatal("verify.html: platform install panel differs from guide.html")
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
