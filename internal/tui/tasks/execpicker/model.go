// Package execpicker is 10a (docs/design/README.md §10a): the small
// centered panel 'x' pushes from tasks/browse or tasks/poddetail when the
// selected pod has more than one container — single-container pods exec
// immediately without this screen (mvp-plan.md §Phase 8, "skipped entirely
// for single-container pods"). Enter suspends the program and hands the tty
// to `kubectl exec` (kube.ExecSpec via tea.ExecProcess); a clean exit pops
// back to the pod that opened the picker, a non-zero exit shows a feedback
// line in place so the user can pick another container or back out.
package execpicker

import (
	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// Config are execpicker's dependencies, per repo convention (package-local
// Config struct, New fills zero values). No cluster seam is needed — the
// picker only spawns a kubectl subprocess, it never reads from the cluster.
type Config struct {
	Session    *tui.Session
	Namespace  string
	PodName    string
	Containers []kube.ContainerInfo
}

type Model struct {
	width, height int

	session    *tui.Session
	namespace  string
	podName    string
	containers []kube.ContainerInfo

	selected int
	// feedback is set after a non-zero kubectl exec exit (docs/design
	// README.md §10a's callback contract) — empty otherwise.
	feedback string

	// conn is the last kube.ConnStateMsg forwarded by the root shell — the
	// header badge's real connection state (never a hardcoded "live").
	conn kube.ConnState
}

func New(cfg Config) Model {
	return Model{
		width:      tui.DefaultWidth,
		height:     tui.DefaultHeight,
		session:    cfg.Session,
		namespace:  cfg.Namespace,
		podName:    cfg.PodName,
		containers: cfg.Containers,
	}
}

func (m Model) Init() tea.Cmd { return nil }

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}
