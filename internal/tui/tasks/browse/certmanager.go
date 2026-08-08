// Certificate-specific browse machinery for §35c (docs/design/
// v.0.7.0.dc.html): the 'r' force-renew verb on a Certificate row. Kept in
// its own file per browse's per-concern split convention (argo.go, flux.go,
// jobs.go), separate from certchain.go's ↵ carve-out into tasks/certchain —
// renew stays out of scope for that screen (tasks/certchain/keys.go's own
// doc comment), so this file only ever reaches the row on the list itself.
package browse

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/resources"
	"github.com/kute-dev/kute/internal/tui"
	"github.com/kute-dev/kute/internal/tui/verbs"
)

// certVerbsApply reports whether §35c's verb is live on the current view: a
// Certificate kind, a wired mutator, and a settled list — same shape as
// argoVerbsApply/fluxVerbsApply.
func (m Model) certVerbsApply() bool {
	return m.kind == kube.KindCertificate && m.mutator != nil && m.state == tui.TaskStateReady
}

// beginCertRenew starts verbs.CertRenew ('r'): verbs.CertRenew.Tier is used
// directly rather than resolved through verbs.TierFor, deliberately — see
// its own doc comment for why PROD never escalates this past the plain
// inline y/N, the same skip beginJobRetry's own doc comment explains for
// Retry.
func (m *Model) beginCertRenew(row resources.Row) tea.Cmd {
	return m.actions.Begin(verbs.CertRenew.Tier, tui.TaskAction{
		ID:    "cert-renew-" + row.Namespace + "/" + row.Name,
		Label: fmt.Sprintf("Renew %s?", row.Name),
		Scope: tui.TaskScope{
			ResourceKind: string(kube.KindCertificate), ResourceName: row.Name,
			Namespace: row.Namespace, Verb: "cert-renew", IsMutating: true,
		},
	})
}

// certRenewWillRunLine is beginCertRenew's confirm "will run: ..." line —
// same idiom as jobRetryWillRunLine/argoSyncWillRunLine: the literal kubectl
// patch RenewCertificate issues, never `cmctl renew`
// (RenewCertificateCommandString's own doc comment has the reasoning).
func certRenewWillRunLine(scope tui.TaskScope) string {
	return "will run: " + kube.RenewCertificateCommandString(scope.Namespace, scope.ResourceName)
}

// certKeybarGroup is §35c's keybar block.
func (m Model) certKeybarGroup() []tui.KeyHint {
	return []tui.KeyHint{verbs.CertRenew.Hint()}
}
