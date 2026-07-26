// Package helmhistory is 18a's `h` — a Helm release's full revision rail,
// reusing 16b's "deployment revisions as a vertical rail with the current
// one highlighted" idiom (docs/design README.md §18a: "h history (16b's
// rail idiom)"). Every revision is decoded straight from its own
// helm.sh/release.v1 Secret (kube.HelmReleaseHistory) — no helm binary
// needed to browse, same as 18a's list. 'R' rolls back to the selected
// revision, through the same kube.Mutator.HelmRollback + 8b-style friction
// tasks/browse's own 'R' uses.
package helmhistory

import (
	"context"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/actions"
	"github.com/kute-dev/kute/internal/tui/components"
)

// Config are helmhistory's dependencies, per repo convention (package-local
// Config struct, interface-typed fields, New fills zero values).
type Config struct {
	Session     *tui.Session
	Lister      resources.RawLister
	Mutator     kube.Mutator
	Namespace   string
	Name        string
	LoadTimeout time.Duration
}

type Model struct {
	width, height int

	session *tui.Session
	lister  resources.RawLister
	mutator kube.Mutator
	actions actions.Controller
	timeout time.Duration

	namespace string
	name      string

	revisions []kube.HelmRelease
	selected  int
	// offset keeps the selected revision within the table's rendered
	// viewport — mirrors nodedetail/routetable's own clampOffset/
	// tableDataRows pattern (update.go's clampOffset).
	offset int

	conn kube.ConnState

	reloadEpoch int

	state    tui.TaskState
	feedback string
	spinner  spinner.Model

	// now is wall-clock as of the last spinner tick / conn-state message.
	// The loading header's "· 0.4s" counter is computed from this rather
	// than read from the clock in Render, which has to stay pure —
	// f(model, theme, size), no I/O and no clock reads.
	now time.Time
	// loadStartedAt is when the current loading spell began, so the counter
	// measures the wait the user is actually sitting through rather than
	// restarting on each cache-sync retry.
	loadStartedAt time.Time
}

// loadedMsg carries one load()'s result.
type loadedMsg struct {
	epoch     int
	revisions []kube.HelmRelease
	err       error
}

func New(cfg Config) Model {
	if cfg.LoadTimeout == 0 {
		cfg.LoadTimeout = 10 * time.Second
	}
	state := tui.TaskStateLoading
	feedback := "Loading " + cfg.Name + " history..."
	if cfg.Lister == nil {
		state = tui.TaskStateError
		feedback = "no cluster connection"
	}
	started := time.Now()
	return Model{
		width:     tui.DefaultWidth,
		height:    tui.DefaultHeight,
		session:   cfg.Session,
		lister:    cfg.Lister,
		mutator:   cfg.Mutator,
		actions:   actions.New(cfg.Mutator),
		timeout:   cfg.LoadTimeout,
		namespace: cfg.Namespace,
		name:      cfg.Name,
		state:     state,
		feedback:  feedback,
		spinner:   components.NewSpinner(),

		now:           started,
		loadStartedAt: started,
	}
}

func (m Model) Init() tea.Cmd {
	if m.lister == nil {
		return nil
	}
	return tea.Batch(m.load(), m.spinner.Tick)
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}

func (m Model) load() tea.Cmd {
	epoch := m.reloadEpoch
	lister := m.lister
	namespace := m.namespace
	name := m.name
	timeout := m.timeout
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		secrets, err := helmSecrets(ctx, lister, namespace)
		if err != nil {
			return loadedMsg{epoch: epoch, err: err}
		}
		// Narrow to this release before decoding: the cache is cluster-wide,
		// this runs again on every Secret change and every cache-sync retry,
		// and decoding a revision is base64 + gunzip + JSON + a YAML
		// re-marshal that HelmReleaseHistory would then throw away.
		mine := kube.HelmReleaseSecretsFor(secrets, namespace, name)
		history := kube.HelmReleaseHistory(kube.DecodeHelmReleases(mine), namespace, name)
		return loadedMsg{epoch: epoch, revisions: history}
	}
}

func (m Model) selectedRevision() (kube.HelmRelease, bool) {
	if m.selected < 0 || m.selected >= len(m.revisions) {
		return kube.HelmRelease{}, false
	}
	return m.revisions[m.selected], true
}

// isProd mirrors browse's own isProd (delete.go) — the same PROD-tag source
// (Session.Config.IsProd), duplicated per the repo's package-local-seam
// convention rather than exported from browse (which task packages don't
// import — see poddetail/nodedetail's own copies of this exact pattern... a
// screen's write-confirm policy always reads its own Session directly).
func (m Model) isProd() bool {
	if m.session == nil {
		return false
	}
	return m.session.Config.IsProd(m.session.Location.Context)
}

// HelmSecretLister is implemented by a lister backed by a Secret cache
// filtered to helm.sh/release.v1 — see app.helmAwareLister's copy. Declared
// here, in the package that consumes it, per the repo's seam convention.
//
// Exported so app can assert its lister decorators against it at compile
// time: this is an optional seam, so a decorator that forgets to forward it
// doesn't fail to build — it just misses the type assertion below and takes
// the read-every-Secret fallback, silently.
type HelmSecretLister interface {
	ListHelmReleaseSecrets(ctx context.Context, namespace string) ([]runtime.Object, error)
}

// helmSecrets reads a namespace's release Secrets from the narrowest source
// the lister offers. The fallback reads every Secret in the namespace, which
// is correct but pulls the largest kind on most clusters to find a handful of
// revisions.
func helmSecrets(ctx context.Context, lister resources.RawLister, namespace string) ([]runtime.Object, error) {
	if hs, ok := lister.(HelmSecretLister); ok {
		return hs.ListHelmReleaseSecrets(ctx, namespace)
	}
	return lister.ListRaw(ctx, kube.KindSecret, namespace)
}
