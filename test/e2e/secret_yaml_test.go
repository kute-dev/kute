//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

// TestSecretYAMLDecodeRevealsOnlyWhatIsAsked covers §21a — the Secret
// semantics inside the 8a YAML view, which is a different screen from §27b's
// masked data grid and has its own masking code.
//
// It is worth a real cluster because the thing under test is what arrives
// over the wire: the API server returns data: as base64, and kute's promise
// is that the plaintext exists only in memory and only for the keys the user
// asked for. A test against a fixture proves the renderer; this proves the
// renderer against a Secret the cluster actually stored.
func TestSecretYAMLDecodeRevealsOnlyWhatIsAsked(t *testing.T) {
	RequireCluster(t)
	// From 20-config.yaml. Both values are asserted on: one has to appear
	// when revealed, the other has to stay absent while it isn't.
	const (
		name     = "app-secret"
		tokenKey = "api-token"
		token    = "KUTE-E2E-SECRET-VALUE"
		urlKey   = "database-url"
		url      = "postgres://kute:hunter2@db.kute-e2e.svc:5432/shop"
	)

	a := Launch(t)
	a.WaitFor("api-", Connect)
	a.gotoKind(t, "secrets", "Secrets")
	a.selectRow(t, name)
	a.Press("y")
	a.WaitLoaded(Settle)

	// 21a's own info strip replaces the generic kind/resourceVersion pair,
	// and it is the screen's honest statement of how much plaintext is
	// currently on screen.
	a.WaitForAll(Settle, "Secret · Opaque · 2 keys · 0 revealed", "decoded in memory only")

	// Masked by default, with the shape of the value described rather than
	// the value shown — and never the raw base64 either, which is not
	// encryption and reads as noise.
	a.WaitFor("base64", Settle)
	a.Never(token, 2*time.Second)
	a.Never(url, 2*time.Second)

	// 'x' reveals the key under the cursor, so the cursor has to be on one:
	// '/' search moves it to the first match, and esc leaves search with the
	// cursor where it landed.
	a.Press("/")
	a.Type(tokenKey)
	a.Esc()
	a.Press("x")

	// Exactly one key revealed — the count says so, the value is on screen,
	// and the other key's plaintext still is not. "reveals the cursor key"
	// failing open would show both.
	a.WaitForAll(Settle, token, "revealed", "1 revealed")
	a.Never(url, 2*time.Second)

	// 'X' is reveal-all, behind an inline y/N rather than as a bare key.
	a.Press("X")
	a.WaitFor("Reveal all 2 keys in this Secret?", Settle)
	// 'n' declines, and declining must not reveal anything.
	a.Press("n")
	a.WaitGone("Reveal all 2 keys", Settle)
	a.Never(url, 2*time.Second)

	a.Press("X")
	a.WaitFor("Reveal all 2 keys in this Secret?", Settle)
	a.Press("y")
	a.WaitForAll(Settle, "2 revealed", token)
	// The second value is long enough to be truncated in a narrow cell, so
	// assert the part that identifies it rather than the whole URL.
	a.WaitForWrapped("postgres://kute", Settle)

	// esc walks back exactly one level, to the list the view was opened
	// from — and the plaintext does not follow it there.
	a.Esc()
	a.WaitFor("Secrets", Settle)
	if frame := a.Frame(); strings.Contains(frame, token) {
		t.Errorf("the Secrets list shows a decoded value after the YAML view revealed it:\n%s", frame)
	}
}
