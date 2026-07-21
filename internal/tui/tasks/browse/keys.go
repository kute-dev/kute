package browse

import (
	"fmt"
	"strings"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// Keybar composes the bottom band from verb references only (never a
// key/label literal), per the registry invariant. The empty state (10c)
// spells its own n/a/g ways out inline in the body, so its keybar carries
// no groups — just the pill and the live watch note.
func (m Model) Keybar() tui.Keybar {
	if m.pendingEdit != nil {
		return tui.Keybar{
			Pill:      tui.ModeConfirm,
			PillText:  "CONFIRM",
			Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
			RightNote: m.editConfirmPrompt(),
		}
	}
	if m.pendingStopAllForwards {
		return tui.Keybar{
			Pill:      tui.ModeConfirm,
			PillText:  "CONFIRM",
			Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
			RightNote: m.stopAllForwardsPrompt(),
		}
	}
	if m.pendingScale != nil {
		kb := tui.Keybar{
			Pill:     tui.ModeBrowse,
			PillText: "SCALE",
			Groups: [][]tui.KeyHint{{
				{Key: m.pendingScale.value + tui.GlyphSelBar, Label: "replicas"},
				{Key: "+/−", Label: "nudge"},
				{Key: "↵", Label: "apply"},
				{Key: "esc", Label: "cancel"},
			}},
		}
		if hpa := m.pendingScale.hpaName; hpa != "" {
			// The will-run line and the HPA warning together rarely fit on
			// one keybar row (insetChromeLine drops the whole right side
			// rather than truncating either) — the warning is the more
			// decision-relevant fact at this moment, so it takes the slot
			// instead of the redundant kubectl-equivalent text.
			kb.RightWarnNote = fmt.Sprintf("managed by hpa/%s — scaling overridden on next sync", hpa)
		} else {
			kb.RightNote = m.scaleWillRunLine()
		}
		return kb
	}
	if m.pendingSetImage != nil {
		t := m.pendingSetImage
		hints := []tui.KeyHint{{Key: "↵", Label: "apply"}, {Key: "↑↓", Label: "pick from history"}}
		if len(t.containers) > 1 {
			hints = append(hints, tui.KeyHint{Key: tui.GlyphTab, Label: "container"})
		}
		hints = append(hints, tui.KeyHint{Key: "ctrl-u", Label: "full ref"}, tui.KeyHint{Key: "esc", Label: "cancel"})
		return tui.Keybar{
			Pill:      tui.ModeBrowse,
			PillText:  "SET IMAGE",
			Groups:    [][]tui.KeyHint{hints},
			RightNote: "watch the rollout in 9a",
		}
	}
	if m.pendingSetResources != nil {
		hints := []tui.KeyHint{{Key: "↵", Label: "apply changed fields"}, {Key: "↑↓", Label: "field"}, {Key: "+/−", Label: "nudge (64Mi / 50m)"}}
		if len(m.pendingSetResources.containers) > 1 {
			hints = append(hints, tui.KeyHint{Key: tui.GlyphTab, Label: "container"})
		}
		hints = append(hints, tui.KeyHint{Key: "u", Label: "unset field"}, tui.KeyHint{Key: "esc", Label: "cancel"})
		return tui.Keybar{
			Pill:     tui.ModeBrowse,
			PillText: "RESOURCES",
			Groups:   [][]tui.KeyHint{hints},
		}
	}
	if m.pendingMeta != nil && !m.actions.Active() {
		// While a TierInline confirm is showing (a joined-label edit or any
		// removal), m.actions.Active() wins instead — the panel-local META
		// keybar below only ever renders in navigation/editing/adding, never
		// stacked under the confirm's own y/N (see the actions.Active()
		// branch's "set-meta" case further down, and this file's doc comment
		// on meta.go: the panel itself stays open underneath either way).
		t := m.pendingMeta
		var hints []tui.KeyHint
		switch {
		case t.adding != metaAddNone:
			// Editing mode (add sub-flow): every printable character inserts
			// literally, so these are the only reserved keys.
			hints = []tui.KeyHint{
				{Key: "↵", Label: "apply"}, {Key: tui.GlyphTab, Label: "key ↔ value"}, {Key: "esc", Label: "cancel"},
			}
		case t.editing:
			// Editing mode: same reasoning — typing a value must never be
			// shadowed by a shortcut.
			hints = []tui.KeyHint{{Key: "↵", Label: "save"}, {Key: "esc", Label: "cancel"}}
		default:
			// Navigation mode never accepts typed text, so single-letter
			// shortcuts here (a, y) can't shadow a value the way they could
			// if typing edited the row directly.
			hints = []tui.KeyHint{
				{Key: "↑↓", Label: "row"}, {Key: tui.GlyphTab, Label: "switch grid"},
				{Key: "↵", Label: "edit"}, {Key: "a/insert", Label: "add"},
				{Key: "ctrl-d", Label: "remove key · y/N"}, {Key: "y", Label: "copy key=value"},
				{Key: "esc", Label: "back"},
			}
		}
		return tui.Keybar{Pill: tui.ModeBrowse, PillText: "META", Groups: [][]tui.KeyHint{hints}}
	}
	if m.pendingBulkDelete != nil {
		if m.pendingBulkDelete.tier == actions.TierInline {
			return tui.Keybar{
				Pill:      tui.ModeConfirm,
				PillText:  "CONFIRM",
				Groups:    [][]tui.KeyHint{{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}},
				RightNote: m.bulkDeleteWillRunLine(),
			}
		}
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}
	if m.actions.Active() {
		if m.actions.Tier() == actions.TierInline {
			if m.actions.ForceArmed() {
				// force-delete staged inside this same inline confirm
				// (ctrl-k, actions.Controller.ArmForceDelete) rather than
				// jumping to the PROD type-the-name modal — the destructive
				// treatment (pill text + red-tagged hints) only kicks in once
				// armed, and the will-run line keeps the extra flags in sync
				// with what DeleteResourceForced actually sends.
				note := ""
				if pending := m.actions.Pending(); pending != nil {
					note = forceDeleteWillRunLine(pending.Scope)
				}
				return tui.Keybar{
					Pill:      tui.ModeConfirm,
					PillText:  "FORCE DELETE",
					Groups:    [][]tui.KeyHint{{{Key: "y", Label: "force delete"}, {Key: "n", Label: "back"}}},
					RightNote: note,
				}
			}
			note := m.actions.Prompt()
			hints := []tui.KeyHint{{Key: "y", Label: "confirm"}, {Key: "n", Label: "cancel"}}
			if pending := m.actions.Pending(); pending != nil {
				switch pending.Scope.Verb {
				case "rollback":
					// 18a: "shell out to helm with a will run line" — the exact
					// command, not just the generic verb/target prompt every
					// other TierInline confirm uses.
					note = rollbackPrompt(pending.Scope)
				case "delete":
					// 8b/20a: the same "will run: kubectl delete ..."
					// documentation idiom every other mutating verb's inline
					// confirm uses — replaces the generic verb/target prompt,
					// which only duplicated the y/n hints already in Groups.
					note = deleteWillRunLine(pending.Scope)
					if pending.Scope.ResourceKind == string(kube.KindPod) {
						hints = append(hints, verbs.ForceDelete.Hint())
					}
				case "set-image":
					// 24a: the exact "will run: kubectl set image ..." line, same
					// idiom as rollback/delete above.
					note = setImageWillRunLine(pending.Scope)
				case "set-resources":
					// 25a: the exact "will run: kubectl set resources ..." line,
					// same idiom as set-image above.
					note = setResourcesWillRunLine(pending.Scope)
				case "set-meta":
					// 26a: the panel itself stays open under this confirm
					// (meta.go's own doc comment) and already renders the full
					// "will run: kubectl label/annotate ..." line plus the
					// Service-selector join warning in its own will-run strip —
					// duplicating that whole line here regularly overruns the
					// keybar's width and gets silently dropped entirely
					// (insetChromeLine's "not enough room" behavior), so this
					// note stays to the short, keybar-safe join warning alone
					// (never shown for a plain removal, which carries no join
					// text of its own).
					note = metaWillRunLine(pending.Scope)
					if pending.Scope.MetaJoinService != "" {
						note = fmt.Sprintf("detaches %d pods from svc/%s",
							pending.Scope.MetaJoinPodCount, pending.Scope.MetaJoinService)
					}
				}
			}
			return tui.Keybar{
				Pill:      tui.ModeConfirm,
				PillText:  "CONFIRM",
				Groups:    [][]tui.KeyHint{hints},
				RightNote: note,
			}
		}
		return tui.Keybar{Pill: tui.ModeConfirm, PillText: "CONFIRM"}
	}

	pillText := strings.ToUpper(m.desc.Display)
	switch m.kind {
	case kube.KindHelmRelease:
		// docs/design README.md §18a: "Keybar pill HELM" — the short form,
		// unlike every other kind's pill (its full uppercase Display name).
		pillText = "HELM"
	case kube.KindDeployment:
		// docs/design README.md §9a: "Keybar pill DEPLOY", not the plural
		// "DEPLOYMENTS" every other kind's pill would produce.
		pillText = "DEPLOY"
	case kube.KindCustomResourceDefinition:
		// docs/design README.md §14b: "Keybar pill CRDS" — the built-in CRD
		// list's own short form, not the full "CUSTOMRESOURCEDEFINITIONS".
		pillText = "CRDS"
	}
	pill := tui.ModeBrowse
	if m.grouped() {
		pillText, pill = "ALL NS", tui.ModeAllNS
	}
	if n := len(m.marks); n > 0 {
		// 20a: "mode pill shows the count" — takes over the pill entirely,
		// the more urgent state once anything is marked.
		pillText, pill = fmt.Sprintf("%d MARKED", n), tui.ModeBrowse
	}

	if m.state == tui.TaskStatePermissionDenied {
		return tui.Keybar{
			Pill:       pill,
			PillText:   pillText,
			Groups:     [][]tui.KeyHint{{verbs.Goto.Hint(), verbs.Context.Hint(), verbs.WhoCan.Hint(), {Key: "y", Label: "copy error"}, verbs.Retry.Hint()}},
			RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
		}
	}

	if m.state == tui.TaskStateEmpty {
		return tui.Keybar{
			Pill:       pill,
			PillText:   pillText,
			RightNote:  fmt.Sprintf("0 %s · watching — new %s appear live", lowerDisplay(m.desc.Display), lowerDisplay(m.desc.Display)),
			RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
		}
	}

	if m.state == tui.TaskStateLoading {
		// 15a: only the nav verbs (g/n/c) are live before the first list
		// lands — everything row-scoped stays dark until rows exist
		// (docs/design README.md §15a).
		return tui.Keybar{
			Pill:       pill,
			PillText:   pillText,
			Groups:     [][]tui.KeyHint{{verbs.Goto.Hint(), verbs.Namespace.Hint(), verbs.Context.Hint()}},
			RightNote:  "row actions enable when data lands",
			RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
		}
	}

	if m.filterActive {
		return tui.Keybar{
			Pill:      tui.ModeFilter,
			PillText:  "FILTER",
			Groups:    [][]tui.KeyHint{{{Key: "esc", Label: "clear"}, verbs.MarkAll.Hint()}},
			RightNote: "type to narrow",
		}
	}

	if m.offline() {
		return tui.Keybar{
			Pill:     tui.ModeOffline,
			PillText: "OFFLINE",
			Groups: [][]tui.KeyHint{{
				verbs.Retry.Hint(),
				verbs.Context.Hint(),
				{Key: "↑↓", Label: "browse snapshot"},
			}},
			RightNote: "mutating actions disabled",
		}
	}

	baseGroup := []tui.KeyHint{verbs.Goto.Hint(), verbs.Filter.Hint(), verbs.Mark.Hint()}
	if m.kind != kube.KindForward {
		if m.kind != kube.KindHelmRelease {
			// Helm Releases carry their own 'v' values verb below instead of
			// the generic YAML/edit set — a release isn't a single real API
			// object those act on (18a has no 'y'/'E', the same carve-out
			// Forward's synthetic rows already take). Namespace-scoped 'e'
			// events stays available, same as every other kind's list view.
			if m.openYAML != nil {
				baseGroup = append(baseGroup, verbs.YAML.Hint())
			}
			baseGroup = append(baseGroup, verbs.Edit.Hint())
		}
		if m.openEvents != nil {
			baseGroup = append(baseGroup, verbs.Events.Hint())
		}
		if m.openTimeline != nil {
			baseGroup = append(baseGroup, verbs.Timeline.Hint())
		}
	}
	groups := [][]tui.KeyHint{baseGroup}
	if m.kind == kube.KindPod {
		podGroup := []tui.KeyHint{}
		if m.openPodDetail != nil {
			podGroup = append(podGroup, verbs.Open.Hint())
		}
		if m.openLogs != nil {
			podGroup = append(podGroup, verbs.Logs.Hint())
		}
		podGroup = append(podGroup, verbs.Exec.Hint())
		if m.openForward != nil {
			podGroup = append(podGroup, verbs.Forward.Hint())
		}
		if len(podGroup) > 0 {
			groups = append(groups, podGroup)
		}
	}
	if m.kind == kube.KindNode {
		nodeGroup := []tui.KeyHint{}
		if m.openNodeDetail != nil {
			nodeGroup = append(nodeGroup, verbs.Open.Hint())
		}
		nodeGroup = append(nodeGroup, verbs.NodeShell.Hint())
		groups = append(groups, nodeGroup)
		if m.mutator != nil {
			groups = append(groups, []tui.KeyHint{verbs.Cordon.Hint(), verbs.Drain.Hint()})
		}
	}
	if m.kind == kube.KindDeployment {
		deployGroup := []tui.KeyHint{verbs.Open.Hint()}
		if m.mutator != nil {
			deployGroup = append(deployGroup, verbs.RolloutRestart.Hint(), verbs.Scale.Hint(), verbs.SetImage.Hint(), verbs.SetResources.Hint())
		}
		if m.openForward != nil {
			deployGroup = append(deployGroup, verbs.Forward.Hint())
		}
		groups = append(groups, deployGroup)
	}
	if m.kind == kube.KindStatefulSet {
		stsGroup := []tui.KeyHint{verbs.Open.Hint()}
		if m.mutator != nil {
			stsGroup = append(stsGroup, verbs.Scale.Hint(), verbs.SetImage.Hint(), verbs.SetResources.Hint())
		}
		groups = append(groups, stsGroup)
	}
	if m.kind == kube.KindDaemonSet {
		dsGroup := []tui.KeyHint{verbs.Open.Hint()}
		if m.mutator != nil {
			dsGroup = append(dsGroup, verbs.SetImage.Hint(), verbs.SetResources.Hint())
		}
		groups = append(groups, dsGroup)
	}
	if m.kind == kube.KindService && m.openForward != nil {
		groups = append(groups, []tui.KeyHint{verbs.Forward.Hint()})
	}
	if m.kind == kube.KindHelmRelease {
		helmGroup := []tui.KeyHint{verbs.Open.Hint()}
		if m.openHelmValues != nil {
			helmGroup = append(helmGroup, verbs.HelmValues.Hint())
		}
		if m.openHelmHistory != nil {
			helmGroup = append(helmGroup, verbs.HelmHistory.Hint())
		}
		if m.mutator != nil {
			helmGroup = append(helmGroup, verbs.Rollback.Hint())
		}
		groups = append(groups, helmGroup)
	}
	if m.kind == kube.KindCustomResourceDefinition {
		groups = append(groups, []tui.KeyHint{verbs.Open.Hint()})
	}
	if m.kind == kube.KindIngress && m.openRouteTable != nil {
		groups = append(groups, []tui.KeyHint{verbs.Open.Hint()})
	}
	if m.desc.Custom && m.openObjectDetail != nil {
		groups = append(groups, []tui.KeyHint{verbs.Open.Hint()})
	}
	if m.kind == kube.KindForward {
		fwdGroup := []tui.KeyHint{verbs.StopForward.Hint(), verbs.RestartForward.Hint(), verbs.CopyForwardURL.Hint()}
		if m.forwards != nil {
			fwdGroup = append(fwdGroup, verbs.StopAllForwards.Hint())
		}
		groups = append(groups, fwdGroup)
	}
	if m.mutator != nil && metaEditable(m.kind) {
		// 26a: 'm' works on any row, any kind (CRDs included) — not
		// kind-gated the way SetImage/SetResources are, since every real
		// object has metadata.labels/annotations.
		groups = append(groups, []tui.KeyHint{verbs.Meta.Hint()})
	}
	if m.mutator != nil && m.kind != kube.KindForward && m.kind != kube.KindHelmRelease {
		// 18a: delete/uninstall is deliberately out of scope for Helm
		// Releases, the same "no install/upgrade-from-repo" carve-out —
		// ctrl-d has no meaningful, safe implementation here (it would only
		// delete one revision Secret, not uninstall the release).
		deleteHint := verbs.Delete.Hint()
		if n := len(m.marks); n > 0 && verbs.Delete.Bulk {
			// 20a: "ctrl-d delete 3 · y/N" — the set-applicable verb's
			// keybar label names the marked count instead of the cursor row.
			deleteHint.Label = fmt.Sprintf("delete %d", n)
		}
		groups = append(groups, []tui.KeyHint{deleteHint})
	}
	scopeGroup := []tui.KeyHint{verbs.Namespace.Hint(), verbs.Context.Hint()}
	if !m.desc.ClusterScoped {
		scopeGroup = append(scopeGroup, verbs.AllNamespaces.Hint())
	}
	if m.grouped() {
		scopeGroup = append(scopeGroup, verbs.JumpNamespace.Hint(), verbs.ToggleGroup.Hint())
	}
	groups = append(groups, scopeGroup)

	return tui.Keybar{
		Pill:       pill,
		PillText:   pillText,
		Groups:     groups,
		RightNote:  m.execFeedback,
		RightHints: append(tui.UpdateRightHints(m.session), verbs.Help.Hint()),
	}
}

func lowerDisplay(display string) string {
	return strings.ToLower(display)
}

// singularDisplay strips a trailing "s" off a Descriptor.Display plural
// (e.g. "Pods" -> "Pod") for a single-row delete confirm's title — same
// rule as objectdetail's own singularDisplay, kept package-local since
// browse doesn't import objectdetail.
func singularDisplay(plural string) string {
	return strings.TrimSuffix(plural, "s")
}

// CapturingInput reports whether the filter box is open, so the root shell
// (tui.InputCapturer) lets every keystroke — including g/n/c/? — reach
// browse's own key handling instead of treating them as global shortcuts.
func (m Model) CapturingInput() bool {
	return m.filterActive || m.actions.Active() || m.pendingEdit != nil || m.pendingStopAllForwards ||
		m.pendingScale != nil || m.pendingSetImage != nil || m.pendingSetResources != nil || m.pendingMeta != nil ||
		m.pendingBulkDelete != nil
}
