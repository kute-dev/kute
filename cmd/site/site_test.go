package main

import (
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
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
		slices.Sort(names)
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
		// node-debug-detail is the same §41d debug panel as node-debug ('x',
		// already documented under Nodes), reachable via 's' only inside
		// tasks/nodedetail specifically — that screen's own pods table
		// already claims 'x' for Exec on a pod row (verbs.go's
		// NodeDebugDetail doc comment explains the collision). It's an
		// implementation-scoped variant of an already-documented action, the
		// same category as the two service-copy actions above, not a
		// separate concept a new user needs taught.
		"node-debug-detail": "same action as node-debug ('x'), reachable via 's' only inside nodedetail's own pods-table screen",
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
	// pages sit at the root next to website/, and the recordings are merged in
	// from docs/assets — where shots/ and demos/ are flattened into one
	// assets/ directory, which is why a page reference names neither.
	resolve := func(p string) []string {
		asset := strings.TrimPrefix(p, "assets/")
		return []string{
			filepath.Join(out, p),
			filepath.Join(repoRoot, "website", p),
			filepath.Join(repoRoot, "docs", "assets", "shots", asset),
			filepath.Join(repoRoot, "docs", "assets", "demos", asset),
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

	if got := strings.Count(body, `class="explorer-subpanel" role="tabpanel"`); got != 25 {
		t.Errorf("homepage has %d explorer subpanels, want 25 grouped screens", got)
	}
	for _, id := range []string{
		"incident-panel-cluster", "incident-panel-pod", "incident-panel-certificate", "incident-panel-timeline",
		"incident-panel-debug",
		"debug-panel-attach", "debug-panel-copy", "debug-panel-node",
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

	// The tape tree is the inventory, not a list restated here. This used to
	// be a hardcoded slice of 25 stems, which made a new checkpoint three
	// separate edits (tape, index.html, this test) with nothing failing if you
	// made only two of them.
	stems := homeTapeStems(t)
	if len(stems) == 0 {
		t.Fatal("no home-*.tape files found under docs/assets/tapes")
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

// --- Recording tapes and the media they produce -----------------------------
//
// docs/assets/ separates source from output: tapes/ holds one .tape per
// capture, shots/ the homepage PNGs, demos/ the guide clips. The rule the
// tests below enforce is that a tape stem names its output exactly, in both
// directions, so nothing can be published that no tape reproduces and no tape
// can quietly stop being recorded. docs/assets/README.md documents the layout.

const (
	tapesDir = "docs/assets/tapes"
	shotsDir = "docs/assets/shots"
	demosDir = "docs/assets/demos"
)

var (
	tapeOutputRe     = regexp.MustCompile(`(?m)^Output "([^"]+)"`)
	tapeScreenshotRe = regexp.MustCompile(`(?m)^Screenshot "([^"]+)"`)
	tapeWidthRe      = regexp.MustCompile(`(?m)^Set Width (\d+)`)
	tapeHeightRe     = regexp.MustCompile(`(?m)^Set Height (\d+)`)
)

// tapeStems lists the stems of every tape matching pattern, e.g. "home-*".
func tapeStems(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(repoRoot, tapesDir, pattern+".tape"))
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	stems := make([]string, 0, len(paths))
	for _, p := range paths {
		stems = append(stems, strings.TrimSuffix(filepath.Base(p), ".tape"))
	}
	slices.Sort(stems)
	return stems
}

func homeTapeStems(t *testing.T) []string { return tapeStems(t, "home-*") }

// tapePaths lists every path a tape names with the given directive, in order.
// A tape may name more than one: the demo tapes write an mp4 with Output and
// capture a still with Screenshot from the same recording.
func tapePaths(t *testing.T, tape string, re *regexp.Regexp) []string {
	t.Helper()
	ms := re.FindAllStringSubmatch(tape, -1)
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m[1])
	}
	return out
}

func tapeOutputs(t *testing.T, tape string) []string {
	t.Helper()
	return tapePaths(t, tape, tapeOutputRe)
}

func tapeScreenshots(t *testing.T, tape string) []string {
	t.Helper()
	return tapePaths(t, tape, tapeScreenshotRe)
}

// tapeField pulls one Set value out of a tape. Betamax has no include
// directive, so every tape carries its own preamble and these are the values
// that can drift against what the page declares.
func tapeField(t *testing.T, tape string, re *regexp.Regexp) string {
	t.Helper()
	m := re.FindStringSubmatch(tape)
	if m == nil {
		return ""
	}
	return m[1]
}

func fileExists(t *testing.T, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(repoRoot, rel))
	return err == nil
}

// pngSize reads a PNG's pixel dimensions out of its IHDR chunk: the 8-byte
// signature, then a length and type, then width and height as big-endian
// uint32. Enough to answer "was this actually recorded at the size its tape
// asks for", which existence alone does not — a re-record at a new Set Height
// otherwise leaves the old capture in place, still passing, while the page
// reserves a box of the wrong shape and reflows as it loads.
func pngSize(t *testing.T, rel string) (w, h int) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil || len(b) < 24 || string(b[12:16]) != "IHDR" {
		t.Errorf("%s is not a readable PNG (%v)", rel, err)
		return 0, 0
	}
	return int(binary.BigEndian.Uint32(b[16:20])), int(binary.BigEndian.Uint32(b[20:24]))
}

// TestRecordingTapesAndAssetsAgree pairs every committed recording with the
// tape that produces it. Nothing else does: a renamed tape, a deleted capture,
// or a capture left behind at the size an older tape asked for all leave a site
// that renders fine locally and is wrong or broken once deployed. Two of those
// had already happened before this test existed — four output stems that did
// not match their tape, and a home-prod-confirm whose dark and light captures
// were framed at different heights.
func TestRecordingTapesAndAssetsAgree(t *testing.T) {
	out := t.TempDir()
	if err := run(filepath.Join(repoRoot, "website"), out); err != nil {
		t.Fatalf("render: %v", err)
	}
	index := string(mustRead(t, filepath.Join(out, "index.html")))

	owned := map[string]bool{} // repo-relative path -> produced by some tape

	for _, stem := range homeTapeStems(t) {
		tape := readFile(t, filepath.Join(tapesDir, stem+".tape"))

		// path, not filepath: these are compared against the paths written
		// inside the tapes, which are always slash-separated. filepath.Join
		// would build "docs\\assets\\shots\\..." on Windows and fail every
		// comparison there.
		dark := path.Join(shotsDir, stem+".png")
		light := path.Join(shotsDir, stem+"-light.png")
		owned[dark], owned[light] = true, true

		if got := tapeField(t, tape, tapeOutputRe); got != dark {
			t.Errorf("%s.tape writes %q; a tape must own the output named after it (%s)", stem, got, dark)
		}
		w, h := tapeField(t, tape, tapeWidthRe), tapeField(t, tape, tapeHeightRe)
		if w == "" || h == "" {
			t.Errorf("%s.tape declares no Set Width/Set Height", stem)
			continue
		}

		for _, png := range []string{dark, light} {
			if !fileExists(t, png) {
				t.Errorf("%s is referenced by %s.tape but not committed; re-record with scripts/record-demo.sh", png, stem)
				continue
			}
			if pw, ph := pngSize(t, png); strconv.Itoa(pw) != w || strconv.Itoa(ph) != h {
				t.Errorf("%s is %dx%d but %s.tape records at %sx%s; re-record it with scripts/record-demo.sh",
					png, pw, ph, stem, w, h)
			}
		}

		// scripts/lib/record.sh derives the light capture by rewriting exactly
		// these two things plus the Output path. A tape that omits either
		// silently records the dark screen twice.
		if !strings.Contains(tape, `Set Theme "3024 Night"`) {
			t.Errorf(`%s.tape must declare Set Theme "3024 Night"; the light capture is derived by swapping it for "3024 Day"`, stem)
		}
		if !strings.Contains(tape, "--theme dark") {
			t.Errorf("%s.tape must pass --theme dark rather than relying on terminal auto-detection; the light capture is derived by swapping it for --theme light", stem)
		}

		// The tape owns the frame size, and index.html has to declare the same
		// one or the browser reserves the wrong box and the page reflows as
		// the screenshot loads.
		for _, variant := range []string{stem, stem + "-light"} {
			src := `src="/assets/` + variant + `.png"`
			i := strings.Index(index, src)
			if i < 0 {
				continue // TestHomeScreenshotsHaveThemePairs reports the missing tag
			}
			tag := index[i:]
			if end := strings.Index(tag, ">"); end >= 0 {
				tag = tag[:end]
			}
			if want := ` width="` + w + `" height="` + h + `"`; !strings.Contains(tag, want) {
				t.Errorf("index.html declares %s at different dimensions than %s.tape records it (want%s)", variant+".png", stem, want)
			}
		}
	}

	guide := string(mustRead(t, filepath.Join(out, "guide.html")))

	for _, stem := range tapeStems(t, "demo-*") {
		tape := readFile(t, filepath.Join(tapesDir, stem+".tape"))

		// Betamax writes both from one recording: Output gives the mp4 the
		// guide plays, and a Screenshot placed at the clip's payoff beat gives
		// the PNG, which is that video's poster and the still README shows.
		// GitHub strips <video> and will not play an mp4 committed to a repo,
		// so the still is the only motion-free thing that page can show — and
		// it is also what a reduced-motion visitor sees on the site. It is a
		// chosen frame rather than Output's last one because a walkthrough
		// does not reliably end on its own payoff: demo-all-namespaces closes
		// on an empty timeline.
		mp4 := path.Join(demosDir, stem+".mp4")
		still := path.Join(demosDir, stem+".png")
		owned[mp4], owned[still] = true, true

		if got := tapeOutputs(t, tape); !slices.Equal(got, []string{mp4}) {
			t.Errorf("%s.tape writes %v; a demo tape's Output must be exactly [%s]", stem, got, mp4)
		}
		if got := tapeScreenshots(t, tape); !slices.Equal(got, []string{still}) {
			t.Errorf("%s.tape screenshots %v; a demo tape must capture exactly [%s]", stem, got, still)
		}
		for _, f := range []string{mp4, still} {
			if !fileExists(t, f) {
				t.Errorf("%s is missing; re-record with scripts/record-demo.sh", f)
			}
		}

		w, h := tapeField(t, tape, tapeWidthRe), tapeField(t, tape, tapeHeightRe)
		if w == "" || h == "" {
			t.Errorf("%s.tape declares no Set Width/Set Height", stem)
			continue
		}
		// libx264 refuses an odd dimension: the encode fails and betamax
		// leaves a zero-byte mp4 behind, which no other check here would
		// notice because the file does exist.
		if wi, _ := strconv.Atoi(w); wi%2 != 0 {
			t.Errorf("%s.tape has odd Set Width %s; libx264 cannot encode it", stem, w)
		}
		if hi, _ := strconv.Atoi(h); hi%2 != 0 {
			t.Errorf("%s.tape has odd Set Height %s; libx264 cannot encode it", stem, h)
		}
		if fileExists(t, still) {
			if pw, ph := pngSize(t, still); strconv.Itoa(pw) != w || strconv.Itoa(ph) != h {
				t.Errorf("%s is %dx%d but %s.tape records at %sx%s; re-record it", still, pw, ph, stem, w, h)
			}
		}

		// Same rule the homepage screenshots follow: the tape owns the frame
		// size and the page has to declare it. The guide used to give all five
		// videos 840x434 while the goto-palette recording was really 1040x616.
		src := `src="/assets/` + stem + `.mp4"`
		if i := strings.Index(guide, src); i >= 0 {
			tag := guide[i:]
			if end := strings.Index(tag, ">"); end >= 0 {
				tag = tag[:end]
			}
			if want := ` width="` + w + `" height="` + h + `"`; !strings.Contains(tag, want) {
				t.Errorf("guide.html declares %s.mp4 at different dimensions than its tape records (want%s)", stem, want)
			}
		} else {
			t.Errorf("guide.html does not play %s.mp4", stem)
		}
	}

	// The other direction: a capture nothing reproduces is worse than a
	// missing one, because it looks current and cannot be regenerated.
	for _, dir := range []string{shotsDir, demosDir} {
		entries, err := os.ReadDir(filepath.Join(repoRoot, dir))
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if rel := path.Join(dir, e.Name()); !owned[rel] {
				t.Errorf("%s is committed but no tape in %s produces it", rel, tapesDir)
			}
		}
	}
}
