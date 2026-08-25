# kute — performance findings

Investigations into throughput/allocation problems that were reproduced against a real
cluster (not just asserted in a unit test), what was ruled out, and what's still open. Add
to this file rather than letting a diagnosis live only in a commit message or a chat log —
the next person hitting the same red CI job shouldn't have to re-derive it.

---

## `TestHighRateLogsStayBoundedAndResponsive` never clears its 45s budget (fixed, 2026-08-25)

**Status:** fixed, in two parts. The log viewer now (1) batch-drains its stream channel, so a
burst costs one render instead of one render per line, and (2) lays out only the rows the
viewport shows, so a render costs the visible ~30 rows instead of the whole 5,000-entry
buffer. Batching alone cleared the budget; the second part is what made the cost *bounded*
rather than merely amortized — before it, a frame cost 19.8 ms whatever triggered it, logs or
not. Before either fix, reproduced locally (kind on Docker Desktop) and on GitHub Actions'
nightly `soak` job — same failure, same assertion, both environments. It was never a flaky
test or a "this machine is slow" issue.

### The test

`test/e2e/high_rate_logs_test.go`'s `TestHighRateLogsStayBoundedAndResponsive`
(`e2e_soak`-tagged, nightly-only): opens `podlogs` on a fixture pod, execs a producer that
writes 12,000 lines to the container's stdout almost instantly, then waits up to `Settle`
(45s, `harness.go:87`) for the batch's own final-line marker and the "older log lines
dropped" strip (i.e. the 5,000-entry buffer filling and starting to evict) to appear on
screen — twice, to also check allocations don't degrade on the second saturating batch.

### What actually happened

```
high_rate_logs_test.go:51: first high-rate log batch: 12001 writes, 8 input fences, max 5.630208ms, burst 121.244334ms
high_rate_logs_test.go:54: never saw all of [".../batch-a-final" "older log lines dropped"] within 45s
```

The exec producer finishes writing all 12,001 lines in ~120–550ms (confirmed on both
machines — this side is fine). 45 seconds later, the viewer had rendered only ~4,000–4,300
of them: kute's own 1-second-tick throughput counter (`▶ live · 45 new lines/s`) read
**~45 lines/sec** in both the local run and the CI run. Draining 12,000 lines inside a 45s
budget needs ~267/sec — roughly 6x short.

### Why it wasn't the machine

The same ~45/sec order of magnitude showed up on a local laptop (Docker Desktop) and a
GitHub Actions runner — two architecturally different hosts. Getting the same answer in both
places pointed at a fixed per-message cost in the code path itself, not raw hardware speed.

**One inference in the original write-up was wrong, and it's kept here because it's an easy
one to repeat:** the cross-machine agreement was read as evidence *against* host-CPU-bound
rendering work ("those numbers would diverge more"). It was host-CPU-bound rendering work —
19.8 ms of `lipgloss.Wrap` per frame. Two hosts within an order of magnitude of each other on
single-thread throughput produce the same order of magnitude, so that observation never
discriminated between the two theories. What the agreement really ruled out was *variance* —
a scheduling hiccup, a slow disk, a noisy runner — not CPU cost. The conclusion happened to
be right; the reasoning wasn't.

### Root cause 1: one log line = one full render cycle, no batching

`internal/tui/tasks/podlogs/update.go`, the `logLineMsg` case as it stood:

```go
case logLineMsg:
    ...
    m.appendEntry(msg.entry)
    return m, m.nextStreamCmd()
```

Every streamed line was its own `tea.Msg`: `stream.go`'s `waitForStream` read exactly one
value off `m.streamCh` (buffered, capacity 128) and returned it as a `tea.Cmd` result;
`Update` appended it, Bubble Tea rendered the whole screen, and only then did
`nextStreamCmd()` go read the next one. Nothing drained whatever else was already queued in
the channel before triggering that render. The producer (`kube.ScanLogLines`, a plain
`bufio.Scanner` loop with no throttling) and the channel itself can clearly deliver far
faster than 45/s — the producer's `emit` blocks on channel send once the 128-slot buffer
fills, so the real pace was set entirely by how fast `Update`+`View` drained it, one message
at a time.

`appendEntry`/`LogBuffer.Append` were already O(1) amortized (the quadratic
append-to-nil-on-trim path was fixed in `6d6ce81` — see the comments in `model.go` around
`Append`), so the per-line *buffer* cost was never the story here. The cost was the rest of
the Bubble Tea render cycle, paid once per single line instead of once per batch of lines.

### Root cause 2: a render cost the whole buffer, twice, to show 30 rows

The measured throughput identifies the second cause almost exactly. A frame at a saturated
buffer took **19.8 ms** (benchmark below); 1/0.0198 = 50 frames/sec, against the ~45
lines/sec the viewer's own counter reported. The per-frame cost *was* the line rate.

Where it went: `Body` called `visualRows` over **every** entry — `lipgloss.Wrap` per line,
into a `[]visualLogRow` sized to the whole buffer — and then sliced out the ~30 rows the
viewport shows. `Strips` → `toolbarLine` → `visibleSeverityCounts` did the identical
whole-buffer pass a second time for the "N WRN · N ERR in view" counts. `lastErrIndex`
scanned the buffer, `maxVerticalOffset` (via `clampOffsets`) laid it out again on every
scroll key, and `filterStripLine` added a third pass plus a full copy while `/` was open.

Two consequences batching does not touch:

- **The cost is per frame, whatever causes the frame.** The root shell fans
  `kube.ResourceChangedMsg` down to the active task (`internal/tui/model.go`), and spinner
  ticks, the 1s rate tick and every key press are messages too. On a busy cluster the log
  viewer burned ~10,000 `lipgloss.Wrap` calls per watch event with no logs arriving at all,
  and every `j` press paid the same.
- **The cost scaled with `MaxEntries`, not with the screen.** Raising the 5,000-entry buffer
  would have made every frame proportionally slower.

### The fix, part 1: batch the drain

`waitForStream` still blocks for the first message, but once that message is a log line it
now takes every other line already queued behind it in one non-blocking drain and returns
them as a single `logBatchMsg` (`stream.go`). `Update`'s new `logBatchMsg` case feeds each
entry through the same `appendEntry` as before — so the incremental scroll/eviction
bookkeeping and the per-line rate counter are unchanged — and Bubble Tea renders once for
the whole burst.

Three details are load-bearing:

- **The drain is non-blocking** (`select` with a `default`), so a quiet stream still delivers
  each line the moment it arrives. Batching only happens when there is a backlog to batch.
- **It stops at anything that isn't a live log line.** A stream error/close, or a line from a
  superseded stream, ends the batch and rides along as its `tail` for `Update` to handle
  immediately after the lines in front of it — ordering against the surrounding messages is
  exactly what it was.
- **Batches are capped** at `maxLogBatch` (= the channel's own 128-slot capacity). Without
  the cap, a producer faster than the viewer keeps the drain loop fed and the command
  goroutine never yields a frame.

Measured on kind/Docker Desktop, same fixture, same 12,000-line batches: the test failed its
45s budget on the *first* batch before the change (~4,000 lines rendered), and passed both
batches with the whole test — launch, two bursts, teardown assertions — taking 14.8s.

### The fix, part 2: lay out only the visible window

`Model` now carries a **row index** — `rowCounts`, one int per buffered entry saying how many
physical rows it occupies (0 when the `/` filter hides it), plus running `totalRows`,
`matchCount` and `lastErr` totals. Nothing new is computed to maintain it: `appendEntry`
already worked out the added and evicted row counts for its incremental scroll bookkeeping
and threw them away.

- `maxVerticalOffset` is `totalRows - viewport`, O(1) — the full pass leaves `clampOffsets`
  and therefore every scroll key.
- `visibleWindow` walks `rowCounts` (integer adds, no allocation, from whichever end is
  closer) to find where the viewport starts, then lays out **only** the entries it shows.
  `Body`, the toolbar's severity counts and `ctrl-y`'s copy all go through it.
- `jumpSeverity` finds its target's row offset in the index instead of laying the buffer out.

Two things keep it honest:

- **One choke point.** `syncLayout` sits at the top of `clampOffsets`, which every key,
  resize, filter edit and append path already runs through, so a future toggle cannot forget
  to invalidate. What invalidates is a `layoutKey` of `{width, wrap, timestamps, filter}` —
  and only those, since `HorizontalOffset` applies solely when wrap is off, where an entry is
  one row whatever the offset. Every trigger is a human-rate event, so the one remaining
  whole-buffer pass costs what the old code paid per frame.
- **The render path never mutates it** (the repo's render-purity invariant). It checks
  `layoutValid()` and falls back to the old whole-buffer pass when the index doesn't describe
  the buffer — which in the app never happens, but keeps a hand-populated test model
  rendering correctly instead of rendering a wrong screen.

Measured by `BenchmarkPodLogsRender` (M5, 120×36, entries wrapping to two rows):

| | before | after |
|---|---|---|
| render, 500 entries | 2.22 ms · 104,260 allocs | 0.41 ms · 7,390 allocs |
| render, 5,000 entries (saturated) | 19.79 ms · 1,048,285 allocs | 0.41 ms · 7,448 allocs |
| render, 5,000 entries, scrolled | 19.44 ms · 1,048,260 allocs | 0.41 ms · 7,425 allocs |
| append one line to a full buffer | 11.8 µs · 218 allocs | 10.1 µs · 109 allocs |

The point isn't only the 48x: it's that 500 and 5,000 now cost the same, so the screen's cost
is its viewport rather than its buffer. Every one of the package's golden fixtures — plain
and truecolor, both themes — still matches byte-for-byte, which is what says this is a
refactor of *how* the window is found and not a change to what's drawn.

End to end, on the same kind/Docker Desktop setup and the same 12,000-line batches:

| | before both fixes | batching only | both |
|---|---|---|---|
| `TestHighRateLogsStayBoundedAndResponsive` | fails the 45s budget | 14.8s | **11.0s** |
| allocations per saturating batch | — | ~2.2 GiB | **~50 MiB** |

The soak's `allocationBudget` was re-baselined against that (`test/e2e/high_rate_logs_test.go`)
— a 4 GiB ceiling on a run that now allocates 50 MiB is an assertion that can no longer
fail — and the measured figure is logged on a passing run so the next drift is visible.

### What was ruled out

- **Producer/exec speed** — sub-second on both machines, not the bottleneck.
- **Kubelet/containerd log delivery pacing** — both environments run kind (same
  containerd/kubelet log-tailing stack) regardless of host OS, so this could not be ruled out
  by the cross-machine argument alone. It's moot now that the two render fixes account for
  the whole gap (19.8 ms/frame ≈ the 45 lines/sec observed, and both benchmark and soak
  moved as predicted), but if high-rate logs ever look slow again, time-stamp lines at the
  point they're read off the HTTP stream (before they reach `podlogs` at all) rather than
  re-deriving this.
- **`LogBuffer.Append`'s own allocation cost** — already fixed to O(1) amortized in `6d6ce81`;
  the heap/alloc assertions later in the test (which this failure never reached) are what
  that fix targets, not the throughput-within-45s assertion.
- **Flaky timing / this being a one-off** — reproduced twice, on two different environments,
  with the same order-of-magnitude number both times, and re-reproduced on demand by
  stashing the fix.

### If this area comes up again

- `BenchmarkPodLogsRender` is the cheap reproduction: it needs no cluster, and a regression to
  whole-buffer layout shows up immediately as `entries=5000` diverging from `entries=500`.
  `TestRowIndexStaysInStepThroughRealInteractions` is what catches the index quietly going
  stale — the render fallback would otherwise absorb the bug and just make the screen slow
  again, with every other test still passing.
- 0.41 ms and ~7,400 allocations a frame is now Chrome v2 itself (header, toolbar, keybar,
  Lip Gloss styling of the visible rows), not the log buffer. That's the next thing to look
  at if this screen is ever too slow again, and it isn't specific to `podlogs` — the same
  frame cost applies to every screen on the shared `Frame`.
- `browse` is the other screen that redraws on every watch event; `BenchmarkBrowseRender`
  exists for the same reason and is worth checking before assuming a slowdown is in the data
  layer.
