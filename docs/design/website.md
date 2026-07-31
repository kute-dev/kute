# kute.dev — site design

How the marketing site is built and what its rules are. For the TUI's design
spec see [`README.md`](README.md) in this directory; this file is only about
the website.

The site's palette mirrors the TUI's semantic `Theme` so the HTML-rebuilt
terminal mockups look like the real product. There is one deliberate exception
and it is called out below.

## How the pages are built

There are no hand-maintained HTML pages. `cmd/site` renders `website/dist/`
from:

| Source | What it holds |
| --- | --- |
| `website/pages/<slug>.html` | The body of one page, everything between the nav and the footer |
| `website/templates/page.html` | The shell: head, skip link, nav slot, `<main>`, footer, scripts |
| `website/templates/nav.html` | The nav, including `aria-current` on the active link |
| `website/templates/install.html` | The install panel, shared by three pages |
| `website/templates/icons.html` | SVG fragments used more than once |
| `website/site.json` | Per-page metadata plus site-wide values |

`website/dist/` is generated and gitignored. Preview with:

```sh
go run ./cmd/site && (cd website/dist && python3 -m http.server 8000)
```

Assets live at `website/assets/` and are copied at deploy time, so a local
preview needs them alongside `dist/` — or serve the staged tree the way
`.github/workflows/deploy-pages.yml` builds it. Demo recordings live in
`docs/assets/` and are merged in by the deploy job; they are **not** in the
repo under `website/`, so demo videos are missing from a naive local preview.

`text/template`, not `html/template`: there is no untrusted input, and
contextual escaping rewrites content inside attributes (an apostrophe in a
meta description becomes `&#39;`), which silently changes what is emitted.

## Colour tokens

Defined three times in `website/assets/styles.css` — once for dark, then the
light palette for an explicit `data-theme="light"` and again under
`prefers-color-scheme`. Plain CSS cannot share a declaration block between a
selector and an at-rule. The two light copies are asserted identical by
`TestLightPaletteBlocksMatch`, so they cannot drift.

| Token | Dark | Light |
| --- | --- | --- |
| `--bg` | `#0b0b10` | `#f7f7fa` |
| `--bg-chrome` | `#0e0e15` | `#eef0f4` |
| `--bg-strip` | `#0c0c12` | `#f2f3f7` |
| `--bg-card` | `#111119` | `#ffffff` |
| `--bg-card-hi` | `#16121e` | `#f4f1fa` |
| `--bg-palette` | `#101018` | `#ffffff` |
| `--border` | `#26263a` | `#d5d7e0` |
| `--border-subtle` | `#1c1c2c` | `#e4e6ee` |
| `--text` | `#f0f0fa` | `#14141c` |
| `--text-primary` | `#d8d8e8` | `#2a2a38` |
| `--text-secondary` | `#9a9ab2` | `#565668` |
| `--text-dim` | `#8f8fa4` | `#5b5b6e` |
| `--text-faint` | `#83839f` | `#64647d` |
| `--text-ghost` | `#78789b` | `#6d6d90` |
| `--accent` | `#a78bfa` | `#6b46d9` |
| `--accent-hi` | `#c4b5fd` | `#5936b8` |
| `--sel-bg` | `#1d1633` | `#ece5fb` |
| `--good` | `#34d17b` | `#13824a` |
| `--warn` | `#e8c74a` | `#936b09` |
| `--bad` | `#ef6a6a` | `#cc3a3a` |
| `--bad-soft` | `#ef8a8a` | `#d95c5c` |
| `--bad-text` | `#f0b7b7` | `#a02c2c` |
| `--info` | `#6aa8ef` | `#2a6fce` |

**The quiet text tiers deliberately do not match the TUI.** `--text-dim`,
`--text-faint` and `--text-ghost`, plus light `--good` and `--warn`, are
lighter here than in the terminal. They carry real prose on this page and have
to clear WCAG AA against `--bg`; a terminal running under the user's own
colour scheme is not held to that standard. Before the change `--text-ghost`
was at 2.08:1. `TestTextContrastMeetsAA` fails any token below 4.5:1, so
re-syncing them to the TUI palette will not pass CI.

Other tokens: `--radius-card: 14px`, `--radius-pill: 9999px`,
`--radius-term: 10px`, `--max-width: 1180px`, `--section-gap: 96px`, and a
four-step shadow scale. Fonts are Sora (display), Inter (UI) and JetBrains
Mono (terminal mockups), linked from the page head — never `@import`, which is
undiscoverable until the stylesheet has parsed and serialises the whole
third-party round trip behind it. Only weights actually used are requested.

## Rules

- **Both themes, always.** Anything new must work in dark and light, including
  under `prefers-color-scheme` with the toggle untouched. A selector list
  ending in a comma before an `@media` does not parse and silently drops the
  whole rule — that bug left the light theme with a dark nav bar for a while.
- **Every internal link is root-absolute.** GitHub Pages serves `404.html` for
  any missing path, so a relative asset path on a URL like `/a/b/c` resolves
  against `/a/b/` and the page renders unstyled. `TestGeneratedSiteLinksResolve`
  rejects relative links.
- **AA contrast for anything carrying text.** See above.
- **Focus must be visible.** Links have their underline removed and several
  controls drop their background and border, so the UA default ring is not
  enough. The `:focus-visible` ring is offset onto the page background so it
  stays legible over filled controls.
- **Nothing autoplays that a visitor cannot stop.** The demo recordings are
  `<video>` with controls, no `autoplay` attribute; script starts them only
  when `prefers-reduced-motion` is not set, and only while on screen. They
  were GIFs, which had no stop control at all.
- **`.reveal` starts at `opacity: 0`.** Anything that disables its transition
  has to restore opacity too, or the section stays permanently invisible. This
  is why the reduced-motion block and the `<noscript>` rule both exist.
- **One version string.** The verify page quotes `releaseVersion` from
  `site.json`; `TestReleaseVersionsAgree` checks it against
  `docs/verifying-releases.md` and `README.md`.

## Provenance

Layout rhythm, the aurora beam, pill geometry and the two-font split were
taken from huly.io. The reference doc is kept at
[`website-inspiration-huly.md`](website-inspiration-huly.md) — it describes
Huly's system, not kute's, and contains no kute content. **The palette was
never taken from it.**
