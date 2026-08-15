# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Primary: engineers and SREs who operate Kubernetes clusters, arriving at kute.dev either mid-incident (searching for a faster way to triage a broken cluster) or while evaluating tools for their day-to-day workflow. Their job on this site is to quickly understand what kute does differently from `kubectl`/`k9s`/Lens and decide whether to install it — the site's own job is to get them from "what is this" to a running `kute --demo` or a real install in one command, with no signup and no cluster required to try it first.

## Product Purpose

kute.dev is the marketing/documentation site for kute, a keyboard-driven Kubernetes TUI (Bubble Tea + Lip Gloss) built around the first 15 minutes of an incident rather than plain object browsing. The site exists to explain that positioning, show the product's actual screens (via faithful HTML/CSS terminal mockups, not screenshots), and convert a visiting engineer into an install. Success is a completed install or a `--demo` trial with zero friction — the CTA is always one copyable shell command away.

## Positioning

"The incident console for Kubernetes" — not a generic resource browser. The mechanism a competing dashboard/TUI could not truthfully copy without doing the same design work:

- Unhealthy-first triage: every list sorts broken workloads to the top; healthy groups collapse to one line.
- Restart-aware logs and an incident timeline that merges events, container restarts, and rollout revisions newest-first.
- CronJob failures promote the controller's reason above retained run history; Jobs open an attempt ledger with retry outcomes and exit codes.
- Termination causes and conditions shown verbatim, never paraphrased.
- Every mutating action shows the exact command it's about to run before it runs — copyable documentation, not a black box.
- Tiered destructive-action friction: reversible verbs (cordon) execute immediately; delete/rollout-restart use inline y/N normally and a type-the-name modal only when the kubeconfig context is explicitly tagged production (never guessed from a name); drain always confirms.
- CRDs, Flux CD reconcilers, and Argo CD Applications render correctly with zero configuration — no plugins, no per-CRD setup, no separate binary.
- One palette (`g`) jumps to any resource kind by alias/fuzzy name or a specific object by name; alt-tab recall for the last two namespaces/contexts.

## Operating Context

Visitors read this site from a browser, generally while at a terminal or about to be at one — often mid-incident with a broken cluster in another window, sometimes calmly evaluating tools ahead of time. Key pages: `index` (positioning/proof), `guide` (full keyboard-driven walkthrough: browsing, diagnosing, acting safely, working with resources/Flux), `install` (macOS/Linux/Windows, package managers, first run, troubleshooting), `verify` (how to check the cosign signature, SBOM, and GitHub build-provenance attestation on a downloaded archive by hand). The site is a static build (Go template generator: `templates/` + `pages/*.html` + `site.json` → `dist/`) deployed to GitHub Pages at the `kute.dev` custom domain (see `CNAME`).

## Capabilities and Constraints

- Static site, no backend, no accounts, no tracking-driven personalization implied by anything in the codebase.
- Install paths that must all stay accurate: `curl -fsSL https://kute.dev/install.sh | sh`, `brew install kute-dev/tap/kute`, PowerShell `irm https://kute.dev/install.ps1 | iex`, `scoop bucket add kute-dev ... && scoop install kute`, plus manual archive download.
- Every release is cosign-signed (keyless), ships an SBOM, and carries a GitHub build-provenance attestation — the `verify` page's claims are load-bearing and must stay procedurally accurate to the actual release workflow, not just plausible-sounding.
- Product screens on the site are hand-built HTML/CSS terminal mockups (`.term` component), not screenshots or embedded GIFs — they must stay visually faithful to kute's actual rendered output (the TUI's own semantic Theme, glyphs, and layout), since a mockup that drifts from the real product is a credibility risk, not just a cosmetic one.
- `releaseVersion` / `signedFrom` in `site.json` must track the actual latest kute release the site is documenting.
- No signup flow, no pricing — kute is free/open-source (GitHub: `kute-dev/kute`).

## Brand Commitments

- Name is always lowercase "kute." Tagline: "the incident console for Kubernetes." Footer tag: "Your cluster, in plain sight. Answers, not objects."
- Mark glyph: `❯` (a terminal prompt caret), used as the wordmark prefix.
- Voice: terse, concrete, confident without hype — describes exact mechanisms ("shows the command it will run first") rather than adjectives ("powerful", "enterprise-grade"). No claims not traceable to an actual feature.
- GitHub org/repo: `kute-dev/kute`. Homebrew tap: `kute-dev/tap`. Scoop bucket: `kute-dev/scoop-bucket`.

## Evidence on Hand

- Full README and CLAUDE.md in the parent repo describe every screen, verb, and design invariant of the actual TUI — authoritative source for any claim the site makes about product behavior.
- Recorded demo GIFs exist in the parent repo (`docs/assets/*.gif`, e.g. incident walkthrough, namespace palette, goto palette), captured against `kute --demo`'s built-in fake cluster via `scripts/record-demo.sh`. The website itself currently represents screens via hand-built terminal mockups rather than these GIFs/screenshots — do not assume the two are interchangeable without checking `pages/*.html`.
- No customer testimonials, logos, case studies, press mentions, or usage benchmarks exist anywhere in the repo. Future work must not fabricate any of these.
- Install/verify pages encode real, checkable procedures (signature/SBOM/attestation verification steps) — treat as evidence to preserve exactly, not copy to rephrase loosely.

## Product Principles

1. One command to try, one command to install — never add friction (signup, config, a required cluster) ahead of the CTA.
2. Every claim on the site must be traceable to an actual, current kute behavior or a real, checkable release artifact (signature/SBOM/attestation) — no aspirational or unverifiable claims.
3. Product mockups shown on the site are a promise about what installing kute actually looks like; they must stay faithful to the real TUI's rendering, not just "inspired by" it.
4. Speak to an engineer mid-incident as well as one calmly evaluating tools — concrete and terse always beats hype, in both states of mind.

## Accessibility & Inclusion

No formal standard is currently required. Build with reasonable care (contrast, keyboard navigation, semantic HTML) as a baseline; no compliance target is binding at this time.
