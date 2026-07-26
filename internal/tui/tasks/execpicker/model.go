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
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/tui"
)

// ShellDetector answers which shells a container actually has, for 10a's
// right-aligned shells column. Declared here rather than in kube, per the
// repo's consuming-interface convention; kube.DetectShells is the real
// implementation and kube/fake supplies the demo one.
type ShellDetector interface {
	DetectShells(ctx context.Context, namespace, pod, container string) ([]string, error)
}

// shellProbeTimeout bounds one container's probe. Generous enough for a
// laggy link, short enough that a wedged exec can't leave the column saying
// "checking…" for the life of the screen.
const shellProbeTimeout = 5 * time.Second

// shellResult is one container's detection outcome. Absent from Model.shells
// entirely means still in flight; err non-nil means the probe couldn't run
// (rendered as unknown, never as "no shell"); a nil err with no shells is a
// real "this container has none".
type shellResult struct {
	shells []string
	err    error
}

// Config are execpicker's dependencies, per repo convention (package-local
// Config struct, New fills zero values). Shells may be nil — the picker then
// renders its shells column as unknown rather than probing.
type Config struct {
	Session    *tui.Session
	Namespace  string
	PodName    string
	Containers []kube.ContainerInfo
	Shells     ShellDetector
}

type Model struct {
	width, height int

	session    *tui.Session
	namespace  string
	podName    string
	containers []kube.ContainerInfo

	shells   ShellDetector
	detected map[string]shellResult

	selected int
	// feedback is set after a non-zero kubectl exec exit (docs/design
	// README.md §10a's callback contract), or when a key is refused while
	// offline — empty otherwise.
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
		shells:     cfg.Shells,
		detected:   map[string]shellResult{},
	}
}

// Init probes every container's shells at once. The picker only ever renders
// for a multi-container pod, so this is a handful of one-shot reads on an
// explicitly-opened screen, not a poll — and every row renders immediately
// with "checking…" rather than blocking on them.
func (m Model) Init() tea.Cmd {
	if m.shells == nil {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(m.containers))
	for _, c := range m.containers {
		cmds = append(cmds, m.detectShellsCmd(c.Name))
	}
	return tea.Batch(cmds...)
}

// detectShellsCmd probes one container off the update loop.
func (m Model) detectShellsCmd(container string) tea.Cmd {
	detector, namespace, pod := m.shells, m.namespace, m.podName
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), shellProbeTimeout)
		defer cancel()
		shells, err := detector.DetectShells(ctx, namespace, pod, container)
		return shellsMsg{container: container, shells: shells, err: err}
	}
}

// preferredShell is the shell enter will actually run for the container at
// index i: the first detected candidate (kube.ShellCandidates order, bash
// first). Empty when detection is pending, failed, or found none — which
// keeps kube.ExecSpec on its in-container bash-then-sh fallback.
func (m Model) preferredShell(i int) string {
	if i < 0 || i >= len(m.containers) {
		return ""
	}
	res, ok := m.detected[m.containers[i].Name]
	if !ok || res.err != nil || len(res.shells) == 0 {
		return ""
	}
	return res.shells[0]
}

func (m *Model) SetSize(width, height int) {
	size := tui.NormalizeSize(width, height)
	m.width, m.height = size.Width, size.Height
}
