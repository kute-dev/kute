//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func TestResizePreservesSelectionAndFocusedBuffers(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)
	pod := a.selectAPIPod(t)
	resizeCycle(a)
	a.Enter()
	a.WaitForAll(Settle, pod, "CONTAINERS")
	a.Esc()
	a.WaitFor("api-", Settle)

	a.Press("g")
	a.Paste("configmaps")
	resizeCycle(a)
	a.WaitFor("ConfigMaps", Settle)
	a.Esc()

	a.openFrom(t, "configmaps", "ConfigMaps", "app-config")
	a.WaitFor("nginx.conf", Settle)
	a.selectRow(t, "nginx.conf")
	a.Press("e")
	a.WaitFor("BUFFER EDITOR", Settle)
	resizeCycle(a)
	a.Paste("\nKUTE-E2E-RESIZE-EDITOR")
	a.WaitFor("KUTE-E2E-RESIZE-EDITOR", Settle)
	a.Esc() // discard

	a.Esc()
	a.gotoKind(t, "pods", "Pods")
	a.WaitFor("api-", Settle)
	pod = a.selectAPIPod(t)
	a.Enter()
	a.WaitFor("CONTAINERS", Settle)
	a.Press("l")
	a.WaitFor("KUTE-E2E-LOG-MARKER", Settle)
	a.Press("/")
	a.Paste("request")
	resizeCycle(a)
	a.WaitFor("/ request", Settle)
}

func TestBracketedPasteRoutesOnlyToActiveBuffers(t *testing.T) {
	a := Launch(t)
	a.WaitFor("api-", Connect)

	// With no active buffer, shortcut-looking bytes are one paste event and
	// must not open goto/context/help/delete actions.
	a.Paste("Dgcn?/api")
	a.WaitFor("Pods", Settle)
	if frame := a.Frame(); strings.Contains(frame, "CONFIRM") || strings.Contains(frame, "jump anywhere") || strings.Contains(frame, "ctx ›") {
		t.Fatalf("paste without a buffer executed a shortcut:\n%s", frame)
	}

	a.Press("/")
	a.Paste("api-Dgcn?/")
	a.WaitFor("/ api-Dgcn?/", Settle)
	a.Esc()

	a.Press("g")
	a.Paste("services")
	a.WaitFor("Services", Settle)
	a.Enter()
	a.WaitFor("Services", Settle)
	a.filterTo(t, "api")
	a.Press("f")
	a.WaitFor("localhost:", Settle)
	a.Press("4")
	a.Paste("5678")
	a.WaitFor("localhost:45678", Settle)
	a.Esc() // leave port edit
	a.Esc() // leave picker without starting
}

func TestBracketedPasteFillsProdTypeNameConfirmation(t *testing.T) {
	ctxName := ContextName(t)
	a := Launch(t, WithProdContexts(ctxName))
	a.WaitFor("api-", Connect)
	pod := a.selectAPIPod(t)
	a.Press("D")
	a.WaitForAll(Settle, TypeToConfirm, pod)
	a.Paste("pasted-but-not-" + pod)
	a.WaitFor("pasted-but-not-", Settle)
	a.Esc() // proving routing only; do not commit the fixture deletion
	assertPodExists(t, pod)
}

func resizeCycle(a *App) {
	a.Resize(140, 36)
	a.Resize(80, 24)
	a.Resize(40, 10)
	a.Resize(140, 36)
}
