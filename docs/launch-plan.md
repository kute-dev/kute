# kute — beta → 1.0 plan

Everything between `0.4.0-beta.1` and `1.0.0`, with the reasoning for why each item
gates the launch rather than being post-1.0 polish.

There is no release candidate. RC exists to coordinate a soak across a userbase and to
stop other contributors landing features during a freeze; this project has one
maintainer and one decider, so RC1/RC2/promotion is ceremony that buys nothing that
tagging beta and waiting doesn't. Beta *is* the soak — and reaches more people than an
RC would, for the reason in §3.

What RC would have contained is not ceremony, and is below.

---

## What "1.0" means here

[`beta-plan.md`](beta-plan.md)'s four gates were about whether the app is honest. These
are about whether the artifact is trustworthy and whether a user can get back off it:

1. **The download is provably yours.** Not just uncorrupted — attributable.
2. **A vulnerability has somewhere to go** that isn't a public issue.
3. **`latest` means stable.** The word on the tag matches what an installer does.
4. **Downgrade works**, and has been run rather than reasoned about.
5. **The binaries run where the README says they do**, tested, not inferred.

---

## 1. The trust chain

`checksums.txt` is fetched from the same host, over the same channel, as the archive it
describes — both installers verify it (`website/install.sh:78-87`,
`install.ps1:75-84`), which proves the download wasn't corrupted in transit. It does not
prove the release is yours: anyone who can tamper with one file can tamper with both.

This matters more here than for most tools. kute reads kubeconfigs, holds cluster
credentials in memory, and can delete production workloads on a keypress. A
`curl | sh` install with no signature is the weakest link in that chain, and it is hard
to retrofit after 1.0 because users' verification habits form at install time.

- cosign keyless signing of archives and checksums.
- `sboms:` (syft) in `.goreleaser.yaml`.
- `actions/attest-build-provenance` in `release.yml`.
- A verify snippet in the README, next to the install commands.

`govulncheck.yml` already covers the dependency half of supply chain.

*Acceptance:* `cosign verify-blob` against a published release succeeds with the
documented `--certificate-identity`, and fails against a modified archive.

## 2. Security disclosure path

There is no `SECURITY.md` and no private channel.

This is now an active hazard rather than an omission, because
[`diagnostics.md`](diagnostics.md) shipped: the bug form tells people to attach a crash
report to a **public issue**, and those files name contexts, clusters and namespaces.
That is the right instruction for a crash and exactly wrong for a vulnerability. The two
paths have to be visibly different.

- `SECURITY.md` with GitHub private advisories as the channel and a supported-versions
  line.
- One line at the top of `bug_report.yml`: not for security issues → here.

*Acceptance:* a reporter looking at the issue chooser can tell in one read which path is
theirs.

## 3. `latest` must mean stable

No release is marked prerelease on GitHub — `gh release list` shows `v0.3.0-alpha.8` as
`Latest`, and `.goreleaser.yaml` sets no `release.prerelease`. Two consequences, both
currently invisible:

- `install.sh` resolves `latest` through `/releases/latest` (`website/install.sh:55-58`),
  so `curl | sh` on a clean machine installs an alpha.
- 28a's update chip uses the same endpoint, so every user is prompted to upgrade to
  alphas.

**This is fine now and wrong at 1.0.** Pre-1.0, everyone installing kute is an early
adopter and the behaviour is what gets a beta soaked at all — it is the reason skipping
RC costs nothing. The moment `1.0.0` exists, `latest` has to mean stable or the version
number is decoration.

So this is a dated change, not a bug fix: set `release.prerelease: auto` (any hyphenated
semver tag gets marked) **as part of the 1.0 release, not before it** — doing it earlier
would silence the beta.

Deciding it now also settles the open question underneath: once prereleases stop
reaching people by default, an opt-in `update.channel: prerelease` is the only way
future betas soak. Worth building at the same time, or explicitly declining.

*Acceptance:* after 1.0, a fresh `curl | sh` installs `1.0.0` with a `1.1.0-beta.1`
published; `KUTE_VERSION=1.1.0-beta.1` still installs it explicitly.

## 4. Rollback is proven

`state.go` discards an unrecognized newer schema version rather than partially
interpreting it, which is the whole rollback story a user has. It has been reasoned
about, not run.

*Acceptance:* 1.0 → beta → 1.0 as a round trip on one machine, with a populated state
file: recents, per-context namespace/kind, and update-check state survive or degrade
cleanly, and nothing is corrupted in either direction.

## 5. The artifact runs where the README claims

`test/e2e` is linux + kind only. The released darwin and windows binaries are never
executed by CI — inferred to work from `GOOS` alone.

The gap is not theoretical: `6735c4a` was a Windows-only bug in the context palette, and
[`beta-plan.md`](beta-plan.md) §1's terminal work lands exactly where Windows differs.

*Acceptance:* a macOS and a Windows runner launch the *released archive*, render a
frame, and quit clean. `KUTE_CRASH_TEST=1` on the same runners proves the crash path
works on each platform — the one place the diagnostics work is platform-sensitive.

## 6. Launch mechanics

- README's project-status paragraph moves from beta's contract to the 1.0 compatibility
  promise, including the deprecation policy. Fold this into
  [`beta-plan.md`](beta-plan.md) §5's freeze rather than writing a second document.
- `website/index.html:316`'s drain claim (beta-plan §7) is wrong copy on the page a
  launch drives traffic to. Fix it before, not after.
- The changelog for 1.0 is the accumulated `feat`/`fix`/`perf` subjects — a pass to read
  them as a user would, since they flow verbatim into 28b's what's-new panel.

## Deliberately not doing

- **A release candidate tag.** See the top.
- **macOS notarization.** Homebrew and `curl | sh` both avoid the Gatekeeper quarantine
  attribute; it only bites browser downloads, which is not a documented install path.
  Revisit if one is added.
- **Telemetry of any kind.** The update check is the only thing kute sends anywhere, it
  is documented, and `update.check: false` turns it off.

---

## Sequencing

1. **`0.4.0-beta.1`** — beta-plan's four gates. Ships as an unmarked release, so it
   reaches every install path and actually soaks.
2. **During the soak** — §1 trust chain, §2 disclosure path, §5 platform smoke. None of
   these touch app behaviour, so they can land while beta is in the field.
3. **§4 rollback** once there are two schema-relevant versions to move between.
4. **`1.0.0`** — §3's prerelease marking and §6's mechanics land *in* the release
   commit, because §3 changes what every earlier tag means.
