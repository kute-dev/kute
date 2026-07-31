// Command site renders website/ from templates.
//
// The four pages shared a nav, a head block, a footer and an install panel by
// copy-paste — about a quarter of the site's HTML, with nothing keeping the
// copies in step. They had already drifted: index.html carried a shorter
// footer tagline than the other three, and its nav icons were missing the
// aria-hidden the same icons had elsewhere.
//
// Page-unique body content lives in website/pages/<slug>.html. Everything
// shared lives in website/templates/, and per-page data in website/site.json.
// Bodies are themselves templates, so a page can quote a value that has to
// stay in step with something outside it — the release version the verify
// page's worked example uses — but they are otherwise emitted as written.
//
// This uses text/template rather than html/template deliberately. There is no
// untrusted input here — the inputs are this repo's own hand-written HTML —
// and html/template's contextual escaping rewrites content inside attributes
// (an apostrophe in a meta description becomes &#39;), which would silently
// change the bytes it emits. text/template passes the source through exactly.
package main

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

// installPanel is the shared install/CTA section. Nil for pages that omit it.
type installPanel struct {
	ID          string `json:"id"`
	SecondHref  string `json:"secondHref"`
	SecondLabel string `json:"secondLabel"`
	ThirdHref   string `json:"thirdHref"`
	ThirdLabel  string `json:"thirdLabel"`
}

// IDAttr renders the id attribute, including its leading space, or nothing.
// Only the landing page anchors #install at this section.
func (i installPanel) IDAttr() string {
	if i.ID == "" {
		return ""
	}
	return fmt.Sprintf(" id=%q", i.ID)
}

type page struct {
	Slug               string        `json:"slug"`
	Title              string        `json:"title"`
	Description        string        `json:"description"`
	Canonical          string        `json:"canonical"`
	OGTitle            string        `json:"ogTitle"`
	OGDescription      string        `json:"ogDescription"`
	TwitterDescription string        `json:"twitterDescription"`
	Home               bool          `json:"home"`
	NoIndex            bool          `json:"noIndex"`
	Install            *installPanel `json:"install"`

	// CTA closes the page with the compact call to action instead. Only the
	// install page carries the full panel now: repeating four package
	// managers at the foot of every page made each one longer without
	// telling anyone anything they could not get by following one link.
	CTA bool `json:"cta"`

	// Body is the page-unique markup, read from pages/<slug>.html.
	// The rest are site-level values copied onto every page at load.
	Body           string `json:"-"`
	FooterTag      string `json:"-"`
	SiteURL        string `json:"-"`
	StructuredData string `json:"-"`

	// ReleaseVersion and SignedFrom are the version strings the verify page
	// quotes. They were hard-coded in four places with nothing keeping them
	// in step with each other or with docs/verifying-releases.md.
	ReleaseVersion string `json:"-"`
	SignedFrom     string `json:"-"`
}

// NavCurrent marks the nav link pointing at the page being rendered. Without
// it nothing in the markup says which of the links you are already on.
func (p page) NavCurrent(slug string) string {
	if p.Slug == slug {
		return ` aria-current="page"`
	}
	return ""
}

type site struct {
	// FooterTag is one string for the whole site rather than a per-page
	// field: the hand-maintained copies had already drifted apart once.
	FooterTag string `json:"footerTag"`
	SiteURL   string `json:"siteURL"`

	// ReleaseVersion is the release the verify page's worked example uses,
	// without a leading v. SignedFrom is the first release that carries
	// signatures. Both must match docs/verifying-releases.md, which the
	// tests assert.
	ReleaseVersion string `json:"releaseVersion"`
	SignedFrom     string `json:"signedFrom"`

	Pages []page `json:"pages"`
}

// structuredData is the SoftwareApplication node, emitted on the landing page
// only — repeating it per page describes four applications, not one.
const structuredData = `{
  "@context": "https://schema.org",
  "@type": "SoftwareApplication",
  "name": "kute",
  "applicationCategory": "DeveloperApplication",
  "operatingSystem": "Linux, macOS, Windows",
  "description": "A terminal console for Kubernetes. The broken thing sorts to the top, every action shows the command it will run before it runs it, and nothing goes blank when the connection drops.",
  "url": "https://kute.dev/",
  "downloadUrl": "https://github.com/kute-dev/kute/releases",
  "license": "https://www.apache.org/licenses/LICENSE-2.0",
  "offers": { "@type": "Offer", "price": "0", "priceCurrency": "USD" }
}`

func main() {
	root := flag.String("root", "website", "website source directory")
	out := flag.String("out", "", "output directory (default <root>/dist)")
	flag.Parse()

	outDir := *out
	if outDir == "" {
		outDir = filepath.Join(*root, "dist")
	}

	if err := run(*root, outDir); err != nil {
		fmt.Fprintln(os.Stderr, "site:", err)
		os.Exit(1)
	}
}

func run(root, outDir string) error {
	raw, err := os.ReadFile(filepath.Join(root, "site.json"))
	if err != nil {
		return err
	}
	var s site
	if err := json.Unmarshal(raw, &s); err != nil {
		return fmt.Errorf("site.json: %w", err)
	}

	tmpl, err := template.ParseGlob(filepath.Join(root, "templates", "*.html"))
	if err != nil {
		return err
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	for _, p := range s.Pages {
		body, err := os.ReadFile(filepath.Join(root, "pages", p.Slug+".html"))
		if err != nil {
			return err
		}
		p.FooterTag = s.FooterTag
		p.SiteURL = s.SiteURL
		p.ReleaseVersion = s.ReleaseVersion
		p.SignedFrom = s.SignedFrom
		if p.Home {
			p.StructuredData = structuredData
		}

		// Bodies are templates too, so a page can quote a value that has to
		// stay in step with something else (the release version), and can
		// reuse a shared fragment — the copy-button icon appears a dozen
		// times across the install and guide pages, and pasting the same SVG
		// path that often is how the copies drift. They are otherwise emitted
		// exactly as written. Cloned per page so one body's definitions can
		// never leak into the next one's.
		bodySet, err := tmpl.Clone()
		if err != nil {
			return err
		}
		bodyTmpl, err := bodySet.New(p.Slug).Parse(string(body))
		if err != nil {
			return fmt.Errorf("pages/%s.html: %w", p.Slug, err)
		}
		var rendered bytes.Buffer
		if err := bodyTmpl.Execute(&rendered, p); err != nil {
			return fmt.Errorf("pages/%s.html: %w", p.Slug, err)
		}
		p.Body = rendered.String()

		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page.html", p); err != nil {
			return fmt.Errorf("%s: %w", p.Slug, err)
		}

		dest := filepath.Join(outDir, p.Slug+".html")
		if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return writeSiteFiles(s, outDir)
}

// writeSiteFiles emits the whole-site files that have no page of their own.
func writeSiteFiles(s site, outDir string) error {
	var sitemap bytes.Buffer
	sitemap.WriteString(xml.Header)
	sitemap.WriteString("<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">\n")
	for _, p := range s.Pages {
		if p.NoIndex || p.Canonical == "" {
			continue
		}
		fmt.Fprintf(&sitemap, "  <url><loc>%s</loc></url>\n", p.Canonical)
	}
	sitemap.WriteString("</urlset>\n")

	robots := "User-agent: *\nAllow: /\n\nSitemap: " + s.SiteURL + "sitemap.xml\n"

	manifest := `{
  "name": "kute",
  "short_name": "kute",
  "description": "The incident console for Kubernetes",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#0b0b10",
  "theme_color": "#0b0b10",
  "icons": [
    { "src": "/assets/favicon.svg", "sizes": "any", "type": "image/svg+xml" },
    { "src": "/assets/apple-touch-icon.png", "sizes": "180x180", "type": "image/png" }
  ]
}
`
	for name, content := range map[string]string{
		"sitemap.xml":      sitemap.String(),
		"robots.txt":       robots,
		"site.webmanifest": manifest,
	} {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
