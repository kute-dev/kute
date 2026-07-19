// Package resources is the display catalog for Kubernetes resource kinds. Each
// kind has a Descriptor that knows its columns and how to project a raw API
// object into a display Row; a Registry maps kinds to descriptors and Groups
// bucket kinds for the Home explorer. This is the layer that makes adding a new
// resource type a single descriptor entry rather than a new screen.
//
// It depends on kube (for ResourceKind), the k8s API types it projects, and
// tui/components (Column/Cell — Theme-agnostic rendering primitives, see
// columns.go) — never on tui itself or any screen package, so the catalog
// stays a pure data/display concern with no Theme or Bubble Tea dependency.
package resources

import (
	"context"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// StatusClass is a coarse health classification used to color a row.
type StatusClass string

const (
	StatusNeutral StatusClass = "neutral"
	StatusOK      StatusClass = "ok"
	StatusWarn    StatusClass = "warn"
	StatusFail    StatusClass = "fail"
)

// Row is one projected resource ready for display. Cells align positionally with
// the descriptor's Columns.
type Row struct {
	Namespace string
	Name      string
	Cells     []string
	Status    StatusClass

	// Glyph/GlyphClass are the status-column glyph and its color class
	// (rendered via tui/glyphs.go by the screen, which owns Theme — this
	// package stays Theme-agnostic). GlyphClass defaults to Status when a
	// projection leaves it unset.
	Glyph      string
	GlyphClass StatusClass

	// Cordoned marks a Node row as unschedulable (spec.unschedulable) — the
	// 11a nodes-list C verb toggles off this state, so browse needs it
	// outside the display Cells to know which direction to mutate.
	Cordoned bool

	// NameSuffix is appended after the NAME cell's text (dim, not part of
	// the filter/sort-relevant Name itself) — 11a's inline control-plane
	// role tag, e.g. "node-1 (control-plane)".
	NameSuffix string

	// Key is an opaque identifier for verbs that need to reference the
	// underlying object beyond Namespace/Name — Forwards' session ID (Name
	// is a fuzzy-searchable "port→target" label, not a stable key; the
	// 13c stop/restart verbs need the real ID). Unset (empty) for every
	// other kind.
	Key string
}

// HealthCounts tallies a kind's rows by StatusClass, for the browse
// health-strip ("● 32 ◐ 2 ✕ 1"). It's coarse (OK/Warn/Fail/Neutral) rather
// than kind-specific so every Descriptor can share one Health
// implementation (StatusHealth) unless a kind needs bespoke tallying.
type HealthCounts struct {
	OK, Warn, Fail, Neutral int
}

// Total is the row count the counts were tallied from.
func (h HealthCounts) Total() int { return h.OK + h.Warn + h.Fail + h.Neutral }

// StatusHealth tallies rows by Status. It's the default Health
// implementation for every built-in Descriptor.
func StatusHealth(rows []Row) HealthCounts {
	var h HealthCounts
	for _, r := range rows {
		switch r.Status {
		case StatusOK:
			h.OK++
		case StatusWarn:
			h.Warn++
		case StatusFail:
			h.Fail++
		default:
			h.Neutral++
		}
	}
	return h
}

// RawLister fetches raw API objects for a kind from a data source (the informer
// cache in production, a fake in tests). It is the catalog's only dependency on
// live cluster data.
type RawLister interface {
	ListRaw(ctx context.Context, kind kube.ResourceKind, namespace string) ([]runtime.Object, error)
}

// Descriptor describes how to list and render one resource kind.
type Descriptor struct {
	Kind    kube.ResourceKind
	Group   GroupID
	Display string // plural display name, e.g. "Deployments"
	Icon    string
	Columns []string
	// Describe is a short noun phrase for the 3a browse-grid footer, e.g.
	// "running application instances".
	Describe string
	// ClusterScoped kinds have no namespace segment (Nodes, Namespaces) —
	// the browse breadcrumb drops it and tags "cluster-scoped" instead.
	ClusterScoped bool
	// FlexColumn names the Columns entry that should flex to fill leftover
	// table width. Empty defaults to "Name" (every built-in kind but
	// Forwards, whose widest/variable cell is Target instead).
	FlexColumn string
	// Project converts a raw API object into a display Row. It must tolerate an
	// unexpected type by returning a best-effort Row (see metaRow).
	Project func(obj runtime.Object) Row
	// Health tallies a kind's rows for the browse health strip. Defaults to
	// StatusHealth for every built-in descriptor; a kind can override it if
	// its health signal isn't well captured by StatusClass alone.
	Health func(rows []Row) HealthCounts
	// HealthLabel names a StatusClass for the health-strip segment ("32
	// running", "2 pending"). Defaults to DefaultHealthLabel's generic
	// wording; Pods overrides it with the docs/design/README.md 2a copy.
	HealthLabel func(StatusClass) string
	// Custom marks a kind discovered from a CRD at connect time (14a) — the
	// one generic flag browse/goto key off to route a row to the 14d
	// generic detail screen and the 14c API-group type label, instead of
	// any kind-name check. False for every built-in kind, including the
	// always-present CustomResourceDefinition list itself (14b) — that one
	// is a routing kind, not a discovered instance kind.
	Custom bool
	// APIGroup is the discovered kind's CRD API group (e.g.
	// "cert-manager.io") — non-empty only when Custom is true. The goto
	// palette's type label (14c) uses it instead of the built-in Group
	// taxonomy.
	APIGroup string
	// APIVersion is the discovered kind's display version (e.g. "v1") —
	// non-empty only when Custom is true. 14a's breadcrumb combines it with
	// APIGroup for the dim "cert-manager.io/v1" tag (docs/design README.md
	// §14a).
	APIVersion string
}

// InstanceCounter reads a live instance count for a discovered kind — the
// 14b CRDs list's COUNT column. Satisfied directly by *kube.Cluster and
// *kube/fake.Cluster.
type InstanceCounter interface {
	CountInstances(kind kube.ResourceKind) int
}

// DefaultHealthLabel is the generic per-class wording used by every kind
// that doesn't set Descriptor.HealthLabel.
func DefaultHealthLabel(class StatusClass) string {
	switch class {
	case StatusOK:
		return "ok"
	case StatusWarn:
		return "warn"
	case StatusFail:
		return "fail"
	default:
		return "other"
	}
}

// List fetches and projects every object of the descriptor's kind in namespace.
func List(ctx context.Context, src RawLister, d Descriptor, namespace string) ([]Row, error) {
	objs, err := src.ListRaw(ctx, d.Kind, namespace)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(objs))
	for _, obj := range objs {
		rows = append(rows, d.Project(obj))
	}
	// The informer cache returns objects in unstable map-iteration order, which
	// makes lists visibly jump on every watch event. Sort into a stable ascending
	// order (namespace, then case-insensitive name) so refreshes don't reshuffle.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return strings.Compare(strings.ToLower(rows[i].Name), strings.ToLower(rows[j].Name)) < 0
	})
	return rows, nil
}

// Count returns how many objects of kind exist in namespace, for the Home tiles
// and per-kind counts.
func Count(ctx context.Context, src RawLister, kind kube.ResourceKind, namespace string) (int, error) {
	objs, err := src.ListRaw(ctx, kind, namespace)
	if err != nil {
		return 0, err
	}
	return len(objs), nil
}
