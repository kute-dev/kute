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

// §27a and §27b are the newest screens in the app and the ones whose write
// paths had, until this file, only ever met a hand-written fake. What they
// share is a contract a unit test cannot check on its own: confirm → execute
// → refresh → show result → *remain on screen*. The remaining is the part
// that matters — `esc` is meant to be the only thing that closes these
// panels, and a commit that quietly popped back to the list would look fine
// in isolation and be wrong in use.
//
// Both tests restore what they change, so the fixtures stay as the other
// tests expect them. If one dies mid-edit, `scripts/e2e-cluster.sh fixtures`
// puts them back.

// TestConfigMapEditInPlace covers §27a's `↵` edit: the grid, the will-run
// line built from the pending change, the apply, and the screen still being
// the screen afterwards.
func TestConfigMapEditInPlace(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.openFrom(t, "configmaps", "ConfigMaps", "app-config")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "cm/app-config", "log-level", "info")

	// The consumers strip is resolved live from the workloads that mount or
	// reference this ConfigMap — the api Deployment reads log-level's
	// sibling through env.valueFrom, so this is real cluster data, not a
	// label lookup.
	a.WaitFor("deploy/api", Settle)

	// ↵ opens the value for editing in place, seeded with the current value
	// and showing what it was.
	a.Enter()
	a.WaitFor("was info", Settle)

	for range len("info") {
		a.Press("backspace")
	}
	a.Type("debug")

	// The will-run band is the command the apply is about to run, built from
	// the pending edit — §27a calls it copyable documentation.
	a.WaitFor(`kubectl patch cm/app-config`, Settle)
	a.WaitFor("debug", Settle)

	a.Enter()

	// Wait on the *result line*, not on "debug" appearing: the new value is
	// on screen before the commit too, in the edit buffer and in the
	// will-run band, so waiting for it proves nothing and races the patch.
	// "updated <key>" exists only once the apply has come back.
	a.WaitFor("updated log-level", Settle)

	// Refreshed: the grid re-reads the object and shows the committed value.
	// Remained: still the Data screen for this ConfigMap, not the list.
	a.WaitForAll(Settle, "cm/app-config", "log-level", "debug")

	// And it really is in the cluster, not just on screen.
	if got := configMapValue(t, "app-config", "log-level"); got != "debug" {
		t.Fatalf("log-level in the cluster = %q, want debug", got)
	}

	// Put it back the same way, which exercises the round trip a second time.
	a.Enter()
	a.WaitFor("was debug", Settle)
	for range len("debug") {
		a.Press("backspace")
	}
	a.Type("info")
	a.Enter()
	a.WaitFor("updated log-level", Settle)
	a.WaitForAll(Settle, "log-level", "info")

	if got := configMapValue(t, "app-config", "log-level"); got != "info" {
		t.Fatalf("log-level in the cluster = %q after restoring, want info", got)
	}
}

// TestSecretUnmaskAddAndRemoveKey covers §27b's three write-adjacent paths:
// ctrl-x unmasking, `a` adding a key, and ctrl-d removing one — against a
// real Secret, whose values are base64 on the wire and have to survive a
// decode/encode round trip that no fake exercises.
func TestSecretUnmaskAddAndRemoveKey(t *testing.T) {
	const (
		key   = "e2e-scratch"
		value = "KUTE-E2E-ADDED-VALUE"
	)

	a := Launch(t)
	a.WaitFor("api-", Connect)

	a.openFrom(t, "secrets", "Secrets", "app-secret")
	a.WaitLoaded(Settle)
	a.WaitForAll(Settle, "secret/app-secret", "api-token", "database-url")

	// Masked at rest.
	if frame := a.Frame(); strings.Contains(frame, "KUTE-E2E-SECRET-VALUE") {
		t.Fatalf("the Secret's value is visible before anything asked for it:\n%s", frame)
	}

	// ↵ decodes the real value into the edit buffer — §27b's deliberate
	// departure from a blind edit — and the round trip through base64 is the
	// half a fake cannot prove.
	a.Enter()
	a.WaitForAll(Settle, "KUTE-E2E-SECRET-VALUE", "ctrl-x")
	// ctrl-x re-masks whatever buffer is open. Plain x has to stay typeable,
	// which is why the toggle is a control key at all.
	a.Press("ctrl+x")
	a.WaitGone("KUTE-E2E-SECRET-VALUE", Settle)
	a.Esc()

	// `a` line-inserts a new key: type the key, tab to the value, type it.
	a.Press("a")
	a.Type(key)
	a.Press("tab")
	a.Type(value)
	a.Enter()

	// Same reasoning as §27a above: the key name is on screen before the
	// commit as well, on the pending "+" row, so the result line is the only
	// signal that means the apply came back. The key count moving to 3
	// confirms the grid re-read the object rather than keeping the row it
	// already had.
	a.WaitFor("added "+key, Settle)
	a.WaitForAll(Settle, "secret/app-secret", key, "3 keys")
	if got := secretValue(t, "app-secret", key); got != value {
		t.Fatalf("added key %s = %q in the cluster, want %q", key, got, value)
	}

	// The value must not have leaked into the frame on the way through —
	// §27b masks or omits it in the will-run line and every result message,
	// success or failure.
	if frame := a.Frame(); strings.Contains(frame, value) {
		t.Errorf("the added value is on screen after committing:\n%s", frame)
	}

	// ctrl-d removes it again, through the same confirm/refresh/remain path.
	a.selectRow(t, key)
	a.Press("ctrl+d")
	a.WaitFor("CONFIRM", Settle)
	a.Press("y")

	// The key count going back to 2 is what says the row is gone. Waiting
	// for the key *text* to disappear would never succeed: the result line
	// names it, and every frame is space-padded to the full width.
	a.WaitFor("removed "+key, Settle)
	a.WaitForAll(Settle, "secret/app-secret", "2 keys")
	if _, ok := secretKeys(t, "app-secret")[key]; ok {
		t.Fatalf("key %s is still in the cluster after ctrl-d", key)
	}
}

// selectRow moves the cursor down until want is the selected row, so a verb
// acts on the row the test means rather than whichever one happened to be
// selected. The selection marker is the only thing that says which is which.
func (a *App) selectRow(t *testing.T, want string) {
	t.Helper()
	for range 12 {
		for _, line := range strings.Split(a.Frame(), "\n") {
			if strings.Contains(line, want) && strings.Contains(line, "›") {
				return
			}
		}
		a.Down()
	}
	t.Fatalf("never reached row %q:\n%s", want, a.Frame())
}

func e2eClientset(t *testing.T) kubernetes.Interface {
	t.Helper()
	cfg, err := clientcmd.BuildConfigFromFlags("", KubeconfigPath())
	if err != nil {
		t.Fatalf("building a REST config from %s: %v", KubeconfigPath(), err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("building a clientset: %v", err)
	}
	return client
}

func configMapValue(t *testing.T, name, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cm, err := e2eClientset(t).CoreV1().ConfigMaps(Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading configmap %s: %v", name, err)
	}
	return cm.Data[key]
}

func secretValue(t *testing.T, name, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret, err := e2eClientset(t).CoreV1().Secrets(Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading secret %s: %v", name, err)
	}
	return string(secret.Data[key])
}

func secretKeys(t *testing.T, name string) map[string][]byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret, err := e2eClientset(t).CoreV1().Secrets(Namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading secret %s: %v", name, err)
	}
	return secret.Data
}
