package tui

import (
	"testing"

	"github.com/kute-dev/kute/internal/kube"
)

// TestCommandTargetFollowsActiveContext is the 04 H1/08 H1 regression at the
// Session seam: every kubectl/helm subprocess a screen launches is pinned to
// the context kute is browsing (which an in-app switch or a restored recent
// can make differ from the kubeconfig's current-context), and never to a
// demo-mode context name no kubectl could resolve.
func TestCommandTargetFollowsActiveContext(t *testing.T) {
	t.Parallel()
	sess := &Session{Cluster: &kube.Cluster{}, Kubeconfig: "/etc/kute/config", Location: Location{Context: "staging"}}
	if got, want := sess.CommandTarget(), (kube.CommandTarget{Kubeconfig: "/etc/kute/config", Context: "staging"}); got != want {
		t.Fatalf("CommandTarget = %+v, want %+v", got, want)
	}
	sess.Location.Context = "prod-eu" // what SwitchContextMsg writes on success
	if got := sess.CommandTarget().Context; got != "prod-eu" {
		t.Fatalf("CommandTarget after switch = %q, want prod-eu", got)
	}
	demo := &Session{Location: Location{Context: "kute-demo"}}
	if got := demo.CommandTarget(); got != (kube.CommandTarget{}) {
		t.Fatalf("demo CommandTarget = %+v, want zero", got)
	}
	var nilSess *Session
	if got := nilSess.CommandTarget(); got != (kube.CommandTarget{}) {
		t.Fatalf("nil-session CommandTarget = %+v, want zero", got)
	}
}
