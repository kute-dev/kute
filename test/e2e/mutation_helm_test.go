//go:build e2e

package e2e

import (
	"context"
	"os/exec"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func TestHelmRollbackCreatesANewVisibleRevision(t *testing.T) {
	RequireCluster(t)
	if _, err := exec.LookPath("helm"); err != nil {
		t.Fatalf("helm is required for Phase 3 mutation coverage; run mise install: %v", err)
	}
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	v1, err := client.CoreV1().Secrets(Namespace).Get(ctx, "sh.helm.release.v1.shop.v1", metav1.GetOptions{})
	if err != nil {
		cancel()
		t.Fatalf("getting Helm revision 1: %v", err)
	}
	v2, err := client.CoreV1().Secrets(Namespace).Get(ctx, "sh.helm.release.v1.shop.v2", metav1.GetOptions{})
	cancel()
	if err != nil {
		t.Fatalf("getting Helm revision 2: %v", err)
	}
	cleanupHelmRollback(t, client, v1.DeepCopy(), v2.DeepCopy())

	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "helm", "Helm Releases")
	a.WaitForAll(Settle, "shop", "shop 1.3.0", "2.5.0", "deployed")
	a.Press("h")
	a.WaitForAll(Settle, "1.3.0", "1.2.0", "superseded")
	a.Down() // revision 1
	a.Press("R")
	a.WaitForAll(Settle, "CONFIRM", "helm rollback shop 1")
	a.Press("y")

	var revision3 *corev1.Secret
	waitForAPI(t, "Helm rollback revision", func(ctx context.Context) (bool, error) {
		secret, err := client.CoreV1().Secrets(Namespace).Get(ctx, "sh.helm.release.v1.shop.v3", metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		revision3 = secret
		return secret.Labels["status"] == "deployed" && secret.Labels["version"] == "3", nil
	})
	if revision3.ResourceVersion == "" || revision3.ResourceVersion == v2.ResourceVersion {
		t.Fatalf("rollback revision resourceVersion = %q, prior = %q", revision3.ResourceVersion, v2.ResourceVersion)
	}
	a.WaitForAll(Settle, "HELM", "3 (current)", "deployed")
}

func cleanupHelmRollback(t *testing.T, client kubernetes.Interface, v1, v2 *corev1.Secret) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = client.CoreV1().Secrets(Namespace).Delete(ctx, "sh.helm.release.v1.shop.v3", metav1.DeleteOptions{})
		_ = client.AppsV1().Deployments(Namespace).Delete(ctx, "shop-web", metav1.DeleteOptions{})
		for _, original := range []*corev1.Secret{v1, v2} {
			current, err := client.CoreV1().Secrets(Namespace).Get(ctx, original.Name, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				original.ResourceVersion = ""
				if _, err := client.CoreV1().Secrets(Namespace).Create(ctx, original, metav1.CreateOptions{}); err != nil {
					t.Errorf("recreating %s: %v", original.Name, err)
				}
				continue
			}
			if err != nil {
				t.Errorf("getting %s for restore: %v", original.Name, err)
				continue
			}
			original.ResourceVersion = current.ResourceVersion
			if _, err := client.CoreV1().Secrets(Namespace).Update(ctx, original, metav1.UpdateOptions{}); err != nil {
				t.Errorf("restoring %s: %v", original.Name, err)
			}
		}
	})
}
