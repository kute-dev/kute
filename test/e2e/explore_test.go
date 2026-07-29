//go:build e2e

package e2e

import "testing"

func TestExploreRestricted(t *testing.T) {
	a := Launch(t, WithKubeconfig(PartialKubeconfigPath()))
	a.WaitFor("kute-e2e", Connect)
	t.Logf("pods:\n%s", a.Frame())

	for _, q := range []string{"deployments", "secrets", "events", "nodes", "ingresses", "helm"} {
		a.Press("g")
		a.Type(q)
		a.Enter()
		a.WaitLoaded(Settle)
		t.Logf("%s:\n%s", q, a.Frame())
	}
}
