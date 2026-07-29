// Helm-release-specific browse machinery for 18a (docs/design README.md):
// the ↵ "open this release's objects" shortcut (9a's recipe), 'v' values
// (pushes the read-only YAML viewer) and 'h' history (pushes
// tasks/helmhistory's revision rail), and the 'R' rollback verb (shells out
// to the real helm binary, 8b-style friction). Kept in its own file,
// browse's per-concern split convention (like nodes.go/deployments.go/
// forwards.go).
package browse

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/helmrepo"
	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// ChartIndexReporter is implemented by a lister that also knows the state of
// the local Helm repo cache the LATEST column is answered from. Declared
// here, in the package that consumes it, per the repo's seam convention.
//
// Exported so app can assert its lister decorators against it at compile
// time: like every optional seam, a decorator that forgets to forward it
// still builds — the type assertion below just misses, and the strip
// silently stops caveating a stale answer.
type ChartIndexReporter interface {
	ChartIndexStatus() helmrepo.Status
}

// chartCacheStale is how old the repo cache gets before its age stops being
// a footnote and becomes a warning. Under a day, "run helm repo update" isn't
// news.
const chartCacheStale = 24 * time.Hour

// chartCacheNote is 18a's caveat on the LATEST column's data source: how old
// the local repo cache the column is answered from actually is.
//
// The strip names it in every state — an all-`–` LATEST column must never be
// readable as "everything is up to date", and silence when the cache is fresh
// left no way to tell "checked, and you're current" from "never checked".
// Fresh renders dim; missing or stale turns Theme.Warn and carries the
// command that fixes it.
//
// Resolved as fields rather than a finished string because the view picks a
// form by available width and colors the pieces differently — while the clock
// stays out of the render path.
type chartCacheNote struct {
	// text is the full form ("no helm repo cache", "repo cache 6d old").
	text string
	// short is the narrow-terminal form ("no repo cache", "cache 6d old"),
	// which on an 80-column strip is the only one that fits beside the
	// health counts.
	short string
	// remedy names the command that fixes it ("run helm repo add"), empty
	// while the cache is fresh and there's nothing to fix.
	remedy string
	// warn marks the missing and stale states, which render in Theme.Warn
	// rather than the strip's usual Theme.TextDim.
	warn bool
}

func (n chartCacheNote) empty() bool { return n.text == "" }

// chartCacheNoteFor resolves the note from the lister's repo-cache status.
// The zero value — no note at all — is what a lister that doesn't report one
// gets, which is every kind but Helm releases.
//
// now is passed in because this runs in the load command, not the render
// path (which stays pure: no clock reads).
func chartCacheNoteFor(lister resources.RawLister, now time.Time) chartCacheNote {
	reporter, ok := lister.(ChartIndexReporter)
	if !ok {
		return chartCacheNote{}
	}
	status := reporter.ChartIndexStatus()
	if !status.Configured || status.Repos == 0 {
		return chartCacheNote{
			text:   "no helm repo cache",
			short:  "no repo cache",
			remedy: "run helm repo add",
			warn:   true,
		}
	}
	note := chartCacheNote{
		text:  "repo cache " + chartCacheAge(status.Age(now)) + " old",
		short: "cache " + chartCacheAge(status.Age(now)) + " old",
	}
	if status.Age(now) < time.Minute {
		// --demo's static cache is minted at startup, and a real one right
		// after `helm repo update` is seconds old: "repo cache 0s old" reads
		// like a defect rather than the best case.
		note.text, note.short = "repo cache just updated", "cache fresh"
	}
	if status.Age(now) >= chartCacheStale {
		note.remedy, note.warn = "run helm repo update", true
	}
	return note
}

// chartCacheAge is shortAge with a floor of one minute, so the sub-minute
// bucket ("0s") never reaches a user-facing sentence.
func chartCacheAge(age time.Duration) string {
	if age < time.Minute {
		age = time.Minute
	}
	return shortAge(age)
}

// openReleaseObjects switches kind to Pods with row's release name
// pre-applied as the filter query — the same "owner match via the existing
// fuzzy filter" recipe openDeploymentPods (deployments.go) already
// established for 9a, reused verbatim for 18a's "↵ = objects in the release
// (filtered tables, 9a's recipe)": Helm-managed pod names conventionally
// carry the release name too (`<release>-<chart>-...`).
func (m *Model) openReleaseObjects(row resources.Row) tea.Cmd {
	cmd := m.switchKind(kube.KindPod)
	m.setFilter(row.Name)
	m.originKind, m.originName = kube.KindHelmRelease, row.Name
	return cmd
}

// openSelectedHelmValues pushes 18a's 'v' — the selected release's decoded
// values in the read-only YAML viewer. ok is false when values aren't
// wired, nothing's selected, or the release's full data hasn't loaded yet,
// so 'v' stays a no-op rather than pushing a broken screen.
func (m Model) openSelectedHelmValues() (tea.Model, tea.Cmd, bool) {
	if m.openHelmValues == nil || m.kind != kube.KindHelmRelease {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	release, ok := m.helmReleases[row.Name]
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openHelmValues(release, m.width, m.height)
	return task, cmd, task != nil
}

// openSelectedHelmHistory pushes 18a's 'h' — the selected release's full
// revision rail.
func (m Model) openSelectedHelmHistory() (tea.Model, tea.Cmd, bool) {
	if m.openHelmHistory == nil || m.kind != kube.KindHelmRelease {
		return nil, nil, false
	}
	row, ok := m.selectedRow()
	if !ok {
		return nil, nil, false
	}
	task, cmd := m.openHelmHistory(row.Namespace, row.Name, m.width, m.height)
	return task, cmd, task != nil
}

// beginRollback confirms rolling row back to its previous revision —
// verbs.Rollback's Tier (TierInline, escalated to TierModal in PROD by
// verbs.TierFor — "Rollback inherits 8b friction"). Executes through
// kube.Mutator.HelmRollback like every other write verb.
func (m *Model) beginRollback(row resources.Row) tea.Cmd {
	release := m.helmReleases[row.Name]
	toRevision := release.Revision - 1 // 0 once Revision is 1, which HelmRollback/HelmRollbackCommandString both read as "Helm's own default: the previous revision"
	tier := verbs.TierFor(verbs.Rollback, m.isProd())
	return m.actions.Begin(tier, tui.TaskAction{
		ID:    "rollback-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Rollback %s to revision %d?", row.Name, toRevision),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindHelmRelease),
			ResourceName: row.Name,
			Namespace:    row.Namespace,
			Verb:         "rollback",
			IsMutating:   true,
			Revision:     toRevision,
		},
	})
}

// rollbackPrompt renders 18a's inline (non-PROD) confirm line — the
// generic actions.Controller.Prompt() plus the exact `helm rollback` command
// that will run (18a: "shell out to helm with a will run line").
func rollbackPrompt(scope tui.TaskScope) string {
	// Kept short deliberately: insetChromeLine (tui/chrome.go) drops the
	// whole RightNote rather than truncating it when the keybar line
	// overflows width, so this stays terser than Controller.Prompt()'s own
	// default "Rollback HelmRelease ns/name? (y) confirm (n) cancel" despite
	// carrying more information (the actual command).
	return kube.HelmRollbackCommandString(scope.Namespace, scope.ResourceName, scope.Revision)
}

// rollbackDetail renders the PROD modal's detail line (nodes.go's
// confirmBody) — the same "will run" command string, shown alongside the
// confirm card instead of the keybar's single line.
func rollbackDetail(scope tui.TaskScope) string {
	return "will run: " + kube.HelmRollbackCommandString(scope.Namespace, scope.ResourceName, scope.Revision)
}
