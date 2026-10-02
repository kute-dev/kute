# 09 — Config, secrets & editors

Scope: `tasks/{configmapdata,secretdata,yamlview}`, `browse/{meta,edit}.go`, `internal/tui/metapanel`,
`components/textfield`, `kube/{edit,yaml}.go`, and the `secret-data`/`configmap-data`/`set-meta`
branches of `actions.Controller` and `kube/mutate.go` (`PatchSecretData`, `PatchConfigMapData`,
`metaPatchJSON`, the will-run string builders). Spec: §8a, §17a, §21a, §26a, §27a, §27b in
`docs/design/README.md`.

Verification: `go vet` and `go test` are clean for `configmapdata`, `secretdata`, `yamlview`,
`metapanel` and `textfield`. Every finding below was traced to the `file:line` given. Findings marked
**(proved)** were reproduced with a throwaway `go test -overlay` test. These tests used realistic
`tea.KeyPressMsg{Code, Mod, Text}` values; for example, shift+r's `String()` is `"R"`. No repo file
was changed. Anything not observed at runtime is marked **unconfirmed** or "by code".

Already covered elsewhere, so not repeated: 04 H1 (`kubectl edit`, from `kube/edit.go:27`, carries
no `--context`/`--kubeconfig`, so `E` edits whatever the kubeconfig's current-context points at),
03 H1 (the event bridge's trailing-edge debounce, which also delays these screens' reloads).

## Summary

Most of the contract is in place:

- **Confirm, execute, refresh, show result, remain on screen** is implemented on 26a, 27a and 27b.
- **Failure restore** brings back the attempted buffer.
- **`RoutePaste`** is wired correctly everywhere. Resolvers use pointer receivers and point at the
  focused buffer. 8a's search re-matches inside the closure.
- **The will-run line and result messages never contain a Secret value.** No logging, diag or
  crash-report path reads `TaskScope.SecretValue`.
- **8a's `managedFields`** come from a live Get, as `CLAUDE.md` says.

The defects are at the data boundary: values that don't survive a round-trip through the editors,
and Secret material that 8a never sees as data.

1. **Opening a Secret value and pressing `↵` corrupts it.** `textfield` flattens newlines and drops
   control/invalid bytes, so a PEM, SSH key or binary value is already "changed" before any keystroke.
   `↵ ↵` writes the mangled value, and outside PROD it does so with no confirm. **(proved)**
2. **Typing a capital `R` in any ConfigMap value buffer applies the edit and rollout-restarts every
   consumer.** It does not insert the letter. `R` replaced `ctrl+r` in 72af16b, but the buffers still
   match on it. **(proved)**
3. **8a's Secret masking can be bypassed.** Data keys that YAML quotes (`"true"`, `"1"`, `"no"`, …)
   render as raw base64. The `kubectl.kubernetes.io/last-applied-configuration` annotation shows a
   `kubectl apply`'d Secret's full data, `stringData` plaintext included. **(proved)**

Behind those, 26a's Service-join safety gate reads a Service cache it never checks is synced. 26a's
post-commit refresh reads a cache that hasn't seen the write yet. The 27a/27b conflict handling the
spec requires is absent.

## Findings

| ID | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `secretdata/update.go:177-186`, `model.go:109`; `textfield/textfield.go:141-154`, `:493-506`; same pattern `metapanel/metapanel.go:533-541`, `:126` | `↵` on a Secret row runs `SetValue(row.value)` through `sanitize`, which turns `\n`/`\t`/`\r` into spaces and drops `U+FFFD` and control runes. `changed()` compares the sanitized buffer with the raw original, so it returns true with zero keystrokes. A second `↵` patches the mangled value; non-PROD is `TierNone`, so there is no confirm. **(proved)** A PEM became `"-----BEGIN CERTIFICATE----- MIIB -----END CERTIFICATE----- "`, and binary `00 ff 10 61` became `61`, both reported as "updated". 26a annotations with newlines hit the same path. | Refuse in-place edit for values that don't round-trip (`sanitize(v) != v` or `!utf8.ValidString`): show "multi-line/binary — use E (kubectl edit)", or open a textarea as 27a does. Compute `changed()` against the sanitized original, or (better) store the original as the buffer's initial `Value()`. Add a test with a PEM and a binary value. |
| H2 | high | `configmapdata/update.go:268-274`, `:296-297`, `:320-321`; `verbs/verbs.go:544-547` | `RestartConfigMapConsumers.Key` is `"R"`, matched before the default insert in the single-line edit, the add row's value buffer and the multi-line buffer editor. Any capital R typed into a value (`Release`, `REDIS_URL`, a config file) commits immediately and runs `RolloutRestart` on every consumer. **(proved)** Typing `E` then `R` into `MODE` applied `"xE"` and restarted `Deployment/ns/web`. The add row's *key* buffer has a guard, which shows the collision was known for that one buffer only. | Use a chord again (`ctrl+r`, as §27a and `CLAUDE.md` still say) in every text-entry mode. Keep bare `R` only in navigation, if at all. Add a regression test that types `R` into each buffer. |
| H3 | high | `yamlview/secret.go:31`, `:44-50`; `yamlview/update.go:88-95` | 8a masks only lines matching `^  ([\w.\-/]+): `. `sigs.k8s.io/yaml` quotes keys like `"true"`, `"1"`, `"no"`, `"on"`, so those render as raw base64. The `last-applied-configuration` annotation is never treated as Secret material, so a `kubectl apply`'d Secret shows every value (plaintext if it used `stringData`), and `/` search matches it. **(proved)** The same annotation is shown and `y`-copyable in the 26a panel on a Secret (`metapanel.go:617-619`, by code). | Mask from the typed object rather than regex-parsing YAML: walk `data` keys from `corev1.Secret` (`ListRaw`), or match keys with optional quotes. For Secrets, replace `last-applied-configuration` with `•••• · contains secret data · N B` in both 8a and 26a, and exclude it from `y`. |
| M1 | medium | `metapanel/metapanel.go:368-420`, `:297`; `kube/cluster.go:1170-1184` | §26a's "joins render before you touch anything" gate reads `ListRaw(Service)`/`ListRaw(Pod)` with no `KindSynced` check. Service is lazy, and the first `ListRaw` starts it and returns an empty cache. Pressing `m` before anything has read Services finds no join, so a selector-matched label edit goes through `TierNone` with no warning and no y/N, detaching pods silently. By code; the race was not observed live. | Gate the join on `tui.KindsSynced(lister, ns, KindService, KindPod)`. If either is unsynced, treat every label as potentially joined (TierInline with "service joins still loading"), or recompute once the caches settle. |
| M2 | medium | `metapanel/metapanel.go:782-855` (refetch at `:838`); `browse/update.go:307-321` | On success, `HandleResult` rebuilds the panel synchronously from the informer cache, usually before the watch event for the patch has arrived. The grid then shows the old value under an "updated k=v" result, and a following `↵` pre-fills the stale value. No `ResourceChangedMsg` path ever rebuilds `pendingMeta`. 27a/27b avoid this only because they reload on their kind's change events. By code. | Rebuild the open panel on `ResourceChangedMsg` for its kind (preserving section, cursor and message), or overlay the committed value until the cache's `resourceVersion` advances. |
| M3 | medium | `configmapdata/model.go:130-143` (`textarea.SetValue`) | The bubbles textarea normalizes the value: tabs become 4 spaces, and CRLF becomes doubled `\n`. **(proved)** `"a\r\nb\r\n"` became `"a\n\nb\n\n"` with `changed()==true` before any edit. Any edit to one line of a tabbed (Makefile/TSV/nginx) or CRLF value rewrites the whole value. | Detect `\t`/`\r` and refuse the buffer editor with a pointer to `E`, or diff only the edited lines. At minimum, warn in the will-run strip that whitespace will be normalized. |
| M4 | medium | `secretdata/update.go:286-306`; `configmapdata/update.go:375-384` | `a` with a key that already exists silently overwrites it. The merge patch sets the key, and the result says "added PASSWORD". **(proved)** The original Secret value is gone, with no confirm in non-PROD. 26a checks `metaKeyExists` for the same case. | If the key exists, either jump to the edit flow or escalate to TierInline with "overwrites existing key". Word the result "updated". |
| M5 | medium | `configmapdata/model.go:18-26`; `kube/mutate.go:1297-1353` | §27a: "the patch carries the observed resourceVersion; a concurrent change surfaces the diff/rebase/discard banner", plus "dry-run-first apply". Neither exists. Patches are unconditional merge patches with no dry-run. The package doc records this as a deliberate gap pending 17a, but the spec still promises it. A concurrent writer's change to the same key is lost silently. | Either implement the precondition (`metadata.resourceVersion` in the merge patch gives a 409 on conflict) and a minimal "changed underneath — r reload / ctrl-o overwrite" banner, or amend §27a/§27b to say conflict handling is deferred. |
| M6 | medium | `actions/controller.go:567-578`; `configmapdata/update.go:437-474` | For `R`/ctrl-r, the patch succeeds and then one restart fails. The joined error turns the whole commit into a failure: the editor re-opens with the "attempted" value, nothing reloads, and the error hides that the patch landed and the other consumers restarted. A retry re-restarts everything. | Return a structured partial result (patched=true, restarted=[…], failed=[…]). On partial failure, show "updated KEY · restarted 1/2 · deploy/x failed: …", refresh, and don't restore the buffer. |
| M7 | medium | `verbs/verbs.go:521-547`; `configmapdata/update.go:318`, `keys.go:63`; `secretdata/update.go:170-192`; spec §26a/§27a/§27b; `CLAUDE.md` Architecture | Key vocabulary has drifted three ways. Spec and `CLAUDE.md` say 27a apply-buffer is `ctrl-o`, restart is `ctrl-r`, and removal is `ctrl-d` (26a/27b). Code uses `alt+a`, `R` and `D`. In secretdata/configmapdata, `ctrl+d` is half-page down. `metapanel`'s own package doc still says "Removal is 'ctrl+d'" (`metapanel.go:27`). | Pick one set (H2 forces a chord for restart) and update the code, §26a/§27a/§27b, `CLAUDE.md` and the package docs together. |
| L1 | low | `kube/mutate.go:1244`, `:1315`, `:1352`, `:1366` | Patch bodies are built with Go `%q`, which is not JSON. `\x1b`, `\a`, invalid UTF-8 and non-printable astral runes (for example the tag characters in flag emoji, `U+E0001`) give `\x..`/`\U........` escapes that the API server rejects as invalid JSON. **(proved)** The textfield's sanitize hides most of these. The textarea path and annotations don't. | Build bodies with `json.Marshal(map[string]any{…})`. |
| L2 | low | `configmapdata/model.go:288`, `:302-309` | `binaryData` is ignored. A binaryData-only ConfigMap shows "no keys", SIZE excludes it, and adding a key that collides with a binaryData key bounces off the server. | List binaryData rows as read-only `binary · N B`. |
| L3 | low | `configmapdata/model.go:333`, `:399-424` | The consumer scan misses `projected` volume sources, CronJob/Job templates, and bare Pods, so the strip under-reports and `R` under-restarts. | Scan `v.Projected.Sources[].ConfigMap`. List Job/CronJob/Pod consumers as "not restartable". |
| L4 | low | `secretdata/view.go:206`, `:208` | The edit row's inline hint says `M reveal` / `M re-mask`, but the key is `ctrl-x`. `M` types a letter. | Render `ctrl-x`, ideally from a verb entry. |
| L5 | low | `secretdata/view.go:345-347` | An unchanged edit shows "no changes — ↵ has nothing to apply". §27b says the band is omitted entirely in that state. | Return `""` when `!changed()`. |
| L6 | low | `secretdata/update.go:197-205`; `configmapdata/update.go:239-247` | Cancelling a PROD y/N (`n`/`esc`) discards the typed value (the buffer was nilled at commit) and leaves a stale `pendingCommit`. 26a reverts to navigation too, but 27b retypes a secret from scratch. | Clear `pendingCommit` on cancel. Consider restoring the buffer (still masked/revealed per its state). |
| L7 | low | `yamlview/update.go:49-52`, `:70-81`; `load.go:42` | Every `ResourceChangedMsg` for the kind (any object, not just this one) issues a live `GetManagedFields` GET. Viewing one Pod's YAML in a busy cluster sends up to ~4 GETs/s. Loads have no epoch, so an older response can overwrite a newer one. | Re-fetch managedFields only when the cached `resourceVersion` changed, or only when unfolded. Add a load epoch like 27a/27b. |
| L8 | low | `metapanel/metapanel.go:279-300`, `:838`; `browse/meta.go:23` | `Open`/`HandleResult` run `ListRaw` (object, Services, Pods) synchronously inside `Update`. These are cache reads, but for a dynamic kind or a first-touch typed kind this starts informers on the update loop. It also reads `Session.ClusterContext()` outside a `tea.Cmd`. | Build the target in a `tea.Cmd` with a loading row, as 27a/27b do. |
| L9 | low | `kube/mutate.go:1327-1329`, `:1361-1367` | The will-run lines wrap the JSON in single quotes without escaping. A value containing `'` produces a command that doesn't paste. | Shell-quote the `-p` argument (`'\''`). |
| L10 | low | `secretdata/keys.go:343-366`; `configmapdata/keys.go:36-68`; `metapanel.go:917-934` | The ADD/EDIT/BUFFER keybars hand-write `↵`, `ctrl-x`, `ctrl-v`, `alt+a`, `D`, `y` hints instead of rendering them from verb entries. The H2/M7 drift happened in exactly these places. | Register the editor-mode keys as verbs (or a small editor-key table) and render from it. |

## Details (high / medium)

### H1. A no-op `↵ ↵` rewrites a multi-line or binary Secret value

`secretdata/update.go:177-186` opens the edit row with `valueInput.SetValue(row.value)`.
`textfield.SetValue` (`textfield.go:141-154`) runs `sanitize`, which does the following
(`:493-506`):

- maps `\r`, `\n` and `\t` to spaces;
- drops `U+FFFD`, which every invalid UTF-8 byte becomes under `[]rune(value)`;
- drops all `Cc` control runes.

`editKeyState.changed()` (`model.go:109`) compares the buffer with the *raw* original, so it is
true immediately. The will-run strip even shows a patch command with nothing typed. The second
`↵` calls `commitEdit` → `PatchSecretData` with the flattened value. Outside PROD that is
`TierNone`, so the write is immediate.

TLS Secrets, SSH keys, kubeconfigs and `.dockerconfigjson` (if pretty-printed) are all multi-line.
JKS and PKCS#12 keystores are binary. "Open it to look, press enter to leave" is the obvious
gesture, and it breaks the workload on the next restart. The proof test showed the PEM
newline-flattened and `00 ff 10 61` reduced to `61`, with the result "updated tls.crt". 26a's row
edit (`metapanel.go:533-541`, `changed()` at `:126`) has the same shape for multi-line annotation
values, though annotations are less often multi-line.

Fix: gate the in-place editors on round-trip safety (`string(sanitize([]rune(v))) == v &&
utf8.ValidString(v)`). For unsafe values, offer only the textarea (multi-line text) or nothing
(binary: "binary · N B — edit with E"). Separately, make "unchanged" mean "buffer equals what the
buffer was initialized to", so an untouched buffer can never commit.

### H2. A capital `R` in a ConfigMap value applies and restarts every consumer

72af16b ("remap shortcuts") changed `case "ctrl+r"` to `case verbs.RestartConfigMapConsumers.Key`
(`"R"`) in all three text-entry handlers (`update.go:268`, `:296`, `:320`). They run before the
`default:` branch that inserts text. The add row guards the *key* buffer (`:269-274`) but not
the value buffer.

In the single-line edit, typing any value with an uppercase R commits whatever was typed so far
and calls `RolloutRestart` on every consumer. Outside PROD there is no confirm. In the buffer
editor, the first `R` after any change does the same. **Proved:** typing `E` then `R` applied
`MODE=xE` and restarted `Deployment/ns/web`. In the multi-line editor, `c` then `R` applied and
restarted.

The existing test `TestCtrlRChainsRolloutRestartOfEveryConsumer` sends `KeyPressMsg{Text:"R"}`, so
it pins the bug as behaviour. The spec (§27a, §36b's "not bare r") and `CLAUDE.md` both say
`ctrl-r`.

### H3. 8a leaks Secret data through quoted keys and `last-applied-configuration`

`parseSecretData` regex-matches `^  ([\w.\-/]+): (.*)$` under `data:`. go-yaml quotes keys that would
otherwise parse as bool, null or number (`"true"`, `"yes"`, `"no"`, `"on"`, `"1"`, `"0"` …). Those
lines don't match, aren't masked, and render as raw base64, which §21a says never happens. They
are also searchable with `/`.

Separately, Secrets created with `kubectl apply` carry
`kubectl.kubernetes.io/last-applied-configuration` containing the full manifest: base64 `data`, or
plaintext `stringData`. The informer transform strips only managedFields (`kube/transform.go:22`),
`GetYAML` marshals the annotation verbatim, and 8a renders it in the metadata block. 26a also
shows it on a Secret, as a read-only "controller-managed" row whose `y` copies `key=value`.

**Proved** with a marshaled Secret: three quoted keys and the annotation all rendered their secret
content. This is the most common way plaintext ends up on screen in a non-GitOps cluster.

### M1. 26a's join warning depends on whether the Service cache happens to be warm

`serviceLabelJoins` (`metapanel.go:368`) calls `ListRaw(KindService, ns)` and, if any Service
matches, `ListRaw(KindPod, ns)`. Only Namespace, Pod and Node start eagerly. Service starts lazily
inside `ListRaw` and returns an empty list on first read (`cluster.go:1172-1184`).

`Open` and `HandleResult` never consult `KindSynced`/`KindError`. So the first `m` in a session
(say, on Deployments, before any screen has listed Services) computes no joins. Editing
`app=nva-worker` on that object is then `TierNone`: no warning, no y/N, immediate. That is exactly
the blast-radius case §26a escalates. Under Forbidden on Services, the same silent "no join"
result applies.

Treat "can't know" as "joined": escalate every label edit to TierInline with a "service joins
unknown (cache loading / forbidden)" note until the caches report settled.

### M2. 26a refreshes from a cache that hasn't seen the write

`HandleResult` → `buildTarget` → `findObject` reads the informer cache in the same `Update` that
received the `ResultMsg`. The PATCH response and the watch event race, and the watch usually
loses, so the rebuilt grid shows the pre-commit value under "updated env=staging".

Browse never routes `ResourceChangedMsg` to `pendingMeta` (`browse/update.go`; only paste, keys,
result and cancel reach it), so the panel stays stale until it is closed and reopened. That breaks
the spec's "the panel always shows what actually landed", and a follow-up `↵` edits from the stale
value. 27a/27b have the same immediate read, but they recover on their kind's change event.

### M3. The ConfigMap buffer editor normalizes whitespace across the whole value

`newMultilineEditState` loads the value with `textarea.SetValue`. Bubbles' textarea expands tabs
and splits `\r\n` into two line breaks. **Proved:** `"a\tb\nc"` became `"a    b\nc"`, and
`"a\r\nb\r\n"` became `"a\n\nb\n\n"`, both reported as `changed()` with no edit.

Any single-character edit to such a value rewrites every tab and line ending. That corrupts a
Makefile, a TSV, or a Windows-authored config. Because of H2, a stray `R` makes this happen without
the user meaning to apply.

### M4. "Add" silently overwrites an existing key

`commitAdd` in both screens never checks `indexOf…Key(m.keys, key)`. The merge patch replaces the
existing value, and the result line says "added". **Proved** on a Secret: `PASSWORD=orig` became
`x`, with the result "added PASSWORD". For a Secret this destroys the only copy of a credential.
26a already computes `metaKeyExists` for its `--overwrite` flag; 27a/27b need the same, plus
escalation.

### M5. Spec-promised conflict handling and dry-run are absent

§27a requires a resourceVersion-carrying patch, a diff/rebase/discard banner, and a "dry-run-first
apply". §27b inherits the confirm/refresh contract. `PatchSecretData`/`PatchConfigMapData`
(`mutate.go:1297-1353`) send unconditional merge patches with no `DryRun`. The configmapdata
package doc says this is deliberate pending 17a. That is reasonable, but the spec and `CLAUDE.md`'s
"decided contract" text present it as done.

A merge patch only touches one key, so the lost-update window is "two people editing the same
key". That is small but real, and it is silent. Cheapest fix: include `"metadata":{"resourceVersion":
"<observed>"}` in the patch (the server returns 409 on mismatch) and render the 409 as "changed
since you opened it — reload".

### M6. A partial ctrl-r failure is reported as a total failure

`controller.go:567-578` patches and then restarts each consumer, joining the restart errors. The
patch succeeded, but `msg.Err != nil`, so `handleResult` takes the failure branch:

- it re-opens the editor with the "attempted" value, which is already applied;
- it skips the reload;
- it shows only the joined error.

The user can't tell that the value landed and N−1 workloads restarted. Pressing `R` again patches
(a no-op) and restarts all of them a second time.

### M7. Key vocabulary drift between spec, `CLAUDE.md` and code

| Action | Spec / `CLAUDE.md` | Code |
|---|---|---|
| 27a buffer apply | `ctrl-o` | `alt+a` (`update.go:318`, keybar `keys.go:63`, package doc `model.go:120`) |
| 27a restart chain | `ctrl-r` | `R` (H2) |
| 26a/27b removal | `ctrl-d` | `D` (`verbs.go:521`, `metapanel.go:544`); `ctrl+d` is half-page in 27a/27b |

72af16b changed the code and 4 lines of the design README, but §26a, §27a and §27b still describe
the old keys, and so do `CLAUDE.md`'s Architecture paragraph and `metapanel`'s package doc. Whichever
set wins, H2 needs restart to be a chord in text-entry modes.

## Test gaps

- **No round-trip test for editor buffers.** Nothing opens a multi-line, tabbed, CRLF or binary
  value and asserts that `changed()` is false and that `↵`/apply without edits writes nothing
  (H1, M3).
- **No negative test that typing literal text never commits.** Each text-entry mode needs one:
  type `R`, `D`, `a`, `y`, `x` into every buffer and assert no `Begin`. The existing ctrl-r test
  sends bare `Text:"R"` and so pins H2.
- **8a masking tests use only unquoted keys** (`secret_test.go`). Add fixtures with keys
  `"true"`/`"1"` and a `last-applied-configuration` annotation, and assert the base64 or plaintext
  never appears in `rendered()` or the view (H3).
- **metapanel has no unsynced-cache test.** Use a lister whose `KindSynced(Service)` is false and
  whose Service list is empty, and assert that a label edit still escalates (M1).
- **No test that the 26a panel reflects a cache that updates after `ResultMsg`** (M2). The fakes
  mutate the object in place, so the immediate refetch always looks right.
- **Add-over-existing-key** (M4) and **partial restart failure** (M6) are untested.
- **`kube` has no test that patch bodies are valid JSON** for control or astral runes (L1); this
  is a candidate for a fuzz target over `secretDataPatchJSON`/`configMapDataPatchJSON`/`metaPatchJSON`.
- **No e2e coverage of 27a/27b apply against a real API server.** Such a test would catch L1, M5's
  409 path and M2's ordering.
- **Truecolor goldens:** configmapdata/secretdata have plain goldens only (`golden_test.go`), and
  neither pins Theme colors in both themes as browse does.

## Quick wins

1. Make restart a chord (`ctrl+r`) in all three configmapdata text modes (H2). This is a one-line
   verb change plus three case labels and a test update.
2. Refuse `↵` in-place edit on values where `sanitize(v) != v`, and make "unchanged" compare
   against the buffer's initial value (H1). This also covers 26a.
3. In 8a, mask Secret data by key list from the typed object, and redact
   `last-applied-configuration` on Secrets, in 8a and 26a (H3).
4. Check key existence in `commitAdd` and word the result "updated" (M4).
5. Build patch JSON with `json.Marshal` (L1).
6. Fix the `M reveal`/`M re-mask` hint text and omit the unchanged-edit band (L4, L5).
7. Gate 26a's join computation on `KindsSynced(Service, Pod)` (M1).
