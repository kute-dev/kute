package browse

import (
	"fmt"
	"testing"

	"github.com/kute-dev/kute/internal/kube"
	"k8s.io/apimachinery/pkg/runtime"
)

// benchModel builds a loaded browse list of n pods at a realistic terminal
// size. Nothing here is asserted — the point is the render path below.
func benchModel(b *testing.B, n int) Model {
	b.Helper()
	objs := make([]runtime.Object, 0, n)
	for i := range n {
		objs = append(objs, pod("default", fmt.Sprintf("api-server-%03d", i)))
	}
	lister := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: objs}}

	m := New(Config{Session: newSession(), Lister: lister})
	m.SetSize(120, 36)
	// Drain Init's load without the *testing.T-shaped step helper.
	updated, _ := m.Update(m.Init()())
	return *updated.(*Model)
}

// BenchmarkBrowseRender measures a full frame of the one resting screen —
// the allocation path a TUI actually spends its life in, since every watch
// event redraws it. Run with -benchmem; the alloc count is the number that
// matters more than ns/op.
func BenchmarkBrowseRender(b *testing.B) {
	for _, n := range []int{50, 500} {
		b.Run(fmt.Sprintf("pods=%d", n), func(b *testing.B) {
			m := benchModel(b, n)
			b.ReportAllocs()
			for b.Loop() {
				_ = m.Render()
			}
		})
	}
}
