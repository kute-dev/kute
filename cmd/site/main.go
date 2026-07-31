// Command site renders website/ from templates.
//
// The four pages shared a nav, a head block, a footer and an install panel by
// copy-paste — about a quarter of the site's HTML, with nothing keeping the
// copies in step. They had already drifted: index.html carried a shorter
// footer tagline than the other three, and its nav icons were missing the
// aria-hidden the same icons had elsewhere.
//
// Page-unique body content lives in website/pages/<slug>.html and is emitted
// verbatim. Everything shared lives in website/templates/. Per-page data is
// website/site.json.
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
	NavIconsHidden     bool          `json:"navIconsHidden"`
	FooterTag          string        `json:"footerTag"`
	Install            *installPanel `json:"install"`

	// Body is the page-unique markup, read from pages/<slug>.html.
	Body string `json:"-"`
}

// AnchorPrefix is empty on the landing page, whose section anchors are local,
// and "index.html" everywhere else, where they have to travel.
func (p page) AnchorPrefix() string {
	if p.Home {
		return ""
	}
	return "index.html"
}

// IconAttr is the aria-hidden attribute for the nav's decorative icons. The
// landing page's copies were written without it; kept per-page so the two
// stay byte-identical to what they replaced, and fixing that is a data change.
func (p page) IconAttr() string {
	if p.NavIconsHidden {
		return ` aria-hidden="true"`
	}
	return ""
}

type site struct {
	Pages []page `json:"pages"`
}

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
		p.Body = string(body)

		var buf bytes.Buffer
		if err := tmpl.ExecuteTemplate(&buf, "page.html", p); err != nil {
			return fmt.Errorf("%s: %w", p.Slug, err)
		}

		dest := filepath.Join(outDir, p.Slug+".html")
		if err := os.WriteFile(dest, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}
