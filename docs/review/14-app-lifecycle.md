# 14 — App lifecycle, diagnostics, state & updates

Scope: `cmd/kute`, `internal/app/{app.go (run/openDiagnostics/configureKlog/event bridge), config.go, crash.go, e2e.go, update.go, session.go}`,
`internal/diag`, `internal/state`, `internal/config`, `internal/update`, `internal/tui/tasks/update` (28b),
`internal/tui/update.go` (28a chip), `docs/diagnostics.md`, `docs/verifying-releases.md`,
`.github/ISSUE_TEMPLATE/bug_report.yml`, `cliff.toml`, `scripts/release-notes.sh`, `.goreleaser.yaml`,
`website/install.sh`, `.github/workflows/*`. Lister-decorator forwarding (01) and connect/switch (02)
are excluded. The 28b value-receiver `Update` (13) is not repeated here.

## Summary

The crash path is well built. `crashCatcher` records and re-panics (`crash.go:98-105`). `liveState`
caches the snapshot on the update goroutine and a dying goroutine reads only a copy under `l.mu`.
`reportProgramCrash` correctly matches Bubble Tea v2.0.10's wrapped `ErrProgramKilled: ErrProgramPanic`
(`tea.go:1037`, `:1171`). klog goes to the sink at default verbosity, so no request headers or
bearer tokens reach the ring. Report and log files are `0600`. `state.Save` uses temp file + rename.
The update check is bounded (10 s timeout, process context, once per 24 h, silent offline). Release
signing is real and is checked by the release job itself.

The problems are in the files kute *persists*:

- **The config file fails open on PROD.** Any YAML or type error in `config.yaml` drops the whole
  file. `prodContexts` goes with it, so every context loses the type-the-name delete guard and nothing
  says so. (H1, proved)
- **`SetProd` writes back the whole in-memory `Config`.** It persists the `--no-update-check`
  override the code promises never to write, and it clobbers hand edits and other instances. (M1, proved)
- **State has no protection against a newer or concurrent writer.** An older binary wipes a newer
  state file on exit, and two kute windows lose each other's recents/per-context data. (M2 proved, M3)
- **Crashes off the update loop are only half covered.** An informer panic prints its footer onto a
  raw, alt-screen terminal. A panic in a kute-owned goroutine leaves no report at all. (M4)
- **Install detection misses Intel-Mac Homebrew casks.** Those users are told to run the curl
  installer, which overwrites Homebrew's symlink. (M5, proved)
- **The shell installer drops to checksum-only when the bundle is missing**, even with cosign
  installed and for releases known to be signed. (M6)

Counts: **1 high, 6 medium, 17 low.**

## Findings

| ID | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `config/config.go:57-67`; codified by `config_test.go:19` `TestLoadUnparsableFileYieldsZeroValue` | `loadFrom` returns `Config{}` on *any* unmarshal error. A plausible hand edit such as `prodContexts: prod-eu` (scalar instead of list) or `nodeShellImage: [x]` silently un-PRODs every context: delete becomes inline `y/N` and drain becomes the plain card. Nothing is shown or logged. **Proved** with an overlay test (`IsProd=false` for both inputs). | Fail closed and say so. On a parse error keep a `LoadErr`, log it to the diag sink, show a persistent header note ("config.yaml unreadable — treating every context as PROD"), and have `IsProd` return true while `LoadErr != nil`. At minimum, decode into a `yaml.Node` and salvage `prodContexts` independently of the other keys. |
| M1 | medium | `config/config.go:86-111`; `app/session.go:32-38`; `tui/context.go:254` | `SetProd` marshals the whole in-memory `Config` back to disk. (a) BuildSession's `--no-update-check` override is written as `update: check: false`, so one `P` press permanently disables update checks. **Proved**; this contradicts session.go:33's "never written back". (b) Startup-time contents overwrite any edit made since (by hand or by another kute instance). (c) Unknown keys and comments are dropped. (d) `os.WriteFile` truncates in place, which is not atomic. | Re-read the file at write time and modify only `prodContexts` on a `yaml.Node`, which keeps comments and unknown keys. Write with temp+rename. Keep per-invocation overrides out of `config.Config` entirely, e.g. a `Session.UpdateCheckDisabled` flag that `UpdateCheckEnabled` callers also consult. |
| M2 | medium | `state/state.go:115-122`, `:178-183`; `app/app.go:1725-1729` | A file with a newer `Version` loads as `zero()`. On exit, `Save` writes v3 over it, which destroys the newer binary's recents, perContext and update cache. **Proved**: a v4 file with recents and perContext came back as `{"version":3,"recentKinds":["Service"]}`. Running a dev build next to a release, or a downgrade, triggers it. | Remember `loadedNewer bool` (or the raw bytes) and skip `Save` in that case, or save to `state.v3.json`. Add a test that a newer file survives a load/save cycle byte-for-byte. |
| M3 | medium | `state/state.go:178-207`; `app/app.go:1725-1729` | Concurrent instances are last-writer-wins on the whole document. Window A (prod) and window B (staging) both load S0. Whichever exits last discards the other's `PerContext` entry, recents, `SeenVersions` (the chip re-nags) and `LastChecked`. Running one kute per context is a common setup. | Merge on save: re-load under an advisory lock (`flock` on `state.json.lock`), apply this session's deltas (touched PerContext keys, pushed recents, seen versions, max(LastChecked)), then rename. |
| M4 | medium | `app/crash.go:176-184`; `app/app.go:1669`, `:1696-1722`; `kube/{forward,cluster,probe,execauth,dynamic}.go` goroutines | (a) The informer `PanicHandlers` entry writes the report and footer, then `utilruntime` re-panics. The terminal is never restored: raw mode, alt screen and hidden cursor all stay. The footer lands in the alt-screen buffer and the Go traceback (LF-only) follows it. (b) kute's own goroutines (`forwardEvents`, `watchForwardManager`, the `cluster.Start` goroutine, the ambient update-check goroutine at `app.go:1720`, forward reconnect loops, health ping, probes, the execauth drain) have no `recover`/`HandleCrash`. A panic there leaves no report and a raw terminal. `docs/diagnostics.md`'s "Where panics are caught" table omits this row. | Give the panic handler a way to release the terminal before printing: store the `*tea.Program` once built and call `program.Kill()`/`ReleaseTerminal()` in the handler (behind a `sync.Once`). Wrap kute-owned goroutines in a small `diag.Go(sink, name, fn)` helper that records, releases the terminal and re-panics. Add the missing row to the doc table. |
| M5 | medium | `update/install.go:187-192`; `install_test.go:17-19` | Releases ship as a Homebrew **cask** (`.goreleaser.yaml` `homebrew_casks`). On Intel macOS the resolved binary is `/usr/local/Caskroom/kute/<v>/kute`, which matches none of `/Cellar/`, `/homebrew/` or `linuxbrew`. **Proved**: it is classified `curl`. 28b then tells the user to `curl … install.sh \| sh`, and `install.sh:110,207-210` `mv`s a plain binary over Homebrew's `/usr/local/bin/kute` symlink. Apple Silicon matches only because `/opt/homebrew/` happens to contain `/homebrew/`. The test table has no Caskroom case; its `/usr/local/homebrew/Cellar` row is a path Homebrew never uses. | Match `/Caskroom/` explicitly and add both real cask paths to the table. Per §28b, prefer `brew upgrade --cask kute-dev/tap/kute` over `brew install`. |
| M6 | medium | `website/install.sh:35-45` (and the same logic in `install.ps1`, by the doc); `docs/verifying-releases.md` "What the installers do for you" | When cosign *is* installed, any failure to fetch `<archive>.sigstore.json` (404 or network) prints a note and continues on checksum alone. The doc's own threat model ("anyone who could tamper with the archive could tamper with the manifest") covers deleting the bundle too. Removing one asset therefore turns the signature check off for exactly the users who opted into it, including for releases ≥ `v0.4.0-beta.1` that are known to be signed. | When cosign is present and the version is ≥ v0.4.0-beta.1 (or not a known pre-signing tag), treat a missing bundle as fatal. Keep the soft fallback only for the known unsigned versions, and document the cut-off as enforced rather than advisory. |
| L1 | low | `config/config.go:90-95`; `tui/context.go:240-254` | `SetProd` mutates `ProdContexts` before `save()`, and the caller discards the error. On a write failure the session shows the new PROD tag but it is not persisted. context.go's doc ("untouched on error") is false. The write is also synchronous file I/O on the update loop. | Mutate a copy and commit it only on a successful save. Surface failure as a palette flash, or at least log it to the sink. |
| L2 | low | `app/app.go:1725-1729` | After a panic, `run` still calls `SyncLocationToPerContext()` + `Save()`. If the persisted location or filter is what crashed (e.g. a restored kind or filter that panics browse), every launch crashes again, and the footer doesn't say to reset state. | Skip the location sync on `ErrProgramPanic`, or save the previous location. Mention `state.json` in the footer when the crash cause was `init`. |
| L3 | low | `app/app.go:1725`; Bubble Tea `tea.go:664` | State is saved only on `program.Run` return. SIGHUP (closing the terminal window) is not handled by Bubble Tea, so the process dies unsaved. The same happens on the M4 informer-crash path. | `signal.Notify` SIGHUP → `program.Quit()`, or save on meaningful transitions (context switch, update-check result). |
| L4 | low | `state/state.go:192-206` | Temp+rename with no `tmp.Sync()`. After a power loss the renamed file can be empty, which loads as `zero()`. | `tmp.Sync()` before `Close`. Optionally fsync the directory. |
| L5 | low | `diag/report.go:80-99` | The `os.TempDir()` fallback uses a predictable name (`kute-crash-<second>.log`) with `os.WriteFile`, which follows symlinks and truncates. On a shared `/tmp`, another user can pre-plant a symlink. This is not the traversal item settled in go-practices-review: it is a shared-directory TOCTOU, and only on the fallback. | Use `os.OpenFile(…, O_CREATE\|O_EXCL\|O_WRONLY, 0600)`, or `os.CreateTemp(dir, "kute-crash-*.log")`. |
| L6 | low | `app/crash.go:157-158`; `app/app.go:1749-1751`; `diag/report.go:172-174` | The ring and report hold `ConnState.Err` verbatim. For exec plugins that is the plugin's own stderr (kubelogin device codes, cloud CLI error text; whether any plugin prints token material is **unconfirmed**). They also hold absolute `--kubeconfig`/`--log-file` paths, which include the username. The bug form tells users to attach the file publicly. | Replace `$HOME` with `~` in rendered paths. Cap the plugin text, and scrub `token=`/`Bearer `/JWT-shaped substrings, before `Logf`. |
| L7 | low | `app/crash.go:192-201`; Bubble Tea `tea.go:719-726`, `:1294-1306` | For a `tea.Cmd` panic, Bubble Tea's `recoverFromPanic` runs on the Cmd goroutine: it shuts down, then prints the stack. `Run` returns on the main goroutine as soon as `shutdown`'s `Once` finishes, so kute's footer races the stack print and can end up above a long trace. **Unconfirmed** ordering. | After `ErrProgramPanic`, wait briefly (or until stderr is quiet) before printing the footer, or reprint the one-line "report: <path>" after the stack. |
| L8 | low | `tui/model.go:486-491`; `diag/report.go:30-31` | `Screen()` returns `reflect.TypeOf(task).String()`, which is `"*browse.Model"` (tasks are pointers). The docs and test stub say `"browse.Model"`. | `strings.TrimPrefix(…, "*")`, or fix the docs. |
| L9 | low | `app/crash.go:54-58`, `:143`; `crash_test.go:21` | `crashInspector` is satisfied only by a type assertion, and tests use `stubRoot`. If `tui.Model`'s accessors moved to pointer receivers, the snapshot would go silently empty. This is the same silent-seam class CLAUDE.md warns about for the lister decorators. | Add `var _ crashInspector = tui.Model{}`. |
| L10 | low | `tui/update.go:33-45`, `:63-69` | `UpdateChip`/`UpdateRightHints` render from the persisted `LatestVersion` without checking `Config.UpdateCheckEnabled()`. With `update.check: false` (or `--no-update-check`) a stale `↑ x.y.z` chip still shows, and `U` then opens "update checks are disabled". §state says the cache is "absent/inert when `update.check: false`". | Return `false` from `UpdateChip` when checks are disabled. |
| L11 | low | `app/update.go:263-289`; `tasks/update/model.go:102-110`; `update/release.go:122-137` | (a) A failure never advances `LastChecked`, so a proxied or rate-limited user GETs on every launch. That is bounded, not a storm. (b) Opening `U` before the first-of-day ambient check returns fires a second concurrent check. (c) `getJSON` decodes an unbounded body. | Record `LastAttempt` and back off (e.g. 1 h) on failure. Skip `Init`'s fetch while an ambient check is in flight (a `Session.UpdateChecking` flag). Wrap the body in `io.LimitReader` (1 MiB). |
| L12 | low | `update/browser.go:13-26` | `cmd.Start()` without `Wait`, so each `o` leaves a zombie until kute exits on Unix. On Windows, `cmd /c start "" url` re-parses `&`/`^` (GitHub `html_url`s don't contain them today). | `go cmd.Wait()`. On Windows use `rundll32 url.dll,FileProtocolHandler <url>`. |
| L13 | low | `tasks/update/view.go:185` | Changelog `Text` from the release asset is rendered verbatim. Control and escape sequences are not stripped, and the app never verifies `changelog.json`. A tampered asset could inject terminal escapes. | Strip C0/C1 controls (keep printable runes) when decoding `ChangelogEntry`. |
| L14 | low | `update/install.go:174-185` vs design §28b | §28b: "plain-binary installs get the release URL instead of a command", and the command shown is `brew upgrade`. Code: everything unclaimed gets the curl installer, including `go install` binaries in `$GOBIN`. Curl then installs a second `kute` in `/usr/local/bin` that may shadow it or be shadowed by it. | Detect `GOBIN`/`GOPATH/bin` (`go install …@latest`) and fall back to the release URL for unknown paths, or update the spec to record the decision. |
| L15 | low | `cmd/kute/main.go:24-47` | Flag validation gaps. A `--theme` typo is silently ignored. Positional args are ignored (`kute pods` does nothing). `--context`/`--kubeconfig`/`-n` together with `--demo` are silently dropped. The `--namespace-scoped` value isn't DNS-1123-validated (it surfaces later as Forbidden or empty). | Reject unknown `--theme` values and stray args. Warn on cluster flags used with `--demo`. Validate the namespace name with `validation.IsDNS1123Label`. |
| L16 | low | `config/config.go:42-48` vs `state/state.go:87-96` | State honours `XDG_STATE_HOME` but config ignores `XDG_CONFIG_HOME`. | Honour `XDG_CONFIG_HOME`, with a fallback read of the legacy path. |
| L17 | low | `.github/workflows/release.yml` | A tag publishes without a test gate (CI may be red on that commit). The channel and signature checks run *after* `goreleaser release`, so a bad release is already live when they fail. `scripts/release-notes.sh` (git-cliff + jq) is never exercised in `ci.yml`. | Add `needs:` on a test job, or require CI success on the tagged SHA. Consider publishing as draft → verify → un-draft. Run `release-notes.sh` against the last tag in `goreleaser-check`. |

The bug form (`bug_report.yml`) keeps the footer's field order (version, platform, terminal, context,
namespace, kind, screen). It omits the three conditional footer rows (connection, mode, log file),
which is harmless because they are extras. Its placeholder says `"demo"` for `--demo`, but the fake's
context is `demo-prod` (`fake/fixtures.go:39`). The footer's `mode` row already covers it.

## Details (high / medium)

### H1 — config parse failure silently disables PROD confirms

`config.loadFrom` returns `Config{}` on any `yaml.Unmarshal` error (`config.go:63-65`), and
`TestLoadUnparsableFileYieldsZeroValue` pins that as intended ("nothing is prod"). PROD status
exists only in this file, and it is the sole input to `verbs.TierFor(verb, isProd)`. Failing open
here weakens the destructive-action policy in exactly the case the user believes it is on.
Overlay proof:

```
"prodContexts: prod-eu\n"                              -> IsProd=false
"prodContexts: [prod-eu]\ntheme: dark\nnodeShellImage: [x]\n" -> IsProd=false
```

(yaml.v3 does accept `check: no`/`off` as a bool, so the common YAML 1.1 habit is *not* a trigger.
The type errors above are.) The fix is to fail closed with a visible note. "Every context is PROD
until you fix config.yaml" is annoying, but safe.

### M1 — `SetProd` persists the in-memory config wholesale

`BuildSession` sets `userConfig.Update.Check = &false` for `--no-update-check`
(`session.go:36-37`). That struct becomes `sess.Config`, and `toggleSelectedContextProd` calls
`sess.Config.SetProd` (`context.go:254`), which marshals the whole struct (`config.go:101-110`). The
overlay test shows the file afterwards:

```yaml
prodContexts:
    - x
update:
    check: false
```

The same write also replaces anything edited since startup (a second kute's `P`, a hand-added
`nodeShellImage`), and drops comments and unknown keys. The write is `os.WriteFile`, which
truncates in place.

### M2 / M3 — state writers don't coordinate

`loadFrom` maps `Version > CurrentVersion` to `zero()` "rather than risking a misread", which is
the right read-side choice. But `run` always saves on exit, so the newer file is replaced
(proof above). Separately, there is no merge or lock: each process writes the snapshot it loaded
plus its own deltas. Both fixes live in `state.Save`: refuse to downgrade, and merge under a lock.
The CLAUDE.md versioning invariant ("bumps the version and adds a migration") is met. These are
the gaps it doesn't cover.

### M4 — crashes outside the update loop

The `utilruntime.PanicHandlers` entry (`crash.go:178-182`) runs on the informer goroutine and
can't reach the program: `installPanicHandler` runs before `tea.NewProgram` (`app.go:1669` vs
`:1679`). It writes the footer to a terminal Bubble Tea still owns (raw mode, alt screen), and then
`HandleCrash` re-panics and the process exits without restoring the terminal. The usual recovery
(`reset`) clears the screen and the footer with it. The report file does survive. For kute's own
goroutines there is no handler at all. `grep` finds 15 `go` statements across `internal/app` and
`internal/kube` (outside tasks) with no recover. Example: `app.go:1720`'s
`go func() { program.Send(cmd()) }()` runs the release-feed HTTP + JSON decode outside Bubble Tea's
Cmd recover, because it is called directly rather than returned as a Cmd.

### M5 — Intel-Mac cask detected as curl

Overlay proof:

```
/usr/local/Caskroom/kute/0.9.0/kute  -> {Manager:curl Command:curl -fsSL https://kute.dev/install.sh | sh}
/opt/homebrew/Caskroom/kute/0.9.0/kute -> {Manager:homebrew ...}
```

`darwin/amd64` is a shipped target. Following 28b's advice replaces Homebrew's symlink in
`/usr/local/bin` with an unmanaged binary, and a later `brew upgrade` then conflicts.

### M6 — missing signature bundle downgrades the check

`install.sh:41-44`: when `curl … ${archive}.sigstore.json` fails for any reason, the script prints
"publishes no signature; verified checksum only" and installs. An attacker who can replace
`kute_*.tar.gz` and `checksums.txt` on the release (the scenario signing exists for) can also delete
the `.sigstore.json`. `docs/verifying-releases.md` documents the soft fallback for pre-signing
releases, and it can be enforced by version: anything ≥ `v0.4.0-beta.1` must carry a bundle.

## Test gaps

- No test that a newer-version state file survives a load/save cycle (M2), and nothing exercises
  two writers (M3).
- `TestLoadUnparsableFileYieldsZeroValue` asserts the fail-open behaviour (H1). Invert it once the
  fix lands, and add type-error cases, not just syntax errors.
- No test that `SetProd` leaves untouched keys/comments alone, or that a per-invocation override
  is not persisted (M1).
- `installPanicHandler` is untested: no test drives `utilruntime.HandleCrash` with `ReallyCrash=false`
  and asserts a report plus a restored `PanicHandlers` slice.
- No compile-time `crashInspector` assertion against `tui.Model` (L9). Every crash test uses `stubRoot`.
- No end-to-end check that a real `tea.Program` with a panicking `tea.Cmd` produces
  `ErrProgramPanic` → report. The test at `crash_test.go:199` feeds the error in by hand. A
  `tea.WithInput(nil)`/`WithOutput(io.Discard)` program test would pin the Bubble Tea contract
  across upgrades.
- `install_test.go` has no `Caskroom` rows and includes an unrealistic `/usr/local/homebrew/Cellar` row (M5).
- `UpdateChip` has no test with `update.check: false` (L10).
- `scripts/release-notes.sh` and `changelog.json`'s shape are never run in CI (L17).

## Quick wins

1. `var _ crashInspector = tui.Model{}` in `crash.go` (L9).
2. Add a `/Caskroom/` match plus two test rows (M5).
3. `if s.Config … !UpdateCheckEnabled() { return "", false }` in `UpdateChip` (L10).
4. `tmp.Sync()` in `saveTo` (L4). Use `O_EXCL` for the TempDir crash report (L5).
5. Skip `Save` when the loaded state was a newer version (M2, ~5 lines).
6. Log `State.Save()` and `SetProd` errors to the sink instead of `_ =` (`app.go:1728`, `context.go:254`).
7. Trim the `*` in `Screen()` (L8). `go cmd.Wait()` in `OpenBrowser` (L12).
8. Add the "kute-owned goroutine" row to `docs/diagnostics.md`'s panic table until M4 is fixed.
