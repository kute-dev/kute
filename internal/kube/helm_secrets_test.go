package kube

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stesting "k8s.io/client-go/testing"
)

func secretOfType(name, namespace string, typ corev1.SecretType) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       typ,
	}
}

// secretListFieldSelectors returns the field selector carried by each
// recorded Secret list, which is what proves the narrowing happened at the
// API server rather than after the bytes arrived.
func secretListFieldSelectors(cs interface{ Actions() []k8stesting.Action }) []string {
	var out []string
	for _, a := range cs.Actions() {
		if a.GetVerb() != "list" || a.GetResource().Resource != "secrets" {
			continue
		}
		la, ok := a.(k8stesting.ListAction)
		if !ok {
			continue
		}
		out = append(out, la.GetListRestrictions().Fields.String())
	}
	return out
}

// TestListHelmReleaseSecretsFiltersServerSide is the whole point of the
// separate cache. Releases used to be decoded from the shared Secret cache,
// so listing them pulled every Secret in the namespace — image-pull
// credentials, TLS material, ServiceAccount tokens — to find a handful of
// release revisions. The request must carry a field selector so the server
// never sends the rest.
func TestListHelmReleaseSecretsFiltersServerSide(t *testing.T) {
	c, cs := newLazyTestCluster(
		secretOfType("sh.helm.release.v1.web.v1", "default", HelmReleaseSecretType),
		secretOfType("registry-creds", "default", corev1.SecretTypeDockerConfigJson),
		secretOfType("tls-cert", "default", corev1.SecretTypeTLS),
	)
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := c.ListHelmReleaseSecrets(context.Background(), "default"); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	waitFor(t, "the release-secret informer to list", func() bool {
		return len(secretListFieldSelectors(cs)) > 0
	})

	want := "type=" + string(HelmReleaseSecretType)
	for _, got := range secretListFieldSelectors(cs) {
		if got != want {
			t.Fatalf("Secret list carried field selector %q, want %q — an unfiltered list pulls every Secret in the namespace", got, want)
		}
	}
}

// TestListHelmReleaseSecretsDoesNotStartTheSharedSecretCache: the two caches
// are separate on purpose. Opening the Helm list must not drag in every
// Secret by starting the shared informer as a side effect.
func TestListHelmReleaseSecretsDoesNotStartTheSharedSecretCache(t *testing.T) {
	c, cs := newLazyTestCluster(
		secretOfType("sh.helm.release.v1.web.v1", "default", HelmReleaseSecretType),
	)
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := c.ListHelmReleaseSecrets(context.Background(), "default"); err != nil {
		t.Fatalf("ListHelmReleaseSecrets: %v", err)
	}
	waitFor(t, "the release-secret informer to list", func() bool {
		return len(secretListFieldSelectors(cs)) > 0
	})

	c.mu.Lock()
	_, sharedStarted := c.kindInformers[KindSecret]
	c.mu.Unlock()
	if sharedStarted {
		t.Fatal("listing Helm releases started the shared Secret informer; that pulls every Secret in the cluster")
	}

	// An unfiltered list would show up here as a selector-less Secret list.
	for _, sel := range secretListFieldSelectors(cs) {
		if sel == "" {
			t.Fatal("an unfiltered Secret list was issued alongside the filtered one")
		}
	}
}

// TestSharedSecretCacheStaysUnfiltered: browsing Secrets is a screen about
// Secrets, so that cache must keep holding all of them. The filter belongs
// to the release cache alone.
func TestSharedSecretCacheStaysUnfiltered(t *testing.T) {
	c, cs := newLazyTestCluster(
		secretOfType("registry-creds", "default", corev1.SecretTypeDockerConfigJson),
	)
	defer c.Stop()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if _, err := c.ListRaw(context.Background(), KindSecret, "default"); err != nil {
		t.Fatalf("ListRaw(Secret): %v", err)
	}
	waitFor(t, "the shared Secret informer to list", func() bool {
		return len(secretListFieldSelectors(cs)) > 0
	})

	for _, sel := range secretListFieldSelectors(cs) {
		if sel != "" {
			t.Fatalf("the shared Secret list carried field selector %q; browsing Secrets must show all of them", sel)
		}
	}
}

// TestHelmReleaseSecretsAfterStop mirrors the lazy-kind contract: a
// torn-down cluster reads empty rather than starting an informer that could
// never be stopped.
func TestHelmReleaseSecretsAfterStop(t *testing.T) {
	c, cs := newLazyTestCluster(
		secretOfType("sh.helm.release.v1.web.v1", "default", HelmReleaseSecretType),
	)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()

	before := len(cs.Actions())
	if _, err := c.ListHelmReleaseSecrets(context.Background(), "default"); err != nil {
		t.Fatalf("ListHelmReleaseSecrets after Stop should read empty, not error: %v", err)
	}
	if got := len(cs.Actions()); got != before {
		t.Fatalf("issued %d API actions after Stop, want 0", got-before)
	}
}
