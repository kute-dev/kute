package verbs

// Motion verbs — the vim scroll vocabulary every list and scrollable view
// shares. j/k stay with Move (and stay each screen's own business, since
// 14d spends them on siblings); these are the larger jumps. Screens match
// them through MotionFor in their browse-mode key switch only — never while
// a text buffer is focused, where ctrl+b/ctrl+f/ctrl+e are the textfield's
// own cursor keys.
//
// gg has no press of its own: g opens the goto palette, and the root shell
// recognises a lone second g and delivers it to the task as "home"
// (internal/tui/gg.go). So a screen that handles Top handles gg for free.
var (
	// TopBottom is gg / G — the first and last row.
	TopBottom = Verb{ID: "top-bottom", Key: "gg/G", Label: "top/bottom", Presses: []string{"home", "end", "G"}}
	// HalfPage is ctrl+d / ctrl+u.
	HalfPage = Verb{ID: "half-page", Key: "ctrl+d/u", Label: "half page", Presses: []string{"ctrl+d", "ctrl+u"}}
	// FullPage is ctrl+f / ctrl+b, with pgdown/pgup as the same motion.
	FullPage = Verb{ID: "full-page", Key: "ctrl+f/b", Label: "page", Presses: []string{"ctrl+f", "ctrl+b", "pgdown", "pgup"}}
	// LineScroll is ctrl+e / ctrl+y — one line, the cursor-free nudge.
	LineScroll = Verb{ID: "line-scroll", Key: "ctrl+e/y", Label: "scroll line", Presses: []string{"ctrl+e", "ctrl+y"}}
)

var motionVerbs = []Verb{TopBottom, HalfPage, FullPage, LineScroll}

// Motion is one scroll step resolved from a key press.
type Motion int

const (
	MotionLineDown Motion = iota + 1
	MotionLineUp
	MotionHalfDown
	MotionHalfUp
	MotionPageDown
	MotionPageUp
	MotionTop
	MotionBottom
)

var motionByPress = map[string]Motion{
	"ctrl+e": MotionLineDown,
	"ctrl+y": MotionLineUp,
	"ctrl+d": MotionHalfDown,
	"ctrl+u": MotionHalfUp,
	"ctrl+f": MotionPageDown,
	"pgdown": MotionPageDown,
	"ctrl+b": MotionPageUp,
	"pgup":   MotionPageUp,
	"home":   MotionTop,
	"end":    MotionBottom,
	"G":      MotionBottom,
}

// MotionFor resolves a key press (tea.KeyPressMsg.String()) to a Motion.
func MotionFor(press string) (Motion, bool) {
	m, ok := motionByPress[press]
	return m, ok
}

// Delta is how far m moves on a view page rows tall, in rows — Top and
// Bottom return a distance large enough that any clamp lands on the end.
func (m Motion) Delta(page int) int {
	page = max(page, 1)
	half := max(page/2, 1)
	const far = 1 << 30
	switch m {
	case MotionLineDown:
		return 1
	case MotionLineUp:
		return -1
	case MotionHalfDown:
		return half
	case MotionHalfUp:
		return -half
	case MotionPageDown:
		return page
	case MotionPageUp:
		return -page
	case MotionTop:
		return -far
	case MotionBottom:
		return far
	}
	return 0
}

// Apply moves cur by m within [0, n-1] on a view page rows tall — the whole
// job for a cursor-driven list. n == 0 returns 0.
func (m Motion) Apply(cur, n, page int) int {
	if n <= 0 {
		return 0
	}
	next := cur + m.Delta(page)
	return min(max(next, 0), n-1)
}
