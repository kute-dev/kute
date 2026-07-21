// 26a's 'm' inline labels/annotations editor (docs/design README.md §26a):
// like setresources.go/setimage.go, a bespoke gate (pendingMeta) rather than
// actions.Controller's y/N/type-name flow up front, since there's a per-row
// value buffer (or, while adding, a key+value pair) to gather before there's
// an action to Begin. Once ↵/delete commits, execution does go through
// actions.Controller/kube.Mutator — TierNone for an ordinary edit (metadata
// changes are reversible), escalated to TierInline only for a Service-
// selector-joined label edit or any key removal. Unlike 24a/25a this
// escalation is never PROD-driven and never escalates further to the
// type-the-name modal (docs/design README.md §26a: "reversible, no
// type-the-name modal — per 8b's tiering"). Kept in its own file, browse's
// per-concern split convention (like scale.go/setimage.go/setresources.go).
//
// Keys are context-sensitive rather than globally reserved: navigation mode
// (the default) never accepts typed text, so single-letter shortcuts there —
// including 'y' copy, 'a'/insert add — can never shadow a value a user might
// want to type. Only pressing ↵ on a row enters editing mode, and only there
// does every printable character (including 'a'/'A'/'y') insert literally.
// This mirrors the add sub-mode's own key/value buffers, which have always
// worked this way. Removal is 'ctrl+d' (not 'delete'), matching the rest of
// browse's own row-delete chord instead of a second, inconsistent one.
//
// A TierInline confirm (a Service-selector-joined label edit, or any
// removal) renders *inside* this still-open panel rather than closing it
// first and falling back to the generic y/N-over-the-table convention 8b's
// delete confirm uses: m.pendingMeta is deliberately left set across
// actions.Controller's Begin/Confirm/Cancel cycle (updateKey already routes
// every keypress to updateConfirmKey once m.actions.Active(), ahead of the
// pendingMeta check, so this needs no extra input plumbing — only Body/
// Keybar/the row renderer need to keep showing the panel underneath). The
// panel only closes once the action actually resolves (update.go's
// actions.ResultMsg case), matching a plain TierNone apply's own
// close-on-success behavior; cancelling reverts the row's buffer and leaves
// the panel open in navigation mode, the same "esc backs out without
// closing" contract editing-mode's own esc already has.
package browse

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
)

// metaAddKind is which grid (if any) a/A's insert row is targeting.
type metaAddKind int

const (
	metaAddNone metaAddKind = iota
	metaAddLabel
	metaAddAnnotation
)

// metaRow is one LABELS/ANNOTATIONS grid row.
type metaRow struct {
	isAnnotation bool
	key          string
	current      string
	// buffer is the editable value, pre-filled to current, cursor-anchored
	// throughout — no replace-on-first-keystroke gate, the same continuous
	// text-field model resourceField's own buffer uses.
	buffer string
	cursor int

	// readOnly rows (a controller-managed annotation, or a workload's own
	// immutable spec.selector.matchLabels key) can be navigated to but never
	// edited or removed — kute says so up front via readOnlyNote rather than
	// letting the apply bounce off the API server (docs/design README.md
	// §26a).
	readOnly     bool
	readOnlyNote string
	// helmOwnedNote marks the one row announcing Helm ownership
	// (app.kubernetes.io/managed-by=Helm) — editable, just carries a note
	// that the next `helm upgrade` may revert it.
	helmOwnedNote bool
	// joinService/joinPodCount are set (labels only) when a Service selector
	// in this namespace currently matches this key/value pair — computed
	// once at beginMeta time ("joins render before you touch anything"),
	// never recomputed against the in-progress (unapplied) buffer.
	joinService  string
	joinPodCount int
}

// changed reports whether r differs from its prefilled current value.
func (r metaRow) changed() bool { return r.buffer != r.current }

// setBuffer replaces r.buffer wholesale and parks the cursor at its end —
// the same convention resourceField.setBuffer/setImageTarget.setBuffer use.
func (r *metaRow) setBuffer(s string) {
	r.buffer = s
	r.cursor = len([]rune(s))
}

// metaSection is which grid — LABELS or ANNOTATIONS — navigation, 'a'/insert,
// and tab/shift+tab currently target. Only two sections exist, so tab and
// shift+tab both just toggle it.
type metaSection int

const (
	metaSectionLabels metaSection = iota
	metaSectionAnnotations
)

// metaPendingCommit remembers what a TierNone or confirmed-TierInline commit
// is currently trying to write, so handleMetaResult can either build the
// right inline success message + know which row to refocus after a refresh,
// or — on failure — restore the exact pre-commit interaction state (still
// editing, or still in the add sub-flow) with the attempted value intact
// (docs/design README.md §26a: "confirm → execute → refresh → show result →
// remain on screen").
type metaPendingCommit struct {
	isAdd        bool
	isRemove     bool
	section      metaSection
	key          string // the existing row's key (edit/remove); "" for add
	isAnnotation bool
	value        string // the value being applied; "" for a removal
}

// metaTarget is the state pendingMeta gates on while 26a's panel is showing.
type metaTarget struct {
	kind      kube.ResourceKind
	namespace string
	name      string

	labels      []metaRow
	annotations []metaRow
	// section is the currently focused grid; labelIdx/annotationIdx are that
	// grid's own cursor, kept independently so switching focus and back
	// doesn't lose your place in either list.
	section       metaSection
	labelIdx      int
	annotationIdx int
	// editing is true while the selected row's value is a free-typing
	// buffer (entered via ↵) — see this file's own doc comment on why
	// navigation-mode shortcuts and editing-mode text input never collide.
	editing bool

	// adding is metaAddNone unless 'a'/insert's insert row is showing.
	adding         metaAddKind
	addKey         string
	addKeyCursor   int
	addValue       string
	addValueCursor int
	addOnValue     bool // tab moves focus from key to value

	// pendingCommit is set the instant a commit starts (TierNone's
	// synchronous apply, or a TierInline confirm) and cleared once
	// handleMetaResult applies its outcome — see that type's own doc
	// comment.
	pendingCommit *metaPendingCommit
	// message/lastError are the panel's own transient inline result line —
	// "updated env=staging" / "removed kute.dev/owner" on success, the raw
	// server error on failure — cleared the next time a commit starts.
	message   string
	lastError string
}

// selectedRow is the focused section's row at its own cursor, if any — nil
// when that section is empty (including both grids empty at once).
func (t *metaTarget) selectedRow() *metaRow {
	switch t.section {
	case metaSectionAnnotations:
		if t.annotationIdx >= 0 && t.annotationIdx < len(t.annotations) {
			return &t.annotations[t.annotationIdx]
		}
	default:
		if t.labelIdx >= 0 && t.labelIdx < len(t.labels) {
			return &t.labels[t.labelIdx]
		}
	}
	return nil
}

// rowsFor returns section's row slice and a pointer to its own cursor field
// — shared by moveSelection and handleMetaResult's failure-restore path,
// which both need to address either grid generically.
func (t *metaTarget) rowsFor(section metaSection) ([]metaRow, *int) {
	if section == metaSectionAnnotations {
		return t.annotations, &t.annotationIdx
	}
	return t.labels, &t.labelIdx
}

// metaEditable reports whether kind takes 26a's editor — every real kind
// (Pod, Node, a CRD instance, …) except kute's own synthetic non-object rows,
// which have no metadata.labels/annotations to speak of.
func metaEditable(kind kube.ResourceKind) bool {
	switch kind {
	case kube.KindForward, kube.KindHelmRelease, kube.KindWhoCan, kube.KindOverview:
		return false
	default:
		return true
	}
}

// beginMeta opens 26a's panel for the selected row. ok is false when nothing
// applies — mirrors beginSetImage/beginSetResources's ok-bool contract.
func (m *Model) beginMeta() bool {
	if !metaEditable(m.kind) || m.mutator == nil || m.state != tui.TaskStateReady {
		return false
	}
	row, ok := m.selectedRow()
	if !ok {
		return false
	}
	t, ok := m.buildMetaTarget(m.kind, row.Namespace, row.Name)
	if !ok {
		return false
	}
	m.pendingMeta = t
	return true
}

// buildMetaTarget fetches kind/namespace/name fresh from the lister and
// builds a metaTarget from its real, current labels/annotations — the
// row-building half of beginMeta, factored out so handleMetaResult's own
// post-commit refresh (docs/design README.md §26a: "re-fetch the object...
// rather than leaving the locally edited state on screen") always reflects
// the authoritative server state rather than an optimistic local patch,
// exactly like the panel's very first open.
func (m *Model) buildMetaTarget(kind kube.ResourceKind, namespace, name string) (*metaTarget, bool) {
	obj, ok := workloadObject(m.lister, kind, namespace, name)
	if !ok {
		return nil, false
	}
	acc, err := apimeta.Accessor(obj)
	if err != nil {
		return nil, false
	}
	objLabels := acc.GetLabels()

	t := &metaTarget{kind: kind, namespace: namespace, name: name}
	t.labels = buildMetaRows(objLabels, false)
	t.annotations = buildMetaRows(acc.GetAnnotations(), true)

	joins := serviceLabelJoins(m.lister, namespace, objLabels)
	immutable := immutableSelectorKeys(obj)
	helmOwned := objLabels["app.kubernetes.io/managed-by"] == "Helm"
	for i := range t.labels {
		l := &t.labels[i]
		if j, ok := joins[l.key]; ok {
			l.joinService, l.joinPodCount = j.service, j.podCount
		}
		if immutable[l.key] {
			l.readOnly, l.readOnlyNote = true, "immutable selector · server rejects this edit"
		}
		if helmOwned && l.key == "app.kubernetes.io/managed-by" {
			l.helmOwnedNote = true
		}
	}
	for i := range t.annotations {
		a := &t.annotations[i]
		if controllerManagedAnnotationKey(a.key) {
			a.readOnly, a.readOnlyNote = true, "controller-managed · read-only"
		}
	}
	return t, true
}

// buildMetaRows sorts values by key (for stable, deterministic display —
// metadata maps carry no meaningful iteration order of their own) and
// prefills each row's buffer to its current value.
func buildMetaRows(values map[string]string, isAnnotation bool) []metaRow {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([]metaRow, 0, len(keys))
	for _, k := range keys {
		r := metaRow{isAnnotation: isAnnotation, key: k, current: values[k]}
		r.setBuffer(values[k])
		rows = append(rows, r)
	}
	return rows
}

// joinInfo is one label key's Service-selector join fact.
type joinInfo struct {
	service  string
	podCount int
}

// serviceLabelJoins finds, for each key in objLabels, the (alphabetically
// first, for determinism when more than one matches) Service in namespace
// whose selector matches the whole objLabels set — docs/design README.md
// §26a: "joins render before you touch anything." podCount is how many Pods
// in the namespace that Service currently selects (kind-independent of
// whatever object is being edited), the exact "detaches N pods" figure the
// confirm's warning line names.
func serviceLabelJoins(lister resources.RawLister, namespace string, objLabels map[string]string) map[string]joinInfo {
	if lister == nil || len(objLabels) == 0 {
		return nil
	}
	svcObjs, err := lister.ListRaw(context.Background(), kube.KindService, namespace)
	if err != nil {
		return nil
	}
	set := labels.Set(objLabels)

	type svcMatch struct {
		name     string
		selector map[string]string
	}
	var matches []svcMatch
	for _, obj := range svcObjs {
		svc, ok := obj.(*corev1.Service)
		if !ok || len(svc.Spec.Selector) == 0 {
			continue
		}
		if labels.SelectorFromSet(svc.Spec.Selector).Matches(set) {
			matches = append(matches, svcMatch{name: svc.Name, selector: svc.Spec.Selector})
		}
	}
	if len(matches) == 0 {
		return nil
	}
	sort.Slice(matches, func(i, j int) bool { return matches[i].name < matches[j].name })

	podObjs, _ := lister.ListRaw(context.Background(), kube.KindPod, namespace)
	out := map[string]joinInfo{}
	for key := range objLabels {
		for _, sm := range matches {
			if _, ok := sm.selector[key]; !ok {
				continue
			}
			out[key] = joinInfo{
				service:  sm.name,
				podCount: countMatchingPods(podObjs, labels.SelectorFromSet(sm.selector)),
			}
			break
		}
	}
	return out
}

// countMatchingPods counts how many Pod objects among podObjs satisfy
// selector — the real, current "how many pods does this Service serve"
// figure, independent of the object whose labels are being edited.
func countMatchingPods(podObjs []runtime.Object, selector labels.Selector) int {
	n := 0
	for _, obj := range podObjs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		if selector.Matches(labels.Set(pod.Labels)) {
			n++
		}
	}
	return n
}

// immutableSelectorKeys reads a Deployment/StatefulSet/DaemonSet's own
// spec.selector.matchLabels — immutable server-side after creation (docs/
// design README.md §26a: "Deployment selector labels are immutable
// server-side — kute says so up front instead of letting the apply bounce
// off the API server"). Nil for every other kind.
func immutableSelectorKeys(obj runtime.Object) map[string]bool {
	var sel *metav1.LabelSelector
	switch o := obj.(type) {
	case *appsv1.Deployment:
		sel = o.Spec.Selector
	case *appsv1.StatefulSet:
		sel = o.Spec.Selector
	case *appsv1.DaemonSet:
		sel = o.Spec.Selector
	default:
		return nil
	}
	if sel == nil || len(sel.MatchLabels) == 0 {
		return nil
	}
	out := make(map[string]bool, len(sel.MatchLabels))
	for k := range sel.MatchLabels {
		out[k] = true
	}
	return out
}

// controllerManagedAnnotationKey reports whether key is one of the
// controller-written annotations §26a calls out as read-only:
// deployment.kubernetes.io/revision and the kubectl.kubernetes.io/* family.
func controllerManagedAnnotationKey(key string) bool {
	return key == "deployment.kubernetes.io/revision" || strings.HasPrefix(key, "kubectl.kubernetes.io/")
}

// metaKeyExists reports whether key already exists in t's label/annotation
// section — used to decide the ADD flow's --overwrite flag.
func metaKeyExists(t *metaTarget, isAnnotation bool, key string) bool {
	rows := t.labels
	if isAnnotation {
		rows = t.annotations
	}
	for _, r := range rows {
		if r.key == key {
			return true
		}
	}
	return false
}

// updateMetaKey routes keys while pendingMeta's panel is showing — add-mode
// and editing-mode each get first refusal (both are text-entry contexts, so
// every printable character must reach the buffer, never a shortcut);
// everything else here is navigation mode, which never accepts typed text at
// all, so its single-letter shortcuts (y, n) can never shadow a value.
func (m *Model) updateMetaKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingMeta
	if t.adding != metaAddNone {
		return m.updateMetaAddKey(msg)
	}
	if t.editing {
		return m.updateMetaEditKey(msg)
	}
	if msg.String() != "esc" {
		// A leftover "updated env=staging"/error banner from the last
		// commit is only meant to answer "what just happened" — the moment
		// the user does anything else in navigation mode, it's stale.
		// (Editing/add-mode's own failure-restore path never routes through
		// here, so a retry-in-progress error stays visible while retyping.)
		t.message, t.lastError = "", ""
	}
	switch msg.String() {
	case "esc":
		m.pendingMeta = nil
	case "up", "k":
		t.moveSelection(-1)
	case "down", "j":
		t.moveSelection(1)
	case "tab", "shift+tab":
		if t.section == metaSectionLabels {
			t.section = metaSectionAnnotations
		} else {
			t.section = metaSectionLabels
		}
	case "enter":
		r := t.selectedRow()
		if r == nil || r.readOnly {
			return m, nil
		}
		r.setBuffer(r.current)
		t.editing = true
	case "ctrl+d":
		r := t.selectedRow()
		if r == nil || r.readOnly {
			return m, nil
		}
		row := *r
		target := *t
		return m, m.commitMetaRemove(target, row)
	case "a", "insert":
		t.adding = metaAddLabel
		if t.section == metaSectionAnnotations {
			t.adding = metaAddAnnotation
		}
		t.addKey, t.addValue, t.addOnValue = "", "", false
		t.addKeyCursor, t.addValueCursor = 0, 0
	case "y":
		if r := t.selectedRow(); r != nil {
			return m, tea.SetClipboard(r.key + "=" + r.current)
		}
	}
	return m, nil
}

// moveSelection moves the focused section's own cursor by delta, clamped —
// the other section's cursor is untouched, so switching focus (tab) and back
// always returns to the same row.
func (t *metaTarget) moveSelection(delta int) {
	switch t.section {
	case metaSectionAnnotations:
		if n := len(t.annotations); n > 0 {
			t.annotationIdx = min(max(t.annotationIdx+delta, 0), n-1)
		}
	default:
		if n := len(t.labels); n > 0 {
			t.labelIdx = min(max(t.labelIdx+delta, 0), n-1)
		}
	}
}

// updateMetaEditKey routes keys while the selected row's value is being
// edited (entered via ↵ in navigation mode) — every printable character,
// including 'a'/'A'/'y', inserts literally here; ↵ saves, esc cancels back
// to navigation without closing the panel.
func (m *Model) updateMetaEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingMeta
	r := t.selectedRow()
	if r == nil {
		t.editing = false
		return m, nil
	}
	switch msg.String() {
	case "esc":
		r.setBuffer(r.current)
		t.editing = false
	case "enter":
		if !r.changed() {
			t.editing = false
			return m, nil
		}
		row := *r
		target := *t
		t.editing = false
		return m, m.commitMeta(target, row, true, false)
	case "left":
		r.cursor = max(r.cursor-1, 0)
	case "right":
		r.cursor = min(r.cursor+1, len([]rune(r.buffer)))
	case "backspace":
		if r.cursor > 0 {
			rr := []rune(r.buffer)
			r.buffer = string(rr[:r.cursor-1]) + string(rr[r.cursor:])
			r.cursor--
		}
	default:
		if msg.Text != "" {
			rr := []rune(r.buffer)
			ins := []rune(msg.Text)
			r.buffer = string(rr[:r.cursor]) + string(ins) + string(rr[r.cursor:])
			r.cursor += len(ins)
		}
	}
	return m, nil
}

// updateMetaAddKey routes keys while 'a'/insert's insert row is showing — a
// two-buffer (key, value) sub-mode distinct from normal row editing, since
// adding needs both typed rather than just a prefilled value. tab/shift+tab
// move focus forward/back between the two buffers, mirroring the rest of the
// panel's own tab/shift+tab convention rather than only going one way.
func (m *Model) updateMetaAddKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	t := m.pendingMeta
	switch msg.String() {
	case "esc":
		t.adding = metaAddNone
	case "tab":
		t.addOnValue = true
	case "shift+tab":
		t.addOnValue = false
	case "enter":
		key := strings.TrimSpace(t.addKey)
		if key == "" {
			return m, nil
		}
		isAnnotation := t.adding == metaAddAnnotation
		row := metaRow{isAnnotation: isAnnotation, key: key, buffer: t.addValue}
		overwrite := metaKeyExists(t, isAnnotation, key)
		target := *t
		t.adding = metaAddNone
		return m, m.commitMeta(target, row, overwrite, true)
	case "left":
		if t.addOnValue {
			t.addValueCursor = max(t.addValueCursor-1, 0)
		} else {
			t.addKeyCursor = max(t.addKeyCursor-1, 0)
		}
	case "right":
		if t.addOnValue {
			t.addValueCursor = min(t.addValueCursor+1, len([]rune(t.addValue)))
		} else {
			t.addKeyCursor = min(t.addKeyCursor+1, len([]rune(t.addKey)))
		}
	case "backspace":
		if t.addOnValue {
			if t.addValueCursor > 0 {
				r := []rune(t.addValue)
				t.addValue = string(r[:t.addValueCursor-1]) + string(r[t.addValueCursor:])
				t.addValueCursor--
			}
		} else if t.addKeyCursor > 0 {
			r := []rune(t.addKey)
			t.addKey = string(r[:t.addKeyCursor-1]) + string(r[t.addKeyCursor:])
			t.addKeyCursor--
		}
	default:
		if msg.Text != "" {
			if t.addOnValue {
				r := []rune(t.addValue)
				ins := []rune(msg.Text)
				t.addValue = string(r[:t.addValueCursor]) + string(ins) + string(r[t.addValueCursor:])
				t.addValueCursor += len(ins)
			} else {
				r := []rune(t.addKey)
				ins := []rune(msg.Text)
				t.addKey = string(r[:t.addKeyCursor]) + string(ins) + string(r[t.addKeyCursor:])
				t.addKeyCursor += len(ins)
			}
		}
	}
	return m, nil
}

// commitMeta executes a label/annotation set through actions.Controller —
// TierNone (applies immediately, mirroring commitSetImage's non-PROD path)
// unless row is a Service-selector-joined label, which escalates to
// TierInline regardless of PROD (docs/design README.md §26a: "requires the
// inline y/N even though metadata edits are otherwise reversible" — no PROD
// dependency, unlike 24a/25a's own tiering, and no further escalation to a
// type-the-name modal). isAdd distinguishes the add sub-flow from an
// existing row's edit purely for handleMetaResult's own bookkeeping (which
// interaction state to restore on failure, "added"/"updated" wording on
// success) — it has no effect on tiering or the patch itself.
func (m *Model) commitMeta(t metaTarget, row metaRow, overwrite, isAdd bool) tea.Cmd {
	tier := actions.TierNone
	scope := tui.TaskScope{
		ResourceKind: string(t.kind), ResourceName: t.name, Namespace: t.namespace,
		Verb: "set-meta", IsMutating: true,
		MetaKey: row.key, MetaValue: row.buffer, MetaIsAnnotation: row.isAnnotation, MetaOverwrite: overwrite,
	}
	if row.joinService != "" {
		tier = actions.TierInline
		scope.MetaJoinService, scope.MetaJoinPodCount = row.joinService, row.joinPodCount
	}
	kind := "label"
	if row.isAnnotation {
		kind = "annotation"
	}
	m.armMetaCommit(&metaPendingCommit{
		isAdd: isAdd, section: sectionOf(row.isAnnotation),
		key: row.key, isAnnotation: row.isAnnotation, value: row.buffer,
	})
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "set-meta-" + t.namespace + "/" + t.name + "/" + row.key,
		Label: fmt.Sprintf("Set %s %s on %s?", kind, row.key, t.name),
		Scope: scope,
	})
}

// commitMetaRemove executes a key removal through actions.Controller —
// always TierInline (docs/design README.md §26a: "reversible, no
// type-the-name modal — per 8b's tiering"), never PROD-escalated further.
func (m *Model) commitMetaRemove(t metaTarget, row metaRow) tea.Cmd {
	kind := "label"
	if row.isAnnotation {
		kind = "annotation"
	}
	m.armMetaCommit(&metaPendingCommit{
		isRemove: true, section: sectionOf(row.isAnnotation),
		key: row.key, isAnnotation: row.isAnnotation,
	})
	return m.actions.Begin(actions.TierInline, tui.TaskAction{
		ID:    "remove-meta-" + t.namespace + "/" + t.name + "/" + row.key,
		Label: fmt.Sprintf("Remove %s %s from %s?", kind, row.key, t.name),
		Scope: tui.TaskScope{
			ResourceKind: string(t.kind), ResourceName: t.name, Namespace: t.namespace,
			Verb: "set-meta", IsMutating: true,
			MetaKey: row.key, MetaIsAnnotation: row.isAnnotation, MetaRemove: true,
		},
	})
}

// armMetaCommit records what a commit about to start (via actions.Begin) is
// attempting, on the live panel — a no-op if the panel somehow isn't open
// (defensive only; every caller is itself a pendingMeta-gated key handler).
// Clears any stale message/error from a previous commit, the same "clear
// before the next attempt" point 24a/25a's own dry-run error handling uses.
func (m *Model) armMetaCommit(pc *metaPendingCommit) {
	if m.pendingMeta == nil {
		return
	}
	m.pendingMeta.pendingCommit = pc
	m.pendingMeta.message = ""
	m.pendingMeta.lastError = ""
}

// sectionOf maps a row's isAnnotation flag to its grid — shared by
// commitMeta/commitMetaRemove's metaPendingCommit construction above.
func sectionOf(isAnnotation bool) metaSection {
	if isAnnotation {
		return metaSectionAnnotations
	}
	return metaSectionLabels
}

// handleMetaResult applies a set-meta/remove-meta action's outcome to the
// still-open panel — update.go's actions.ResultMsg case calls this instead
// of ever nulling m.pendingMeta itself, per docs/design README.md §26a's own
// contract: "confirm → execute → refresh → show result → remain on screen."
//
// On success, the object is re-fetched and the grid rebuilt from the real,
// current cluster state (buildMetaTarget — never an optimistic local patch),
// recomputing joins/immutable-selector/controller-managed/Helm-owned flags
// fresh; focus lands back on the row that was just touched (by key), or —
// after a removal — the nearest remaining row in the same grid.
//
// On failure, nothing is refetched: the row/add-buffers are restored to
// exactly their pre-commit interaction state (still editing, or still in the
// add sub-flow) with the attempted value intact, and the server's error is
// surfaced via t.lastError (meta_view.go's will-run strip).
//
// Only esc/back ever closes the panel from here — a failed or successful
// commit never does.
func (m *Model) handleMetaResult(msg actions.ResultMsg) tea.Cmd {
	t := m.pendingMeta
	pc := t.pendingCommit
	t.pendingCommit = nil

	if msg.Err != nil {
		t.lastError = msg.Err.Error()
		t.message = ""
		if pc == nil {
			return nil
		}
		t.section = pc.section
		if pc.isAdd {
			t.adding = metaAddLabel
			if pc.isAnnotation {
				t.adding = metaAddAnnotation
			}
			t.addKey, t.addValue = pc.key, pc.value
			t.addKeyCursor, t.addValueCursor = len([]rune(pc.key)), len([]rune(pc.value))
			t.addOnValue = true
			return nil
		}
		rows, idx := t.rowsFor(pc.section)
		for i := range rows {
			if rows[i].key != pc.key {
				continue
			}
			*idx = i
			if !pc.isRemove {
				rows[i].setBuffer(pc.value)
				t.editing = true
			}
			break
		}
		return nil
	}

	// docs/design README.md §26a: "Show an inline success message such as
	// updated env=staging or removed kute.dev/owner" — key=value for a
	// set, bare key for a removal, verbatim.
	message := "updated"
	switch {
	case pc == nil:
	case pc.isRemove:
		message = "removed " + pc.key
	case pc.isAdd:
		message = fmt.Sprintf("added %s=%s", pc.key, pc.value)
	default:
		message = fmt.Sprintf("updated %s=%s", pc.key, pc.value)
	}

	fresh, ok := m.buildMetaTarget(t.kind, t.namespace, t.name)
	if !ok {
		// The object vanished from the lister (deleted concurrently, most
		// likely) — nothing left to refresh into, so the panel closes
		// rather than sit open on a stale/empty shell.
		m.pendingMeta = nil
		return nil
	}
	fresh.message = message
	fresh.section = t.section
	targetKey := ""
	if pc != nil {
		targetKey = pc.key
	}
	fresh.labelIdx = metaFocusIndex(fresh.labels, t.labelIdx, targetKey, t.section == metaSectionLabels)
	fresh.annotationIdx = metaFocusIndex(fresh.annotations, t.annotationIdx, targetKey, t.section == metaSectionAnnotations)
	m.pendingMeta = fresh
	return nil
}

// metaFocusIndex picks the refreshed grid's cursor after a commit: the same
// key's new row when it still exists (an edit or an add), or — once it's
// gone (a removal) — the nearest remaining row at about the same position.
// applies is false for the grid that wasn't focused at commit time, which
// always just keeps index 0 (its cursor is meaningless until focused again).
func metaFocusIndex(newRows []metaRow, oldIdx int, targetKey string, applies bool) int {
	if !applies || len(newRows) == 0 {
		return 0
	}
	if targetKey != "" {
		for i, r := range newRows {
			if r.key == targetKey {
				return i
			}
		}
	}
	return min(oldIdx, len(newRows)-1)
}

// metaWillRunLine renders the exact "will run: kubectl label/annotate ..."
// line for a pending TierInline confirmation's keybar RightNote — same
// "read straight off the resolved Scope" idiom as setImageWillRunLine/
// setResourcesWillRunLine.
func metaWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.MetaCommandString(
		kube.ResourceKind(scope.ResourceKind), scope.Namespace, scope.ResourceName,
		scope.MetaIsAnnotation, scope.MetaKey, scope.MetaValue, scope.MetaRemove, scope.MetaOverwrite,
	)
}
