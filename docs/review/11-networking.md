# 11 — Networking: routes, forwards, certs

Scope: `tasks/{routetable,forwardpicker,certchain}`, `browse/{routes,forwards,services,certchain,certmanager}.go`,
`internal/kube/forward.go`, `internal/resources/{backend,certmanager}.go`, `kube/fake/forward.go`,
the forward wiring in `internal/app/app.go`, and `test/e2e/{forward_lifecycle,gateway,network,certchain}_test.go`.
Design: §13a/§13c/§13d, §23a/§23b, §35a/§35b.

## Summary

The forward lifecycle is mostly sound. Forwards are a registry kind (`forwardAwareLister`). The
header chip is the only ambient surface, and a failing forward changes only its colour
(`chrome.go:115-131`), with no modal or banner. Sessions are rooted in `context.Background()`, so a
context switch cannot cancel them. `run()` defers `StopAll()` for quit (`app.go:1676-1678`), and an
e2e test proves the listener closes. The go-practices orphan fix holds for the window it targeted,
between `Dial` and publishing the tunnel (`forward.go:466-470`).

That fix has a gap. The real `spdyDialer.Dial` does no I/O; it only builds a dialer. Its network
dial happens inside `Tunnel.Run`. `spdyTunnel.Close` is a no-op until `Run` has set `stopCh`, so a
`Stop`, `Restart` or pod-reconcile that lands between publish and `Run` is lost. The tunnel then
listens until the process exits (M1, **proved** at the tunnel level).

The most serious defect is in the picker. **Service forwards dial the Service's `port` on the
backing pod instead of the resolved `targetPort`.** Any Service whose port differs from its
container port (`80 → http/8080`, which is the common case) forwards to a port nothing listens on
(H1, **proved**). The e2e fixture hides this because its `api` Service uses 8080 on both sides.

The routing table and cert screens have a cluster of correctness gaps. All of them make false health
claims:
- Expiry math truncates toward zero, so a cert that expired up to 24 h ago reads as `0d` yellow (M3, **proved**).
- A missing `issuerRef.kind` is treated as ClusterIssuer, but cert-manager's default is Issuer (M5).
- Cross-namespace `backendRefs` and `certificateRefs` are resolved in the route's own namespace, and ReferenceGrant is not read at all (M6).
- TLS Secret, issuer and chain reads have no `KindSynced`/`KindError` gate. A user who cannot list Secrets therefore sees red "secret not found" on every TLS host (M7).

Reading one cert's expiry starts the cluster-wide, full-payload Secret informer (M8).

Counts: **1 high, 9 medium, 12 low.**

Already raised elsewhere and not re-counted:
- certchain matching `kube.ConnState` instead of `ConnStateMsg` (07 M2).
- Bare-Kind registration of `Gateway` and `Certificate`, where Istio `Gateway` and ACK `Certificate` collide (03 H3).
- Ingress BACKENDS projection cost and `context.Background()` (03 L7).
- Debounce starvation in the event bridge, which also delays `ReconcilePodTargets` (03 H1).

## Findings

| # | Sev | Location | Issue | Suggestion |
|---|---|---|---|---|
| H1 | high | `kube/forward.go:75-79`; `forwardpicker/update.go:195`; `forwardpicker/view.go:179` | For a Service, `ForwardablePorts` offers `spec.ports[].port`, and `startSelected` passes it unchanged as `remotePort` to `Dial(pod, …)`. A pod port-forward needs the **container** port. **(proved)** For Service `web` (port 80 → `targetPort: http`/8080), the manager dialed `api-0` on port **80**. The will-run line also says `kubectl port-forward pod/api-0 8080:80`, so the documentation repeats the bug. | Carry `TargetPort` in `PortOption`. When dialing, resolve it against the resolved pod: a numeric value is used as-is, and a named one maps to the matching `containerPort` name. Re-resolve it after every reconnect re-resolution in `run()`, because a named port can map to different numbers on different pods. Render the will-run line from the resolved number, or as `svc/<name> 8080:80`, which kubectl translates itself. Add e2e coverage on the existing `web` Service (80 → http). |
| M1 | medium | `kube/forward.go:204-231`, `:470-481` | `spdyTunnel.Close()` returns early when `stopCh == nil`. `run()` publishes `e.tunnel`, unlocks, and only then calls `tunnel.Run`. A `Stop`, `Restart` or `reconcilePodTarget` that closes the tunnel in that gap is lost: `Run` then creates a fresh `stopCh` that nobody will close. **(proved)** Calling `Close()` and then `Run()` leaves `stopCh` set and open. The result is a listener that outlives `x stop` and holds the port, so the next forward on it loops in Reconnecting. | Add a `closed` flag set by `Close` under `t.mu`. Have `Run` return immediately if it is set, or create `stopCh` in `Dial`. Add a tunnel-level regression test. The existing `TestForwardManagerRestartDoesNotOrphanTheReplacedTunnel` uses a fake tunnel whose `Close` works before `Run`. |
| M2 | medium | `kube/forward.go:552`; `:140-147` | `ReconcilePodTargets` skips `KindPod` targets. The seam's own doc says an SPDY session can stay open after its Pod is deleted. A forward to a deleted pod therefore stays `● active` (purple chip) indefinitely, which is the 13c/13d "failing forward" signal not firing. Not observed live (**unconfirmed**); the e2e covers only Service targets. | Reconcile Pod targets too: close the tunnel when the pod is gone. The loop then shows Reconnecting with "not found", which is the honest state. Add an e2e test that deletes a directly-forwarded pod. |
| M3 | medium | `resources/certmanager.go:179`; `routetable/load.go:216` | `days := int(x.Hours()/24)` truncates toward zero, so `notAfter` in the past 24 h gives `days == 0`. **(proved)** 12 h after expiry, `certExpiryCell` returns `"0d"`/Warn, and the Ingress/Gateway TLS cell says `expires in 0d` in yellow. Both show an expired cert as about to expire. | Test `!now.Before(notAfter)` (or `d < 0`) before bucketing, and use `math.Floor` for the day count. Share one helper between the two copies, which the comment already says use "the same thresholds". |
| M4 | medium | `resources/certmanager.go:142-153` | The glyph is escalated only for `expiresClass == StatusWarn`. A cert with `Ready=True` and `notAfter` already past keeps a green `●` and counts as ready. EXPIRES says "expired" in red, but the row glyph and strip say healthy. This is exactly what you see when cert-manager is down and nobody flips Ready. | Escalate on `StatusFail` too: show the fail glyph, and consider demoting `Status` to Fail. |
| M5 | medium | `certchain/load.go:358-370` | An empty `issuerRef.kind` maps to `ClusterIssuer`. cert-manager defaults it to **Issuer** in the Certificate's namespace. Any `issuerRef.group` other than `cert-manager.io` (external issuers such as AWS PCA or Google CAS) is also read as a ClusterIssuer. Both cases render `clusterissuer/<name> · missing`, and `↵` jumps to a nonexistent object. It also starts a ClusterIssuer informer for nothing. | Default an empty kind to Issuer. When the group is set and isn't `cert-manager.io`, render the ref as `<kind>/<name>` with neutral status and no read. Add a fixture with `kind` omitted. |
| M6 | medium | `routetable/load.go:341-358`, `:304-305`, `:544-545`; `resources/crd.go:203-226` | The routing table ignores `backendRefs[].namespace/kind/group` and `certificateRefs[].namespace`. It resolves everything in the route's or Gateway's own namespace and never reads ReferenceGrant or the route's `ResolvedRefs` condition. The effects:<br>• A permitted cross-namespace backend shows red `✕ not found`.<br>• A cross-namespace backend that ReferenceGrant does **not** permit can show green, if a same-named Service exists locally.<br>• A non-Service backendRef is looked up as a Service. | Resolve each ref in its own namespace (default: the route's). Treat non-core/non-Service kinds as neutral and unresolved. Surface `status.parents[].conditions[type=ResolvedRefs]` (e.g. `RefNotPermitted`) on the row instead of inferring. Reading ReferenceGrant itself is optional once ResolvedRefs is shown. |
| M7 | medium | `routetable/load.go:191-225`; `certchain/load.go:36-38`, `:44-56`, `:341-385`; `routetable/update.go:155-183` | Cert and ref reads have no `KindSynced`/`KindError` gate:<br>• `resolveCertExpiry` returns `secret not found`/Fail on an empty or Forbidden Secret cache. Many users can't list Secrets, so the red is permanent.<br>• certchain reports `secret … missing` and `issuer … missing` the same way.<br>• certchain discards errors from the CertificateRequest, Order and Challenge reads. With no `KindError` check, a Forbidden or still-filling cache silently truncates the chain, and the screen shows no failure card. That is the "settled ≠ empty" invariant.<br>`backendDeniedNote` covers only Service and Pod. | Gate each ref on `tui.KindsSynced`/`KindsError` for the namespace actually read. Render `–` with "secrets: permission denied" (or a loading state) instead of a red "not found". In certchain, run the same gate over the chain kinds before `buildFailure` and show `KindError` when it is non-nil. |
| M8 | medium | `routetable/load.go:195`; `certchain/load.go:348` | Reading one TLS Secret's `tls.crt` calls `ListRaw(KindSecret, ns)`. By default that starts the **cluster-wide** Secret informer with full `data` (no transform strips it), including every Helm release payload: 8.19 MB on the reference cluster according to CLAUDE.md. Opening any TLS Ingress, TLS Gateway or certchain pays that cost. | Prefer `Certificate.status.notAfter` where cert-manager owns the Secret. Otherwise add a narrow seam: a named-Secret GET (a documented one-shot, added to CLAUDE.md's list) or a field-selected per-name informer, the same way Helm got its own filtered informer. |
| M9 | medium | `routetable/load.go:94-115`; `resources/projections.go:565-581` | `ing.Spec.DefaultBackend` is ignored everywhere, and `Backend.Resource` paths are dropped. A defaultBackend-only Ingress (or one with only resource backends) gets BACKENDS `–` and a routing table in the **empty state**, a false "no routes" claim. | Emit a `* (default)` row for `DefaultBackend` and count it in BACKENDS. Render resource backends as neutral `<kind>/<name>` rows. |
| L1 | low | `routetable/load.go:63-110` | TLS host coverage uses exact string match. A `*.example.com` TLS block doesn't cover rule host `a.example.com`, so the row shows `http://` and no TLS cell. | Match single-label wildcards. |
| L2 | low | `routetable/load.go:537-539`; `routetable/update.go:289` | `loadGateway` rewrites an empty listener hostname to `"*"`, so the `gw/<name>` fallback in `selectedListenerRouteFilter` is dead and `↵` on a wildcard listener filters on `*`. The unit test builds `listenerRow{hostname: ""}` directly, which bypasses the loader. | Keep the raw hostname in `listenerRow` and substitute `*` only in the view. Or check for `"*"` in the filter. |
| L3 | low | `routetable/load.go:131` | A rule with no host yields `url = "http:///path"` for `y`. | Omit the URL, or use a placeholder host and say so. |
| L4 | low | `routetable/load.go:388-425` | `routeMatchText` reads only `matches[0]`. It returns `*` for header-only matches, which drops the header clause, and for GRPCRoute `method` matches. A multi-match rule shows one match, and a header-gated rule looks like match-all. | Render every match (or `+N`). Fall through to headers when there's no path, and render a GRPC `service/method`. |
| L5 | low | `routetable/load.go:456-492`; `resources/crd.go:203-226` | Only the first `status.parents` entry with an Accepted condition is used. There's no `observedGeneration` check against `metadata.generation`, so a stale status reads as current. `parentRef.kind` isn't checked: for a GAMMA `Service` parent, `p` jumps to a "Gateway" that doesn't exist. | Aggregate across parents (`2/2 accepted`). Mark the status stale when generations differ. Gate `p` on kind Gateway. |
| L6 | low | `resources/backend.go:73-110`; `routetable/load.go:162-185` | Readiness counts pods whose containers are all ready rather than the pod `Ready` condition, so readiness gates are ignored and terminating pods still count as ready. An Ingress port (number or name) that the Service doesn't declare still resolves green, falling back to showing the bare name. The design says "ready endpoints". | Use `PodReady` and skip `DeletionTimestamp`, or read EndpointSlices. Flag a port the Service doesn't declare as `▲ no such port`. |
| L7 | low | `kube/forward.go:301-313` vs `:258-267` | `firstRunningPod` accepts a terminating pod (phase is still Running during grace), but `ForwardPodAvailable` rejects it. After a pod delete, the reconnect can re-resolve to the same dying pod and get closed again on the next Pod event, which lengthens recovery by up to the grace period. When no pod is Running it falls back to `Items[0]` (a Pending or Failed pod). | Skip pods with `DeletionTimestamp` and prefer Ready pods. Return an error rather than an unrunnable pod. |
| L8 | low | `kube/forward.go:539-601`; `app/app.go:1853-1856` | Every debounced Pod event issues a live `Pods.Get` per Service/Deployment forward. That read isn't among CLAUDE.md's sanctioned one-shot reads, and it scales with pod churn × forwards. The events come only from the **current** context's Pod informer, so forwards started under a previous context are never reconciled. | Filter on the event's namespace or pod name when the bridge can supply it, or rate-limit per forward. Document the GET as a sanctioned read. Accept, or note, that forwards from another context rely on `Run` failing. |
| L9 | low | `forwardpicker/model.go:150-171`; `kube/forward.go:410` | Port collisions:<br>• `pickLocalPort` probes only `127.0.0.1` and never consults the manager. A Reconnecting forward holds no listener during backoff, so its port is offered again.<br>• `Start` accepts a duplicate `LocalPort`, and the loser loops in Reconnecting forever.<br>• IPv6: client-go binds both `127.0.0.1` and `::1`, but `::1` is never probed. If another process holds `[::1]:port`, the copied `http://localhost:port` can reach that process instead (**unconfirmed**). | Exclude the manager's `LocalPort`s in `pickLocalPort` and reject duplicates in `Start`. Probe `[::1]` as well, or copy `http://127.0.0.1:port`. |
| L10 | low | `resources/projections.go:776-785`; `browse` (no Forward tick) | `retry N · next in Ns`, UPTIME and `idle 12m` are computed at projection time. The manager notifies only on state changes, so the countdown stays frozen until the retry fires. CronJobs get a 1 s tick for exactly this reason. | Add the same UI tick while `m.kind == KindForward` and any row is Reconnecting (or always, at a coarse rate, for UPTIME). |
| L11 | low | `certchain/load.go:36-38` | `gone` ("no longer exists") is returned without checking `KindSynced(Certificate, ns)`. A reload during a context or namespace re-sync, or a scoped-mode cache that isn't filled yet, reads as deleted. | Gate on `KindsSynced`/`KindsError` like routetable's `applyLoaded`. |
| L12 | low | `certchain/update.go:59-75` | `reloadsOn` matches Order/Challenge only once they are already in the chain. A freshly created Order or Challenge triggers a reload only if its parent also changes. That is usual in practice (**unconfirmed**) but not guaranteed. | Always reload on the four chain kinds. All are namespace-scoped, cache-local reads. |

## Details

### H1 — Service forwards target the wrong port

`ForwardablePorts` (`forward.go:75-79`) builds `PortOption{Port: p.Port}` from `spec.ports`. The
picker calls `m.manager.Start(…, row.localPort, row.Port, …)` (`update.go:195`). `run()` then calls
`dialer.Dial(ns, pod, localPort, remotePort)`, which is a pod-subresource port-forward, so the port
has to be the container's. kubectl's `port-forward svc/x 8080:80` translates 80 into the target
port through the Service. kute skips that translation.

Proof: an overlay test fed a Service `web` with `port: 80, targetPort: http` and a recording
dialer. The log showed `dialed pod api-0 remote port(s) [80]` and the line
`will run  kubectl port-forward pod/api-0 8080:80 -n default`. Deployment targets are unaffected,
because they offer container ports.

The e2e test `TestServiceForwardRebindsToReplacementAndStopCancelsRetry` uses Service `api`
(8080 → `http`/8080), so it can't see the bug. `40-routing.yaml` already has `web` (80 → http),
which is the fixture a regression test needs.

### M1 — `Close` before `Run` is silently dropped

```go
func (t *spdyTunnel) Close() {
	t.mu.Lock(); defer t.mu.Unlock()
	if t.stopCh == nil { return }   // ← not running yet: close is forgotten
```

`run()` (`:470-481`) publishes the tunnel under `m.mu`, unlocks, calls `notify()`, and then calls
`tunnel.Run`. Any closer that reads `entry.tunnel` in that gap calls a no-op `Close()`:
- `Stop` (`:604-620`)
- `StopAll`
- `Restart` (`:640-663`)
- `reconcilePodTarget` (`:590-600`), which runs on its own goroutine for every Pod event

`Run` then creates and owns a fresh `stopCh`. `Stop` has already deleted the entry and cancelled
the ctx, but `Run` watches neither, so the local listener stays bound until kute exits.

The go-practices fix put a `ctx.Err()` check after `Dial`. For the real dialer, `Dial` is pure
construction (`forward.go:188-201`), and all the network I/O happens in `Run`. The window the fix
guards is real, but it is the narrow one. The overlay proof (`Close()` then `Run()` gives
`stopCh set=true closed=false`) confirms the tunnel semantics. The race itself is a
microsecond-scale window, traced by code rather than reproduced end to end.

### M3/M4 — certificate expiry

`int(-0.5)` is `0`. In both `certExpiryCell` and `resolveCertExpiry`, `case days < 0` therefore
fires only from 24 h after expiry. In the first day after expiry, the window in which an outage is
being diagnosed, both say `0d`/`expires in 0d` in yellow. Proof: `certExpiryCell(now-12h, now)`
returns `"0d"`, warn.

Separately, `projectCertificate` keeps the green `●` and `Status=OK` for a `Ready=True` cert whose
`notAfter` has passed. The strip then counts it ready.

### M5 — `issuerRef.kind` default

cert-manager's `ObjectReference.Kind` defaults to `Issuer`. `resolveIssuerRef` inverts that default:

```go
kind := kube.KindClusterIssuer
if issuerKindStr == string(kube.KindIssuer) { kind = kube.KindIssuer; issuerNamespace = namespace }
```

All fixtures (fake and e2e `57-certmanager-objects.yaml`) set `kind` explicitly, so this path is
untested.

### M6 — Gateway API cross-namespace refs

`routeRowsFromRoute` reads only `name`, `port` and `weight` from each backendRef, then calls
`ResolveServiceBackend(ctx, lister, ns, ref.name)` with the **route's** namespace. Gateway API
allows `backendRefs[].namespace` when a ReferenceGrant permits it. That is the standard pattern for
a shared ingress namespace routing to app namespaces.

Today a permitted cross-namespace backend renders red `✕`, which is a false failure. A forbidden one
(`ResolvedRefs=False, RefNotPermitted`) can render green if a same-named Service happens to exist
locally, which is a false success. `certificateRefs[].namespace` (`:304`, `:544`) has the same
problem. The cheapest honest fix:
1. Honour `namespace`.
2. Lift the route's own `ResolvedRefs` condition into the row or strip, so kute reports the
   controller's verdict instead of re-deriving it.

### M7/M8 — Secret reads

`resolveCertExpiry` scans `ListRaw(KindSecret, ns)` and returns `"secret not found", StatusFail` on
a miss. `Cluster.ListRaw` (`cluster.go:1170-1184`) returns an empty, nil-error list on first read
and for a Forbidden cache. `reloadsOn(KindSecret)` repairs the cold-cache case once events land.
The Forbidden case never repairs: a namespace-admin who cannot list Secrets sees every TLS host
in red.

certchain has the same shape for `secret/<name> · missing` and the issuer ref. It also discards the
CertificateRequest, Order and Challenge errors (`crs, _ :=`), so a denied chain looks like a healthy
single-node chain with no failure card.

Cost: in default mode that first `ListRaw(KindSecret, ns)` normalizes to the cluster-wide Secret
informer, and nothing strips `data` (`transform.go` has no Secret handling). Opening one TLS
Ingress downloads every Secret in the cluster, including all Helm release payloads. That is the
case §5.5 of `lazy-informers.md` gave Helm its own filtered informer to avoid.

### M9 — Ingress default backend

`spec.defaultBackend` appears nowhere in `routetable` or `projections.go`. An Ingress made of only a
default backend is a common catch-all. `applyLoaded` gives it zero rows, and once the caches settle
it enters `TaskStateEmpty`. That is an empty-state claim (CLAUDE.md: "An empty state is a claim
about the cluster") which is false.

## Test gaps

- **Service forward with `port ≠ targetPort`**, unit and e2e: use the existing `web` Service.
  Assert the dialed remote port (unit, with a recording dialer) and a successful fetch (e2e).
- **`spdyTunnel` close-before-run**: a test against the real tunnel type rather than the fake
  `Tunnel` the manager tests use.
- **A forward survives a context switch**: no e2e covers §13d's "global across context switches".
  `context_switch_test.go` never starts a forward. Assert the chip and the listener survive `c` ↵.
- **Pod-target forward with the pod deleted**: e2e. Today only Service targets are covered.
- **Expiry boundaries**: `notAfter = now−1h`, `now+1h` and `now−25h` for both `certExpiryCell`
  and `resolveCertExpiry`. `Ready=True` with an expired `notAfter`.
- **certchain with `issuerRef.kind` omitted**, an external issuer group, and Forbidden
  Secret/CertificateRequest caches (`KindError` surfaced).
- **routetable**: a cross-namespace backendRef, `ResolvedRefs=False`, a wildcard TLS host, a
  defaultBackend-only Ingress, and `loadGateway` → `selectedListenerRouteFilter` for a hostname-less
  listener (the existing test bypasses the loader).
- **Recorded-actions test** (fake clientset) asserting that opening a TLS Ingress's routing table
  does not LIST Secrets cluster-wide. It would pin M8 once fixed.
- **Golden/truecolor**: forwardpicker and certchain have goldens. routetable has goldens but none
  in the error, permission-denied or backend-note states.

## Quick wins

1. Make `spdyTunnel.Close` sticky with a `closed` flag checked in `Run` (M1, a few lines).
2. Fix the day bucketing with `if !now.Before(notAfter) → expired`, in both copies (M3), and
   escalate the glyph on `StatusFail` (M4).
3. Default an empty `issuerRef.kind` to Issuer (M5, one line plus a fixture).
4. Keep the raw listener hostname and substitute `*` in the view (L2).
5. Skip `DeletionTimestamp` pods in `firstRunningPod` (L7).
6. Exclude the manager's live `LocalPort`s in `pickLocalPort` (L9).
7. Use the Service `targetPort` for the dial and the will-run line (H1). It's small once
   `PortOption` carries the target port, and it is the highest-impact item here.
