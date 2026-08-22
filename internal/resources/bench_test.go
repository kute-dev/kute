package resources

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/kute-dev/kute/internal/kube"
)

// BenchmarkList measures project-then-sort over a whole namespace, which is
// what runs on every watch-driven reload of the browse list.
//
// The comparator is the interesting part: it lowercases both operands on every
// comparison, so the allocation count grows as n·log n rather than n. Names
// are deliberately mixed-case and share a long prefix, which is what real pod
// names look like and what makes the comparator do its full work.
func BenchmarkList(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("pods=%d", n), func(b *testing.B) {
			objs := make([]runtime.Object, 0, n)
			for i := range n {
				objs = append(objs, &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      fmt.Sprintf("API-Server-Worker-%05d-abcde", (i*7919)%n),
						Namespace: "default",
					},
					Status: corev1.PodStatus{Phase: corev1.PodRunning},
				})
			}
			src := fakeLister{objs: map[kube.ResourceKind][]runtime.Object{kube.KindPod: objs}}
			desc, ok := DefaultRegistry().Descriptor(kube.KindPod)
			if !ok {
				b.Fatal("no Pod descriptor")
			}
			ctx := b.Context()

			b.ReportAllocs()
			for b.Loop() {
				if _, err := List(ctx, src, desc, "default"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
