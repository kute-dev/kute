# E2E coverage implementation plan

This plan extends the existing real-cluster E2E suite from startup and core
workflow coverage into lifecycle, recovery, and long-session behavior. It is a
companion to [`e2e-testing.md`](e2e-testing.md), which remains the source for
how the harness works and the rules that keep tests deterministic.

## Goals

- Exercise failures and transitions that occur after a successful startup.
- Detect leaked goroutines, listeners, watches, timers, and informer factories.
- Verify cached data remains honest and useful during connectivity failures.
- Prove recovery without restarting kute.
- Cover repeated navigation and resource churn rather than only first use.
- Keep deterministic, bounded scenarios in the PR suite and move expensive
  stress, soak, and PTY scenarios to nightly jobs.

## Current baseline

The suite is already strong in these areas:

- Real kubeconfig to informer to registry to rendered-frame integration.
- Lazy informer startup, `CountLive`, and Helm informer isolation.
- Static RBAC denial and namespace-scoped failure handling.
- Core Pod navigation, logs, rollout restart, editors, CRDs, Flux, Argo, and
  basic port-forward traffic.
- Initial responsiveness and heap use with 5,000 Pods.

The main gap is lifecycle coverage after those initial paths succeed.

## Phase 1: fault-injection infrastructure

Add a controllable TLS reverse proxy to the E2E harness. Launch kute against a
temporary kubeconfig whose API endpoint is the proxy, while the proxy forwards
to the existing kind control plane.

The proxy should support:

- Delaying requests by method, path, resource, and verb.
- Failing the next N LIST or WATCH requests.
- Closing active WATCH and streaming response bodies.
- Returning Kubernetes Status responses for 401, 403, 410, 429, and 503.
- Allowing `/livez` while LIST or WATCH requests fail.
- Switching the entire API endpoint between healthy and unavailable.
- Recording method, URL, start time, completion time, response, and
  cancellation for every request.
- Reporting active request counts and request totals by resource and verb.

Add these harness capabilities alongside the proxy:

- Retain the `*tea.Program` so tests can send explicit messages.
- `Resize(width, height)` using `tea.WindowSizeMsg`.
- Bracketed-paste input using the terminal's `ESC[200~...ESC[201~` encoding.
- A helper that waits until a local TCP port refuses connections.
- Heap and goroutine snapshots with stack classification for informers,
  Bubble Tea commands, streams, and forwards.
- A temporary merged kubeconfig builder for multi-context tests.

Acceptance criteria:

- Existing E2E tests can run through the proxy without behavioral changes.
- Proxy controls are synchronized by explicit request fences, not sleeps.
- Tests can assert request pacing and cancellation without depending on log
  text.

## Phase 2: PR lifecycle coverage

These scenarios should run on every PR because they protect core correctness
and have bounded runtimes.

Implementation status: complete. The suite now pins the pushed-task context
switch contract to **return to browse**, with the destination context's saved
namespace, kind, and filter restored. Failed switches rebuild the original
context before reporting the error. The implementation also closes all
background forwards on application exit, paces real `/livez` probes from the
advertised exponential retry schedule, keeps connection health degraded until
resource watches resume, and terminates log restart waits when the Pod is
deleted.

### Forward lifecycle

Add `test/e2e/forward_lifecycle_test.go`.

Scenarios:

1. Start a real forward and verify traffic, as the existing test does.
2. Quit kute and require the local listener to close promptly.
3. Open the Forwards screen, stop one forward, and require its row and listener
   to disappear.
4. Start a Service or Deployment forward, delete its resolved Pod, and verify
   the same local port reconnects to a replacement Pod.
5. Stop a forward while it is reconnecting and verify no later retry revives
   the listener.

This should expose and then guard shutdown cleanup for background-rooted
forwards in `internal/kube/forward.go` and `internal/app/app.go`.

### Connectivity outage and recovery

Add `test/e2e/network_test.go`.

Scenarios:

1. Launch through the proxy and wait for populated Pod rows.
2. Make the API endpoint unavailable.
3. Verify cached rows remain visible, the disconnected state appears, writes
   are disabled, and keyboard input remains responsive.
4. Verify `/livez` requests follow the advertised retry schedule rather than a
   fixed high-frequency loop.
5. Mutate an object directly while disconnected.
6. Restore the proxy and verify the connection recovers and the mutation
   reaches the frame without restarting kute.

The request timestamps must test actual retry pacing, not only the displayed
countdown.

### Watch close, relist, and recovery

Add `test/e2e/watch_recovery_test.go` and a wire-level companion in
`internal/kube/e2e_resilience_test.go`.

Cover one informer of each shape:

- Typed Pod or ConfigMap informer.
- Dynamic Widget informer.
- Filtered Helm release informer.

For each shape:

1. Complete initial cache sync.
2. Close the active WATCH.
3. Return a 410 Gone on the next WATCH request.
4. Require a new LIST followed by a new WATCH.
5. Change an object during the gap.
6. Verify cached rows never become a false empty and the final value reaches
   the UI.
7. Verify exactly one active informer remains for the kind and scope.

The connection must not be considered fully recovered solely because
`/livez` succeeds while resource watches remain broken.

### Context switch and cancellation

Add `test/e2e/context_switch_test.go`.

Build a temporary kubeconfig with two contexts backed by distinct identities
or distinguishable proxy endpoints.

Scenarios:

1. Switch contexts from browse and verify namespace, kind, filter, and rows
   restore for the target context.
2. Open Pod detail, YAML, logs, and an editor, then switch contexts while a
   read or stream is deliberately delayed.
3. Verify the old request is cancelled, its marker never reaches the new
   frame, and no old-context error changes the new context's health.
4. Verify the old endpoint receives no new LIST or WATCH requests after the
   switch.
5. Exercise a failed context switch and confirm the current screen and session
   location remain on the original context.

Before implementation, decide the destination contract for a switch from a
pushed task: return to browse, rebuild an equivalent task, or reject the
switch. The E2E test should pin that decision.

### External resource churn

Add `test/e2e/churn_test.go`.

Scenarios:

1. Create an object externally while its list is open and verify it appears.
2. Patch a projected field and verify the row changes.
3. Delete the selected row and verify selection clamps to a valid neighbor.
4. Delete the last object and verify an empty state appears only after cache
   settlement.
5. Recreate the same name with a different UID and verify identity-dependent
   navigation remains correct.
6. Delete a Pod while Pod detail is open and verify the explicit gone state.

### Log stream lifecycle

Add `test/e2e/log_lifecycle_test.go`.

Scenarios:

1. Stream a unique marker from a temporary Pod.
2. Delete the Pod externally.
3. Require a terminal "pod deleted" or "stream ended" state rather than an
   indefinite restart wait.
4. Verify `esc`, context switch, and application quit close the `follow=true`
   request promptly.
5. Disconnect and restore the API while logs are open and verify the screen
   remains navigable.

### Namespace-scoped switching

Add a second readable fixture namespace and extend `scoped_test.go` plus
`internal/kube/e2e_scoped_test.go`.

Scenarios:

1. Switch namespace A to B to A through the real palette.
2. Verify each namespace shows only its own rows.
3. Update A while B is active and verify A is current when revisited.
4. Verify two successful namespace reads create two distinct cache keys.
5. Verify revisiting A reuses its cache rather than creating another informer.
6. Verify an explicit all-namespaces read starts only the explicit global
   cache and is never silently downgraded.

### Terminal input and resize

Add `test/e2e/terminal_test.go`.

Resize through 140x36, 80x24, 40x10, and back while browse, a palette, an
editor, and logs are active. Verify no panic or event-loop stall and that focus
and selection survive.

Bracket-paste into:

- Browse filter, including characters that are global shortcuts.
- Goto query.
- PROD type-name confirmation.
- Port input.
- Multiline ConfigMap editor.

Paste with no active buffer and verify no command is executed accidentally.

## Phase 3: mutation and screen breadth

Implementation status: complete. The PR suite now performs every write
against a disposable object or a shared fixture with an explicit restore.
The API proxy can inject 409 Conflict responses for deterministic editor
recovery coverage, the resources editor now follows the context-preserving
refresh-and-remain contract, and Helm is pinned in `mise.toml` because
rollback is the one Helm path that intentionally shells out to the CLI.

Expand real write coverage in risk order.

1. Commit non-prod and PROD deletes against disposable resources and verify
   API deletion, watch refresh, and selection behavior.
2. Cover force-delete escalation without targeting shared fixtures.
3. Cover ConfigMap multiline editing, add/remove key, and apply-plus-restart.
4. Cover rewriting an existing Secret value and failed optimistic-concurrency
   recovery.
5. Cover cordon/uncordon, using a disposable worker and restoring it in test
   cleanup.
6. Cover Job rerun and CronJob run-now/schedule editing with dedicated
   fixtures.
7. Cover Helm rollback using the existing revision Secrets.
8. Cover scale, set-image, metadata, and resource editors with disposable
   workloads.

Every mutation test should:

- Capture the prior server value.
- Require a new value or resource version after the action.
- Verify the resulting frame, not only the API object.
- Restore shared fixture state in cleanup or use a disposable object.
- Verify the screen's remain-or-navigate contract.

## Phase 4: event-storm and long-session suite

Implementation status: complete. The nightly-only `e2e_soak` suite uses
bounded, environment-overridable iteration counts and records input-fence,
heap, allocation, goroutine, stream, forward, informer-watch, and proxy
request budgets. The application bridge coalesces informer notifications on
one per-kind timer so event bursts cannot enqueue one screen load per watch
event or lose the final state behind a full notification channel. Saturated
log-buffer appends reuse their backing storage instead of allocating a new
5,000-entry slice for every line. Namespace-scoped caches deliberately use
unbounded retention for the lifetime of one cluster context: revisits reuse
the cache, while a context switch or application shutdown releases the whole
set. The fan-out test enforces that linear policy rather than treating it as
an accidental baseline.

Add an `e2e_soak` build tag and a nightly job. The first version should run for
10 to 20 minutes and use bounded iterations so failures are reproducible.

### Event storm

Add `test/e2e/storm_test.go`.

1. Patch a Widget hundreds of times, ending with a unique final value.
2. Generate rapid Pod metadata and status changes while Pod detail is open.
3. Create and update several hundred Event objects while Events and Timeline
   screens are active.
4. Require prompt input fences throughout the burst.
5. Require the final state to win and remain stable after the storm.
6. Assert bounded API request and goroutine growth after settling.

This should detect one-load-per-event amplification in detail screens and
timer accumulation in debounced paths.

### Repeated workflow soak

Record a settled baseline, then repeat:

- Open and close logs, events, timeline, YAML, and detail screens.
- Switch among at least ten resource kinds.
- Open and close all palette scopes.
- Start, restart, and stop a forward.
- Switch contexts.
- Switch namespace-scoped caches.

After pending ticks and timeouts settle, force GC and compare against the
baseline.

Use budgeted deltas rather than exact counts:

- Heap returns near baseline after transient buffers are released.
- Informer goroutines match the active retained cache policy.
- No stream, port-forward, or old-context request remains active.
- Input fences remain within a fixed responsiveness budget.

### High-rate logs

Stream more than 20,000 lines into the log viewer.

Verify:

- The bounded buffer drops old entries and advances its dropped counter.
- Heap plateaus.
- Input remains responsive.
- Allocation and CPU behavior do not degrade sharply after the buffer reaches
  its maximum size.

### Namespace fan-out

Visit 20 to 50 namespaces in scoped mode and measure factory, informer, watch,
goroutine, and heap growth.

Before setting a strict pass budget, decide the intended cache policy:

- Unbounded retention.
- Bounded LRU retention.
- Stop-on-namespace-departure.

The test should enforce the chosen policy rather than accidentally blessing
the current behavior.

Decision: **unbounded retention for the active cluster context**. Each
namespace/kind pair actually read retains its informer until context switch or
shutdown. This preserves watch-current data when the user returns to a
namespace and avoids LIST/WATCH churn during incident navigation. The soak
test therefore requires one active ConfigMap watch per visited namespace,
requires a revisit to create no new LIST or WATCH, and applies linear
per-namespace heap and goroutine budgets.

## Phase 5: authentication and PTY coverage

Implementation status: complete. The nightly-only `e2e_auth` suite drives a
real client-go exec Authenticator through a proxy mode that forwards the
client's bearer token to the apiserver, and separately injects a direct 401.
It pins cached-row preservation, the offline write gate, paused plugin/probe
execution, explicit retry, and watch recovery. The `e2e_pty` suite builds the
shipping binary, runs it under a kernel PTY, exercises clean and non-zero
`kubectl exec` exits, proves redraw/input recovery, and compares the terminal
mode before launch and after a quit queued across an active handoff.

These scenarios belong in nightly jobs because they require extra process and
terminal infrastructure.

### Authentication expiry

Add `test/e2e/auth_test.go` with a temporary exec credential plugin.

1. Return a valid short-lived token on the first invocation.
2. Fail later invocations with recognizable stderr.
3. Verify the UI enters the unauthenticated state, preserves cached rows,
   disables writes, and stops periodic plugin execution.
4. Make the plugin healthy and press retry.
5. Verify watches and live updates resume.

Also cover a direct server-side 401 separately from an exec plugin failure.

### PTY subprocess handoff

Add an `e2e_pty` suite that runs the built `kute` binary under a real PTY.

1. Navigate to exec and enter a shell.
2. Print a unique marker and exit.
3. Verify kute redraws and remains usable.
4. Repeat with a non-zero subprocess exit.
5. Quit while the subprocess is active and verify terminal mode restoration.

The same harness can later cover `kubectl edit` and node debug handoffs.

## Test-quality corrections

Tighten existing tests while adding the new infrastructure:

- Flux and Argo mutation tests must capture the previous annotation or
  operation and require a new value, so reused clusters cannot satisfy the
  predicate with stale state.
- Make the open-palette discovery test deterministic by delaying discovery or
  fencing the relevant request before opening the palette.
- Make confirmed-delete tests operate on disposable resources rather than
  cancelling every confirmation.
- Keep negative assertions windowed with `Never`; do not replace them with
  one-shot frame checks.
- Continue verifying server mutations through the API and UI refresh through
  frames as separate assertions.

## CI layout

### Pull requests

Run the existing kind suite plus:

- Forward shutdown and stop.
- One outage and recovery scenario.
- One typed-informer watch relist scenario.
- Context switch cancellation.
- External create, update, and delete churn.
- Two-namespace scoped switching.
- Runtime resize and bracketed paste.

Keep the PR suite deterministic and below the existing 15-minute test timeout.

### Nightly

Run:

- Both supported Kubernetes versions.
- Dynamic and Helm watch recovery variants.
- Authentication expiry.
- Event storm and repeated-workflow soak.
- High-rate logs.
- Namespace fan-out.
- PTY exec.

### Scale

Retain the existing 5,000-Pod startup assertions and add post-navigation heap
and goroutine deltas. At scale, also verify an event burst converges to the
final state without starting breadth-first informers.

## Documentation corrections

Update [`e2e-testing.md`](e2e-testing.md) when implementation begins:

- Replace the claim that `kinds_test.go` has one subtest per resource kind
  with an explicit list of covered kinds.
- Correct the forbidden-kind description: the real-server tests use the
  forbidden signal while `KindError` remains reserved for retryable/stalled
  cache failures.
- Keep the documented default viewport synchronized with `harness.go`.
- Document the new proxy, soak, and PTY tags and their local commands.

## Implementation order

1. Proxy and harness controls.
2. Forward shutdown test and cleanup fix.
3. Outage, watch recovery, and retry pacing tests.
4. Context switch and stale-read cancellation tests.
5. Resource churn, log deletion, scoped namespace, resize, and paste tests.
6. Mutation breadth.
7. Soak metrics and nightly workflow.
8. Authentication and PTY suites.

Each new test should be demonstrated to fail without its corresponding fix or
guard. A test that passes both before and after a lifecycle change does not
prove the intended behavior.
