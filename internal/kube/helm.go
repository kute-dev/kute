// Package kube's Helm release support (18a, docs/design/README.md):
// releases are decoded straight from `sh.helm.release.v1` Secrets already
// sitting in the watched Secret cache — "browsing needs no helm binary".
// Only the rollback verb shells out to the real `helm` binary (see
// HelmRollback below). Helm itself stores each release revision as its own
// Secret (labelled owner=helm, name=<release>, version=<revision>), so a
// release "row" is really the highest-revision Secret for a given
// name/namespace — DecodeHelmReleases/LatestHelmReleases do that
// aggregation; HelmReleaseHistory keeps every revision for 18a's `h` rail.
package kube

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	sigsyaml "sigs.k8s.io/yaml"
)

// HelmReleaseSecretType is the Secret.Type Helm 3 stores every release
// revision under (helm.sh/helm/v3/pkg/storage/driver's own constant,
// reproduced here rather than pulling in the full Helm SDK for one string).
const HelmReleaseSecretType corev1.SecretType = "helm.sh/release.v1"

// HelmRelease is one decoded release revision — either the release's
// current (highest-revision) state for the 18a list, or one entry in its
// full history (h).
type HelmRelease struct {
	Name         string
	Namespace    string
	Chart        string
	ChartVersion string
	AppVersion   string
	Revision     int
	// Status is Helm's own lowercase-hyphenated status word ("deployed",
	// "pending-upgrade", "failed", "superseded", "uninstalled", …) — the 18a
	// STATUS cell's base text.
	Status string
	// StatusReason is Info.Description, non-empty mainly for a failed
	// release ("hook timeout", "pre-upgrade hooks failed: ..."). 18a: "failed
	// carries the reason verbatim".
	StatusReason string
	Updated      time.Time
	// Values is the release's supplied values (Config) rendered as YAML —
	// what 18a's 'v' pushes into the read-only YAML viewer.
	Values   string
	Manifest string
	Notes    string
}

// StatusCell renders 18a's STATUS column text: the bare status word, or
// "failed · <reason>" verbatim when a failure reason is known.
func (r HelmRelease) StatusCell() string {
	if r.Status == "failed" && r.StatusReason != "" {
		return "failed · " + r.StatusReason
	}
	return r.Status
}

// HelmReleaseObject adapts a HelmRelease to runtime.Object so it can flow
// through the same resources.List/Project pipeline as every other kind
// (the same shape ForwardObject already established for a non-typed,
// app-synthesized kind — see forward.go). Unlike ForwardObject it also
// carries a real ObjectMeta (Name/Namespace mirrored from Release) so
// apimeta.Accessor works on it — a Helm release genuinely lives in a
// namespace, unlike a Forward session, so callers that generically
// namespace-filter a RawLister's results (kube/fake's own ListRaw, and any
// test double built the same way) need it to satisfy metav1.Object.
type HelmReleaseObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
	Release HelmRelease
}

// NewHelmReleaseObject builds a HelmReleaseObject with ObjectMeta populated
// from r — the one constructor every caller (helmAwareLister, test
// fixtures) should use rather than setting ObjectMeta by hand.
func NewHelmReleaseObject(r HelmRelease) *HelmReleaseObject {
	return &HelmReleaseObject{
		ObjectMeta: metav1.ObjectMeta{Name: r.Name, Namespace: r.Namespace},
		Release:    r,
	}
}

// DeepCopyObject satisfies runtime.Object. HelmRelease has no pointer
// fields, so a shallow copy is a full deep copy.
func (o *HelmReleaseObject) DeepCopyObject() runtime.Object {
	cp := *o
	return &cp
}

// helmReleaseData mirrors just the fields kute reads off Helm 3's own
// release.Release JSON schema (helm.sh/helm/v3/pkg/release) — reproduced
// locally rather than importing the Helm SDK for a handful of fields.
type helmReleaseData struct {
	Name string `json:"name"`
	Info struct {
		Status        string    `json:"status"`
		Description   string    `json:"description"`
		LastDeployed  time.Time `json:"last_deployed"`
		FirstDeployed time.Time `json:"first_deployed"`
		Notes         string    `json:"notes"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			AppVersion string `json:"appVersion"`
		} `json:"metadata"`
	} `json:"chart"`
	Config    map[string]any `json:"config"`
	Manifest  string         `json:"manifest"`
	Version   int            `json:"version"`
	Namespace string         `json:"namespace"`
}

// DecodeHelmReleaseSecret decodes one release revision Secret. Helm stores
// the release payload base64-encoded a *second* time (on top of the Secret
// API's own wire-format encoding, which client-go already reverses into
// secret.Data), then gzip-compressed, then JSON — this reverses exactly
// that: base64 decode, gunzip, unmarshal.
func DecodeHelmReleaseSecret(secret *corev1.Secret) (HelmRelease, error) {
	if secret.Type != HelmReleaseSecretType {
		return HelmRelease{}, fmt.Errorf("secret %s/%s is not a %s release", secret.Namespace, secret.Name, HelmReleaseSecretType)
	}
	raw, ok := secret.Data["release"]
	if !ok {
		return HelmRelease{}, fmt.Errorf("secret %s/%s has no release data", secret.Namespace, secret.Name)
	}
	b64Decoded, err := base64.StdEncoding.DecodeString(string(raw))
	if err != nil {
		return HelmRelease{}, fmt.Errorf("base64-decode release: %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(b64Decoded))
	if err != nil {
		return HelmRelease{}, fmt.Errorf("gunzip release: %w", err)
	}
	defer func() { _ = gz.Close() }()
	jsonBytes, err := io.ReadAll(gz)
	if err != nil {
		return HelmRelease{}, fmt.Errorf("read release: %w", err)
	}
	var data helmReleaseData
	if err := json.Unmarshal(jsonBytes, &data); err != nil {
		return HelmRelease{}, fmt.Errorf("unmarshal release: %w", err)
	}

	namespace := data.Namespace
	if namespace == "" {
		namespace = secret.Namespace
	}
	valuesYAML := ""
	if len(data.Config) > 0 {
		if y, err := sigsyaml.Marshal(data.Config); err == nil {
			valuesYAML = string(y)
		}
	}
	return HelmRelease{
		Name:         data.Name,
		Namespace:    namespace,
		Chart:        data.Chart.Metadata.Name,
		ChartVersion: data.Chart.Metadata.Version,
		AppVersion:   data.Chart.Metadata.AppVersion,
		Revision:     data.Version,
		Status:       data.Info.Status,
		StatusReason: data.Info.Description,
		Updated:      data.Info.LastDeployed,
		Values:       valuesYAML,
		Manifest:     data.Manifest,
		Notes:        data.Info.Notes,
	}, nil
}

// DecodeHelmReleases decodes every helm.sh/release.v1 Secret in objs,
// silently skipping anything that isn't one or fails to decode (the same
// best-effort tolerance projectForward/podsByName already apply to a
// partially-unexpected object list).
func DecodeHelmReleases(objs []runtime.Object) []HelmRelease {
	out := make([]HelmRelease, 0, len(objs))
	for _, obj := range objs {
		secret, ok := obj.(*corev1.Secret)
		if !ok || secret.Type != HelmReleaseSecretType {
			continue
		}
		r, err := DecodeHelmReleaseSecret(secret)
		if err != nil {
			continue
		}
		out = append(out, r)
	}
	return out
}

// LatestHelmReleases collapses all to one row per namespace/name — the
// highest-revision entry, matching `helm list`'s own "current state of the
// release" semantics (18a's list shows one row per release, not one per
// revision).
func LatestHelmReleases(all []HelmRelease) []HelmRelease {
	latest := make(map[string]HelmRelease, len(all))
	for _, r := range all {
		key := r.Namespace + "/" + r.Name
		if cur, ok := latest[key]; !ok || r.Revision > cur.Revision {
			latest[key] = r
		}
	}
	out := make([]HelmRelease, 0, len(latest))
	for _, r := range latest {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// HelmReleaseHistory filters all to one release's revisions, newest first —
// 18a's `h` revision rail.
func HelmReleaseHistory(all []HelmRelease, namespace, name string) []HelmRelease {
	var out []HelmRelease
	for _, r := range all {
		if r.Namespace == namespace && r.Name == name {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision > out[j].Revision })
	return out
}

// HelmRollbackCommandString renders the exact `helm` invocation HelmRollback
// runs — 18a's "will run" documentation line (the same copyable-command
// idiom as 10a/13a/17b). toRevision 0 means "the previous revision" (plain
// `helm rollback <name>`, Helm's own default).
func HelmRollbackCommandString(namespace, name string, toRevision int) string {
	if toRevision > 0 {
		return fmt.Sprintf("helm rollback %s %d -n %s", name, toRevision, namespace)
	}
	return fmt.Sprintf("helm rollback %s -n %s", name, namespace)
}

// HelmRollback shells out to the real `helm` binary — the one Helm verb
// that isn't decoded from the watch cache (18a: "browsing needs no helm
// binary" but rollback does). Returns a clear, inline-explainable error when
// helm isn't on PATH rather than a raw exec.ErrNotFound.
func HelmRollback(ctx context.Context, namespace, name string, toRevision int) error {
	if _, err := exec.LookPath("helm"); err != nil {
		return fmt.Errorf("helm not found in PATH — install helm to roll back releases")
	}
	args := []string{"rollback", name}
	if toRevision > 0 {
		args = append(args, fmt.Sprintf("%d", toRevision))
	}
	args = append(args, "-n", namespace)
	cmd := exec.CommandContext(ctx, "helm", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("helm rollback failed: %s", msg)
	}
	return nil
}

// EncodeHelmReleaseSecret builds the Secret Helm itself would store for r —
// the inverse of DecodeHelmReleaseSecret. Used by kube/fake's demo fixtures
// (fixtures.go) and by fake.Cluster.HelmRollback to synthesize the new
// revision a real `helm rollback` would create, without needing a real Helm
// SDK dependency.
func EncodeHelmReleaseSecret(r HelmRelease) *corev1.Secret {
	data := helmReleaseData{
		Name:      r.Name,
		Namespace: r.Namespace,
		Version:   r.Revision,
		Manifest:  r.Manifest,
	}
	data.Info.Status = r.Status
	data.Info.Description = r.StatusReason
	data.Info.LastDeployed = r.Updated
	data.Info.FirstDeployed = r.Updated
	data.Info.Notes = r.Notes
	data.Chart.Metadata.Name = r.Chart
	data.Chart.Metadata.Version = r.ChartVersion
	data.Chart.Metadata.AppVersion = r.AppVersion
	if r.Values != "" {
		cfg := map[string]any{}
		if err := sigsyaml.Unmarshal([]byte(r.Values), &cfg); err == nil {
			data.Config = cfg
		}
	}

	jsonBytes, _ := json.Marshal(data)
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	_, _ = gz.Write(jsonBytes)
	_ = gz.Close()
	encoded := base64.StdEncoding.EncodeToString(gzBuf.Bytes())

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("sh.helm.release.v1.%s.v%d", r.Name, r.Revision),
			Namespace: r.Namespace,
			Labels: map[string]string{
				"owner":   "helm",
				"name":    r.Name,
				"status":  r.Status,
				"version": fmt.Sprintf("%d", r.Revision),
			},
			CreationTimestamp: metav1.NewTime(r.Updated),
		},
		Type: HelmReleaseSecretType,
		Data: map[string][]byte{"release": []byte(encoded)},
	}
}
