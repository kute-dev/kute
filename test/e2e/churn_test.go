//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExternalResourceChurnKeepsRowsAndIdentityHonest(t *testing.T) {
	RequireCluster(t)
	client := e2eClientset(t)
	ctx := context.Background()
	namespace := fmt.Sprintf("kute-churn-%d", time.Now().UnixNano())
	if _, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{}); err != nil {
		t.Fatalf("creating churn namespace: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_ = client.CoreV1().Namespaces().Delete(cleanupCtx, namespace, metav1.DeleteOptions{})
	})

	a := Launch(t, WithNamespace(namespace))
	a.WaitLoaded(Connect)
	a.gotoKind(t, "secrets", "Secrets")
	a.WaitLoaded(Settle)

	createSecret := func(name, marker string) *corev1.Secret {
		secret, err := client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{"marker": marker},
		}, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		return secret
	}
	first := createSecret("churn-a", "old-identity")
	createSecret("churn-b", "neighbor")
	a.WaitForAll(Settle, "churn-a", "churn-b")

	secret, err := client.CoreV1().Secrets(namespace).Get(ctx, "churn-a", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	secret.StringData = map[string]string{"projected": "second-key"}
	if _, err := client.CoreV1().Secrets(namespace).Update(ctx, secret, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("patching projected Secret data count: %v", err)
	}
	a.WaitFor("churn-a", Settle)
	if !rowContains(a.Frame(), "churn-a", "2") {
		frame, ok := a.poll(func(f string) bool { return rowContains(f, "churn-a", "2") }, Settle)
		if !ok {
			t.Fatalf("projected Secret data count never changed to 2:\n%s", frame)
		}
	}

	if err := client.CoreV1().Secrets(namespace).Delete(ctx, "churn-a", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	a.WaitGone("churn-a", Settle)
	a.Enter() // selection clamps to churn-b
	a.WaitForAll(Settle, "secret/churn-b", "marker")
	a.Enter() // decoded values are visible only inside the explicit edit buffer
	a.WaitFor("neighbor", Settle)
	a.Esc() // cancel the edit
	a.Esc() // return to browse
	a.WaitFor("Secrets", Settle)

	if err := client.CoreV1().Secrets(namespace).Delete(ctx, "churn-b", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	a.WaitGone("churn-b", Settle)
	a.WaitFor("no secrets in", Settle)

	recreated := createSecret("churn-a", "new-identity")
	if recreated.UID == first.UID {
		t.Fatalf("recreated Secret reused UID %s", recreated.UID)
	}
	a.WaitFor("churn-a", Settle)
	a.Enter()
	a.WaitForAll(Settle, "secret/churn-a", "marker")
	a.Enter()
	a.WaitFor("new-identity", Settle)
	a.Never("old-identity", time.Second)
}

func TestPodDetailShowsGoneAfterExternalDelete(t *testing.T) {
	RequireCluster(t)
	name := fmt.Sprintf("detail-gone-%d", time.Now().UnixNano())
	createDisposablePod(t, name, map[string]string{"churn": "detail"})
	a := Launch(t)
	a.WaitFor(name, Connect)
	a.filterTo(t, name)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)

	ctx, cancel := context.WithTimeout(context.Background(), Settle)
	defer cancel()
	zero := int64(0)
	err := e2eClientset(t).CoreV1().Pods(Namespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero})
	if err != nil && !apierrors.IsNotFound(err) {
		t.Fatal(err)
	}
	a.WaitForAll(Settle, "Pod deleted", "no longer exists")
}

func rowContains(frame, name, value string) bool {
	for _, line := range strings.Split(frame, "\n") {
		if strings.Contains(line, name) && strings.Contains(line, value) {
			return true
		}
	}
	return false
}
