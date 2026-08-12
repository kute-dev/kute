//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// TestProdDeleteRequiresTheTypedName is the destructive-action policy at its
// sharpest: delete is tiered, and in a PROD context the inline y/N is
// replaced by the type-the-name modal. PROD comes from config.yaml's
// prodContexts and from nothing else — never a name heuristic — so the test
// writes it into the isolated config the harness points HOME at, which is the
// only honest way to reach this state.
func TestProdDeleteRequiresTheTypedName(t *testing.T) {
	ctxName := ContextName(t)
	a := Launch(t, WithProdContexts(ctxName))
	a.WaitFor("api-", Connect)

	pod := a.selectAPIPod(t)
	a.Press("D")

	// The modal, not the inline card: the name has to be typed, and the
	// context is called out as PROD.
	a.WaitForAll(Settle, "PROD CONTEXT", TypeToConfirm, pod)

	// A wrong name does not delete. This is the assertion the whole modal
	// exists for — enter with a mismatched name has to do nothing at all.
	a.Type("definitely-not-the-pod-name")
	a.Enter()
	if frame := a.Frame(); !strings.Contains(frame, TypeToConfirm) {
		t.Fatalf("enter on a mismatched name closed the confirm:\n%s", frame)
	}
	assertPodExists(t, pod)

	// And esc gets out without deleting either.
	a.Esc()
	a.WaitGone(TypeToConfirm, Settle)
	a.WaitFor(pod, Settle)
	assertPodExists(t, pod)
}

// TestNonProdDeleteIsInline is the other side of the tier: the same key on
// the same object in a context that is not marked prod gets the inline y/N,
// never the modal. Without this, a delete flow that had escalated everything
// to the modal would look "safe" and pass every test.
func TestNonProdDeleteIsInline(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	pod := a.selectAPIPod(t)
	a.Press("D")

	a.WaitFor("CONFIRM", Settle)
	frame := a.Frame()
	if strings.Contains(frame, TypeToConfirm) {
		t.Fatalf("a non-prod delete escalated to the type-the-name modal:\n%s", frame)
	}
	if strings.Contains(frame, "PROD CONTEXT") {
		t.Fatalf("a context absent from prodContexts rendered as PROD:\n%s", frame)
	}

	// n declines, and nothing is deleted.
	a.Press("n")
	a.WaitGone("CONFIRM", Settle)
	assertPodExists(t, pod)
}

// assertPodExists checks the cluster directly rather than the frame: a screen
// can go on rendering a row from a cache for a moment after the object behind
// it is gone, and "was it actually deleted" is a question only the apiserver
// can answer.
func assertPodExists(t *testing.T, name string) {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatalf("building a REST config from %s: %v", KubeconfigPath(), err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a clientset: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := client.CoreV1().Pods(Namespace).Get(ctx, name, metav1.GetOptions{}); err != nil {
		t.Fatalf("pod %s/%s is gone, and nothing in this test confirmed a delete: %v", Namespace, name, err)
	}
}
