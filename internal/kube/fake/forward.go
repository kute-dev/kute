// Port-forward seams for --demo/tests: a PodResolver that resolves
// Service/Deployment targets against the fake cluster's own seeded objects
// (same selector-matching logic the real kube.ClientsetPodResolver runs
// against a live clientset), and a ForwardDialer whose Tunnel binds a real
// local listener — so 13a's busy-port detection still probes a real OS
// port in demo mode — but never proxies real traffic, since there's no
// backing pod to actually reach.
package fake

import (
	"context"
	"fmt"
	"net"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
)

type podResolver struct{ cluster *Cluster }

// NewPodResolver builds the fake kube.PodResolver bound to cluster.
func NewPodResolver(cluster *Cluster) kube.PodResolver { return podResolver{cluster: cluster} }

func (r podResolver) ResolveForwardPod(ctx context.Context, target kube.ForwardTarget) (string, error) {
	switch target.Kind {
	case kube.KindPod:
		return target.Name, nil
	case kube.KindService:
		objs, _ := r.cluster.ListRaw(ctx, kube.KindService, target.Namespace)
		for _, obj := range objs {
			if svc, ok := obj.(*corev1.Service); ok && svc.Name == target.Name {
				return r.firstMatchingPod(ctx, target.Namespace, svc.Spec.Selector)
			}
		}
		return "", fmt.Errorf("service %q not found", target.Name)
	case kube.KindDeployment:
		objs, _ := r.cluster.ListRaw(ctx, kube.KindDeployment, target.Namespace)
		for _, obj := range objs {
			dep, ok := obj.(*appsv1.Deployment)
			if !ok || dep.Name != target.Name {
				continue
			}
			sel := map[string]string{}
			if dep.Spec.Selector != nil {
				sel = dep.Spec.Selector.MatchLabels
			}
			return r.firstMatchingPod(ctx, target.Namespace, sel)
		}
		return "", fmt.Errorf("deployment %q not found", target.Name)
	default:
		return "", fmt.Errorf("port-forward is not supported for kind %s", target.Kind)
	}
}

func (r podResolver) firstMatchingPod(ctx context.Context, namespace string, selector map[string]string) (string, error) {
	if len(selector) == 0 {
		return "", fmt.Errorf("target has no selector to resolve a backing pod")
	}
	objs, _ := r.cluster.ListRaw(ctx, kube.KindPod, namespace)
	for _, obj := range objs {
		pod, ok := obj.(*corev1.Pod)
		if !ok {
			continue
		}
		match := true
		for k, v := range selector {
			if pod.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return pod.Name, nil
		}
	}
	return "", fmt.Errorf("no pods match the target's selector")
}

type dialer struct{}

// NewForwardDialer builds the fake kube.ForwardDialer.
func NewForwardDialer() kube.ForwardDialer { return dialer{} }

func (dialer) Dial(_ string, _ string, localPort int, _ int32) (kube.Tunnel, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return nil, err
	}
	return &tunnel{ln: ln}, nil
}

// tunnel accepts and immediately closes local connections — enough to make
// the local port genuinely bound (so busy-port detection has something
// real to probe) without pretending to proxy data nothing backs.
type tunnel struct {
	ln        net.Listener
	closeOnce sync.Once
}

func (t *tunnel) Run(activity func()) error {
	for {
		conn, err := t.ln.Accept()
		if err != nil {
			return err
		}
		if activity != nil {
			activity()
		}
		conn.Close()
	}
}

func (t *tunnel) Close() {
	t.closeOnce.Do(func() { t.ln.Close() })
}
