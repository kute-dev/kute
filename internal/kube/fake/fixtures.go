package fake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kute-dev/kute/internal/kube"
)

// NewDemo builds a fake cluster seeded with a representative fixture set —
// pods incl. crashloop/pending/completed, a deployment mid-rollout, nodes
// with pressure, and events tied to the crashlooping pod — for the --demo
// flag and any task test that wants a whole cluster rather than one-off
// stubs.
func NewDemo() *Cluster {
	c := New("default", "demo")
	c.AddContext("demo-prod", "default")
	// dev-readonly is who tasks/whocan (22a) pins as "current user" — a
	// read-only persona (bound to the "view" ClusterRole below, which
	// excludes secrets) so `g "who"`'s default "who can list secrets in
	// default" query has a real closest-miss row to show, the same persona
	// docs/design README.md's own 4b mockup names.
	c.SetUserName("dev-readonly")

	now := metav1.Now()
	age := func(d time.Duration) metav1.Time { return metav1.NewTime(now.Add(-d)) }

	// apiPod/workerPod carry OwnerReferences + labels so poddetail's alt+o
	// (owning Deployment/StatefulSet) and alt+i (fronting Ingress) have real
	// chains to resolve in --demo: apiPod -> ReplicaSet api-7d9f6c8 ->
	// Deployment api -> Service api -> Ingress api; workerPod -> StatefulSet
	// worker directly (no intermediate ReplicaSet, same as a real cluster).
	apiPod := demoPod("api-7d9f6c8-abcde", "default", age(2*24*time.Hour), corev1.PodRunning, corev1.PodQOSGuaranteed, "node-a", true, 0, nil)
	apiPod.Labels = map[string]string{"app": "api"}
	apiPod.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-7d9f6c8"}}
	// A second, running sidecar container makes apiPod --demo's one
	// reachable exercise of 10a's exec-container-picker screen — a
	// single-container pod execs straight through instead, so without this
	// the picker screen could only ever be driven by synthetic unit-test
	// fixtures (CLAUDE.md: "the fake provider must stay feature-complete
	// for tests/demo mode").
	apiPod.Spec.Containers = append(apiPod.Spec.Containers, corev1.Container{Name: "metrics-sidecar", Image: "sidecar:0.9.1"})
	apiPod.Status.ContainerStatuses = append(apiPod.Status.ContainerStatuses, corev1.ContainerStatus{
		Name: "metrics-sidecar", Ready: true,
		State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: age(2 * 24 * time.Hour)}},
	})

	workerPod := demoCrashLoopPod("worker-0", "default", age(14*time.Hour), "node-a")
	workerPod.Labels = map[string]string{"app": "worker"}
	workerPod.OwnerReferences = []metav1.OwnerReference{{Kind: "StatefulSet", Name: "worker"}}

	c.Seed(kube.KindPod,
		apiPod,
		workerPod,
		demoPendingPod("cache-0", "default", age(3*time.Minute)),
		demoCompletedPod("migrate-job-x8z2p", "default", age(20*time.Minute)),
	)

	c.Seed(kube.KindDeployment, demoMidRolloutDeployment("api", "default", age(30*24*time.Hour)))
	c.Seed(kube.KindReplicaSet, demoReplicaSet("api-7d9f6c8", "default", "api", age(30*24*time.Hour)))
	c.Seed(kube.KindStatefulSet, demoStatefulSet("worker", "default", age(14*time.Hour)))
	c.Seed(kube.KindService, demoService("api", "default", map[string]string{"app": "api"}, age(30*24*time.Hour)))
	c.Seed(kube.KindIngress, demoIngress("api", "default", "api", "api.demo.local", age(30*24*time.Hour)))

	// A production-like cluster has many namespaces beyond the one an
	// operator is actively working in: system/platform namespaces
	// (kube-system), operator-owned namespaces for the CRD-installing
	// add-ons below (cert-manager/monitoring/argocd/ingress-nginx/logging),
	// and app environments (production alongside default/staging).
	// "development" is seeded with zero resources of any kind — the
	// fully-empty-namespace case, distinct from staging's "no pods, some
	// config" case below.
	c.Seed(kube.KindNamespace,
		demoNamespace("default"), demoNamespace("staging"), demoNamespace("production"),
		demoNamespace("kube-system"), demoNamespace("cert-manager"), demoNamespace("monitoring"),
		demoNamespace("argocd"), demoNamespace("ingress-nginx"), demoNamespace("logging"),
		demoNamespace("development"),
	)

	// "staging" has no pods (10c empty-namespace preview) but does have
	// other kinds, so the empty state's "g other kinds" way-out has live
	// data to show rather than degrading to a plain line.
	c.Seed(kube.KindConfigMap,
		demoConfigMap("app-config", "staging", age(10*24*time.Hour)),
		demoConfigMap("feature-flags", "staging", age(2*24*time.Hour)),
	)
	c.Seed(kube.KindSecret, demoSecret("app-secret", "staging", age(10*24*time.Hour)))

	c.Seed(kube.KindNode,
		demoNode("node-a", true, false, false),
		demoNode("node-b", true, true, false),   // MemoryPressure
		demoNode("node-c", true, false, true),   // cordoned
		demoNode("node-d", false, false, false), // NotReady
	)

	c.Seed(kube.KindEvent,
		demoEvent("worker-0.backoff1", "default", "Pod", "worker-0", "Warning", "BackOff",
			"Back-off restarting failed container worker in pod worker-0_default(...)", 5, age(30*time.Minute)),
		demoEvent("worker-0.scheduled", "default", "Pod", "worker-0", "Normal", "Scheduled",
			"Successfully assigned default/worker-0 to node-a", 1, age(14*time.Hour)),
		demoEvent("node-b.pressure", "", "Node", "node-b", "Warning", "MemoryPressure",
			"Node node-b status is now: MemoryPressure", 3, age(45*time.Minute)),
	)

	c.SeedLogs("default", "worker-0", []string{
		"2024-01-01T00:00:00Z INF starting worker",
		"2024-01-01T00:00:05Z ERR panic: connection refused",
		"2024-01-01T00:00:06Z INF restarting",
	})

	demoRBACFixtures(c, age)
	demoCertManagerFixtures(c, age)
	demoKubeSystemFixtures(c, age)
	demoIngressNginxFixtures(c, age)
	demoProductionFixtures(c, age)
	demoLoggingFixtures(c, age)
	demoPrometheusFixtures(c, age)
	demoArgoCDFixtures(c, age)
	demoGatewayAPIFixtures(c, age)
	demoHelmReleaseFixtures(c, age)

	// "ghost" exercises 23a's ◐ "0 ready" backend state: a Service whose
	// selector matches no pod at all, fronted by an Ingress rule that routes
	// to it — staging otherwise has no pods (10c's empty-namespace preview).
	c.Seed(kube.KindService, demoService("empty-svc", "staging", map[string]string{"app": "ghost"}, age(5*24*time.Hour)))
	c.Seed(kube.KindIngress, demoIngress("ghost", "staging", "empty-svc", "ghost.demo.local", age(5*24*time.Hour)))

	return c
}

// demoKubeSystemFixtures seeds the platform namespace every real cluster
// has — coredns/kube-proxy — so "many namespaces" includes the one every
// operator recognizes on sight.
func demoKubeSystemFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	sysAge := age(120 * 24 * time.Hour)
	c.Seed(kube.KindDeployment, demoStableDeployment("coredns", "kube-system", "coredns:1.11.1", 2, sysAge))
	c.Seed(kube.KindDaemonSet, demoDaemonSetReady("kube-proxy", "kube-system", 4, sysAge))
	c.Seed(kube.KindPod, demoOwnedPod("coredns-6b7f9d5f8c-x2k9p", "kube-system", age(3*24*time.Hour), "node-a", "ReplicaSet", "coredns-6b7f9d5f8c"))
}

// demoIngressNginxFixtures seeds the ingress controller namespace — a
// LoadBalancer Service with an external IP is the other half of the
// "api" Ingress' own address, demonstrated end to end.
func demoIngressNginxFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	ingAge := age(60 * 24 * time.Hour)
	c.Seed(kube.KindDeployment, demoStableDeployment("ingress-nginx-controller", "ingress-nginx", "registry.k8s.io/ingress-nginx/controller:v1.10.1", 2, ingAge))
	c.Seed(kube.KindService, demoLoadBalancerService("ingress-nginx-controller", "ingress-nginx", 80, "203.0.113.15", ingAge))
	c.Seed(kube.KindPod, demoOwnedPod("ingress-nginx-controller-8f7d9c-k7m2q", "ingress-nginx", age(10*24*time.Hour), "node-b", "ReplicaSet", "ingress-nginx-controller-8f7d9c"))
}

// demoProductionFixtures mirrors "default"'s api/worker pattern in a
// second app environment — a stable, fully-ready Deployment (no rollout in
// progress) so the health strip's "many namespaces" story isn't only
// crashloops and mid-rollouts.
func demoProductionFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	prodAge := age(90 * 24 * time.Hour)
	c.Seed(kube.KindDeployment, demoStableDeployment("web", "production", "web:4.2.0", 3, prodAge))
	c.Seed(kube.KindReplicaSet, demoReplicaSet("web-7c9f8d", "production", "web", prodAge))
	c.Seed(kube.KindService, demoService("web", "production", map[string]string{"app": "web"}, prodAge))
	c.Seed(kube.KindIngress, demoIngress("web", "production", "web", "web.prod.demo.local", prodAge))
	webPodA := demoOwnedPod("web-7c9f8d-aaaaa", "production", age(9*24*time.Hour), "node-a", "ReplicaSet", "web-7c9f8d")
	webPodB := demoOwnedPod("web-7c9f8d-bbbbb", "production", age(9*24*time.Hour), "node-b", "ReplicaSet", "web-7c9f8d")
	// Labeled to match the "web" Service's selector above — 23a's Ingress
	// routing table (and 23b's canary HTTPRoute below) resolve backend
	// health by matching a Service's selector against real pods, so these
	// need the label a real ReplicaSet-managed pod would carry.
	webPodA.Labels = map[string]string{"app": "web"}
	webPodB.Labels = map[string]string{"app": "web"}
	c.Seed(kube.KindPod, webPodA, webPodB)

	// "web-secure" exercises 23a's TLS strip and both remaining backend
	// glyphs: /  -> web (● ready), /admin -> web-missing (✕ service not
	// found — no such Service is ever seeded). The TLS secret expires soon
	// (yellow) so the strip's <30d coloring has something to show.
	c.Seed(kube.KindSecret, demoTLSSecret("web-tls", "production", time.Now().Add(20*24*time.Hour), prodAge))
	c.Seed(kube.KindIngress, demoIngressWithTLS("web-secure", "production", "web", "web-missing", "secure.prod.demo.local", "web-tls", prodAge))
}

// demoLoggingFixtures seeds a third add-on namespace shape: a DaemonSet
// (fluent-bit, one per node) feeding a StatefulSet (elasticsearch) behind a
// Deployment (kibana) — none of them CRD-backed, unlike cert-manager/
// monitoring/argocd, so the namespace list isn't only operator namespaces.
func demoLoggingFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	logAge := age(75 * 24 * time.Hour)
	c.Seed(kube.KindDaemonSet, demoDaemonSetReady("fluent-bit", "logging", 4, logAge))
	c.Seed(kube.KindStatefulSet, demoStatefulSetN("elasticsearch", "logging", 3, logAge))
	c.Seed(kube.KindDeployment, demoStableDeployment("kibana", "logging", "kibana:8.13.0", 1, logAge))
	c.Seed(kube.KindPod, demoOwnedPod("fluent-bit-9k2mp", "logging", age(5*24*time.Hour), "node-a", "DaemonSet", "fluent-bit"))
}

// demoRBACFixtures seeds tasks/whocan's (22a) resolution graph: a broad
// read-only ClusterRole ("view") deliberately excluding secrets — bound to
// dev-readonly (NewDemo's pinned "current user") and a "dev-team" Group —
// plus a secret-scoped Role and a cluster-wide admin ClusterRoleBinding, so
// `g "who can list secrets"` has real subjects across both SCOPE values
// (namespace and cluster) and a genuine closest-miss for dev-readonly.
func demoRBACFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	rbacAge := age(120 * 24 * time.Hour)

	c.Seed(kube.KindClusterRole,
		demoClusterRole("view", rbacAge,
			rbacv1.PolicyRule{
				APIGroups: []string{""},
				Resources: []string{"pods", "services", "configmaps", "namespaces", "nodes", "events"},
				Verbs:     []string{"get", "list", "watch"},
			},
			rbacv1.PolicyRule{
				APIGroups: []string{"apps"},
				Resources: []string{"deployments", "replicasets", "statefulsets", "daemonsets"},
				Verbs:     []string{"get", "list", "watch"},
			},
		),
		demoClusterRole("admin", rbacAge,
			rbacv1.PolicyRule{APIGroups: []string{"*"}, Resources: []string{"*"}, Verbs: []string{"*"}},
		),
	)

	c.Seed(kube.KindRole,
		demoRole("secret-reader", "default", rbacAge,
			rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{"secrets"}, Verbs: []string{"get", "list", "watch"}},
		),
	)

	c.Seed(kube.KindClusterRoleBinding,
		demoClusterRoleBinding("cluster-admins", "admin", rbacAge,
			rbacv1.Subject{Kind: rbacv1.UserKind, Name: "alice"},
		),
	)

	c.Seed(kube.KindRoleBinding,
		demoRoleBinding("dev-viewers", "default", "ClusterRole", "view", rbacAge,
			rbacv1.Subject{Kind: rbacv1.UserKind, Name: "dev-readonly"},
			rbacv1.Subject{Kind: rbacv1.GroupKind, Name: "dev-team"},
		),
		demoRoleBinding("secret-readers", "default", "Role", "secret-reader", rbacAge,
			rbacv1.Subject{Kind: rbacv1.UserKind, Name: "bob"},
			rbacv1.Subject{Kind: rbacv1.ServiceAccountKind, Name: "vault-agent", Namespace: "default"},
		),
	)
}

func demoClusterRole(name string, created metav1.Time, rules ...rbacv1.PolicyRule) *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: created},
		Rules:      rules,
	}
}

func demoRole(name, ns string, created metav1.Time, rules ...rbacv1.PolicyRule) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Rules:      rules,
	}
}

func demoClusterRoleBinding(name, roleName string, created metav1.Time, subjects ...rbacv1.Subject) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, CreationTimestamp: created},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: roleName},
		Subjects:   subjects,
	}
}

func demoRoleBinding(name, ns, roleKind, roleName string, created metav1.Time, subjects ...rbacv1.Subject) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: roleKind, Name: roleName},
		Subjects:   subjects,
	}
}

// demoCertManagerFixtures seeds the design doc's own 14a/14b/14d exemplar —
// cert-manager.io's Certificate (namespaced, exercises READY/SECRET/ISSUER
// printer columns), CertificateRequest (namespaced), and ClusterIssuer
// (cluster-scoped) — so --demo exercises CRD support end to end without a
// real cluster.
func demoCertManagerFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	crdAge := age(90 * 24 * time.Hour)

	certCols := []kube.PrinterColumn{
		{Name: "Ready", Type: "string", JSONPath: `.status.conditions[?(@.type=="Ready")].status`},
		{Name: "Secret", Type: "string", JSONPath: ".spec.secretName"},
		{Name: "Issuer", Type: "string", JSONPath: ".spec.issuerRef.name"},
	}
	certReqCols := []kube.PrinterColumn{
		{Name: "Ready", Type: "string", JSONPath: `.status.conditions[?(@.type=="Ready")].status`},
		{Name: "Issuer", Type: "string", JSONPath: ".spec.issuerRef.name"},
	}
	issuerCols := []kube.PrinterColumn{
		{Name: "Ready", Type: "string", JSONPath: `.status.conditions[?(@.type=="Ready")].status`},
	}

	c.Seed(kube.KindCustomResourceDefinition,
		demoCRD("certificates.cert-manager.io", "cert-manager.io", "Certificate", "certificates", "Namespaced", "v1", true, certCols, crdAge),
		demoCRD("certificaterequests.cert-manager.io", "cert-manager.io", "CertificateRequest", "certificaterequests", "Namespaced", "v1", true, certReqCols, crdAge),
		demoCRD("clusterissuers.cert-manager.io", "cert-manager.io", "ClusterIssuer", "clusterissuers", "Cluster", "v1", true, issuerCols, crdAge),
	)

	c.SeedDiscovered(demoDiscoveredKind("Certificate", "certificates", "certificates.cert-manager.io", false, certCols))
	c.SeedDiscovered(demoDiscoveredKind("CertificateRequest", "certificaterequests", "certificaterequests.cert-manager.io", false, certReqCols))
	c.SeedDiscovered(demoDiscoveredKind("ClusterIssuer", "clusterissuers", "clusterissuers.cert-manager.io", true, issuerCols))

	c.Seed(kube.ResourceKind("Certificate"),
		demoCertificate("api-tls", "default", true, "api-tls-secret", "letsencrypt-prod", age(5*24*time.Hour)),
		demoCertificate("staging-tls", "staging", false, "staging-tls-secret", "letsencrypt-staging", age(2*time.Hour)),
	)
	c.Seed(kube.ResourceKind("CertificateRequest"),
		demoCertificateRequest("api-tls-abcd1", "default", true, "letsencrypt-prod", age(5*24*time.Hour)),
	)
	c.Seed(kube.ResourceKind("ClusterIssuer"),
		demoClusterIssuer("letsencrypt-prod", true, age(60*24*time.Hour)),
		demoClusterIssuer("letsencrypt-staging", true, age(60*24*time.Hour)),
	)

	// The operator itself lives in its own "cert-manager" namespace, same
	// as a real Helm install — the CRD instances above are what it manages,
	// not what it is.
	c.Seed(kube.KindDeployment,
		demoStableDeployment("cert-manager", "cert-manager", "quay.io/jetstack/cert-manager-controller:v1.14.4", 1, crdAge),
		demoStableDeployment("cert-manager-cainjector", "cert-manager", "quay.io/jetstack/cert-manager-cainjector:v1.14.4", 1, crdAge),
		demoStableDeployment("cert-manager-webhook", "cert-manager", "quay.io/jetstack/cert-manager-webhook:v1.14.4", 1, crdAge),
	)
	c.Seed(kube.KindPod, demoOwnedPod("cert-manager-7d8f9c-h6q2v", "cert-manager", age(20*24*time.Hour), "node-a", "ReplicaSet", "cert-manager-7d8f9c"))
}

// demoPrometheusFixtures seeds the prometheus-operator (monitoring.coreos.com)
// CRD family: Prometheus/Alertmanager (namespaced, do carry a Ready
// condition in this fixture set) plus ServiceMonitor/PrometheusRule
// (namespaced, no status subresource at all in the real CRD — left with no
// conditions here too, so their glyph is the 14a "never fake health"
// neutral "·" rather than a fabricated one), alongside the operator's own
// workloads in "monitoring".
func demoPrometheusFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	crdAge := age(80 * 24 * time.Hour)
	group := "monitoring.coreos.com"

	c.Seed(kube.KindCustomResourceDefinition,
		demoCRD("prometheuses.monitoring.coreos.com", group, "Prometheus", "prometheuses", "Namespaced", "v1", true, nil, crdAge),
		demoCRD("alertmanagers.monitoring.coreos.com", group, "Alertmanager", "alertmanagers", "Namespaced", "v1", true, nil, crdAge),
		demoCRD("servicemonitors.monitoring.coreos.com", group, "ServiceMonitor", "servicemonitors", "Namespaced", "v1", true, nil, crdAge),
		demoCRD("prometheusrules.monitoring.coreos.com", group, "PrometheusRule", "prometheusrules", "Namespaced", "v1", true, nil, crdAge),
	)
	c.SeedDiscovered(demoDiscoveredKind("Prometheus", "prometheuses", "prometheuses.monitoring.coreos.com", false, nil))
	c.SeedDiscovered(demoDiscoveredKind("Alertmanager", "alertmanagers", "alertmanagers.monitoring.coreos.com", false, nil))
	c.SeedDiscovered(demoDiscoveredKind("ServiceMonitor", "servicemonitors", "servicemonitors.monitoring.coreos.com", false, nil))
	c.SeedDiscovered(demoDiscoveredKind("PrometheusRule", "prometheusrules", "prometheusrules.monitoring.coreos.com", false, nil))

	c.Seed(kube.ResourceKind("Prometheus"),
		demoCR("monitoring.coreos.com/v1", "Prometheus", "k8s", "monitoring", age(80*24*time.Hour), nil, readyCondition(true, "")),
	)
	c.Seed(kube.ResourceKind("Alertmanager"),
		demoCR("monitoring.coreos.com/v1", "Alertmanager", "main", "monitoring", age(80*24*time.Hour), nil, readyCondition(true, "")),
	)
	c.Seed(kube.ResourceKind("ServiceMonitor"),
		demoCR("monitoring.coreos.com/v1", "ServiceMonitor", "api", "monitoring", age(30*24*time.Hour), nil),
		demoCR("monitoring.coreos.com/v1", "ServiceMonitor", "grafana", "monitoring", age(30*24*time.Hour), nil),
	)
	c.Seed(kube.ResourceKind("PrometheusRule"),
		demoCR("monitoring.coreos.com/v1", "PrometheusRule", "k8s-rules", "monitoring", age(30*24*time.Hour), nil),
	)

	c.Seed(kube.KindDeployment,
		demoStableDeployment("prometheus-operator", "monitoring", "quay.io/prometheus-operator/prometheus-operator:v0.74.0", 1, crdAge),
		demoStableDeployment("grafana", "monitoring", "grafana/grafana:10.4.2", 1, crdAge),
		demoStableDeployment("kube-state-metrics", "monitoring", "registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.12.0", 1, crdAge),
	)
	c.Seed(kube.KindStatefulSet,
		demoStatefulSetN("prometheus-k8s", "monitoring", 2, crdAge),
		demoStatefulSetN("alertmanager-main", "monitoring", 3, crdAge),
	)
	c.Seed(kube.KindService, demoService("grafana", "monitoring", map[string]string{"app": "grafana"}, crdAge))
	c.Seed(kube.KindPod,
		demoOwnedPod("prometheus-k8s-0", "monitoring", age(15*24*time.Hour), "node-a", "StatefulSet", "prometheus-k8s"),
		demoOwnedPod("grafana-5c8d9f-t9x4r", "monitoring", age(15*24*time.Hour), "node-b", "ReplicaSet", "grafana-5c8d9f"),
	)
}

// demoArgoCDFixtures seeds the argoproj.io CRD family: Application
// (namespaced, real cluster's own printer columns — SYNC STATUS/HEALTH
// STATUS off .status.sync.status/.status.health.status, no Ready-style
// condition at all, so kute's generic health glyph correctly falls back to
// neutral "·" while the meaningful signal still shows up as printer-column
// text) and AppProject, plus the argocd operator's own workloads.
func demoArgoCDFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	crdAge := age(45 * 24 * time.Hour)
	group := "argoproj.io"

	appCols := []kube.PrinterColumn{
		{Name: "Sync Status", Type: "string", JSONPath: ".status.sync.status"},
		{Name: "Health Status", Type: "string", JSONPath: ".status.health.status"},
	}

	c.Seed(kube.KindCustomResourceDefinition,
		demoCRD("applications.argoproj.io", group, "Application", "applications", "Namespaced", "v1alpha1", true, appCols, crdAge),
		demoCRD("appprojects.argoproj.io", group, "AppProject", "appprojects", "Namespaced", "v1alpha1", true, nil, crdAge),
	)
	c.SeedDiscovered(demoDiscoveredKind("Application", "applications", "applications.argoproj.io", false, appCols))
	c.SeedDiscovered(demoDiscoveredKind("AppProject", "appprojects", "appprojects.argoproj.io", false, nil))

	c.Seed(kube.ResourceKind("Application"),
		demoArgoApplication("api", "argocd", "default", age(20*24*time.Hour), "Synced", "Healthy"),
		demoArgoApplication("worker", "argocd", "default", age(20*24*time.Hour), "OutOfSync", "Degraded"),
		demoArgoApplication("web", "argocd", "default", age(10*24*time.Hour), "Synced", "Progressing"),
	)
	c.Seed(kube.ResourceKind("AppProject"),
		demoCR(group+"/v1alpha1", "AppProject", "default", "argocd", age(45*24*time.Hour), nil),
	)

	c.Seed(kube.KindDeployment,
		demoStableDeployment("argocd-server", "argocd", "quay.io/argoproj/argocd:v2.10.7", 1, crdAge),
		demoStableDeployment("argocd-repo-server", "argocd", "quay.io/argoproj/argocd:v2.10.7", 1, crdAge),
		demoStableDeployment("argocd-redis", "argocd", "redis:7.0.15-alpine", 1, crdAge),
	)
	c.Seed(kube.KindStatefulSet, demoStatefulSetN("argocd-application-controller", "argocd", 1, crdAge))
	c.Seed(kube.KindService, demoLoadBalancerService("argocd-server", "argocd", 443, "203.0.113.20", crdAge))
	c.Seed(kube.KindPod,
		demoOwnedPod("argocd-server-6f9c8d-p4k9w", "argocd", age(12*24*time.Hour), "node-a", "ReplicaSet", "argocd-server-6f9c8d"),
		demoOwnedPod("argocd-application-controller-0", "argocd", age(12*24*time.Hour), "node-b", "StatefulSet", "argocd-application-controller"),
	)
}

// demoArgoApplication builds an Application instance whose printer-column
// fields (Sync/Health Status) carry the meaningful state — no synthetic
// Ready condition, per demoArgoCDFixtures' doc comment.
func demoArgoApplication(name, ns, project string, created metav1.Time, syncStatus, healthStatus string) *unstructured.Unstructured {
	u := demoCR("argoproj.io/v1alpha1", "Application", name, ns, created, map[string]any{
		"project":     project,
		"destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": ns},
	})
	u.Object["status"] = map[string]any{
		"sync":   map[string]any{"status": syncStatus},
		"health": map[string]any{"status": healthStatus},
	}
	return u
}

// demoCR builds a generic custom-resource instance: metadata + optional
// spec + optional status.conditions. The one shared constructor behind
// every non-cert-manager CRD instance below (cert-manager's own
// demoCertificate/demoCertificateRequest/demoClusterIssuer predate it and
// are left as they are) — CRD support being data, not code, extends to the
// fixtures that exercise it.
func demoCR(apiVersion, kind, name, ns string, created metav1.Time, spec map[string]any, conditions ...map[string]any) *unstructured.Unstructured {
	meta := map[string]any{
		"name":              name,
		"creationTimestamp": created.UTC().Format(time.RFC3339),
	}
	if ns != "" {
		meta["namespace"] = ns
	}
	obj := map[string]any{
		"apiVersion": apiVersion,
		"kind":       kind,
		"metadata":   meta,
	}
	if spec != nil {
		obj["spec"] = spec
	}
	if len(conditions) > 0 {
		conds := make([]any, len(conditions))
		for i, cond := range conditions {
			conds[i] = cond
		}
		obj["status"] = map[string]any{"conditions": conds}
	}
	return &unstructured.Unstructured{Object: obj}
}

// readyCondition builds a status.conditions entry of type "Ready" — the
// condition type projectCustomResource (internal/resources/crd.go) scans
// for on every discovered kind.
func readyCondition(ready bool, message string) map[string]any {
	status := "True"
	if !ready {
		status = "False"
	}
	cond := map[string]any{"type": "Ready", "status": status}
	if message != "" {
		cond["message"] = message
	}
	return cond
}

// demoDiscoveredKind builds the DiscoveredKind counterpart of one demoCRD
// call — kept as a small parallel constructor rather than re-deriving it
// from the unstructured object (fake fixtures build both representations by
// hand, same as every other demo* helper in this file).
func demoDiscoveredKind(kind, plural, crdName string, clusterScoped bool, cols []kube.PrinterColumn) kube.DiscoveredKind {
	return kube.DiscoveredKind{
		GVR:            schema.GroupVersionResource{Group: "cert-manager.io", Version: "v1", Resource: plural},
		Kind:           kind,
		Plural:         plural,
		Group:          "cert-manager.io",
		Versions:       []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		ClusterScoped:  clusterScoped,
		PrinterColumns: cols,
		Established:    true,
		CRDName:        crdName,
	}
}

// demoCRD builds a CustomResourceDefinition unstructured object shaped the
// same way a real apiserver would serve it — the 14b CRDs list's row source.
func demoCRD(name, group, kind, plural, scope, version string, established bool, printerCols []kube.PrinterColumn, created metav1.Time) *unstructured.Unstructured {
	status := "True"
	if !established {
		status = "False"
	}
	cols := make([]any, 0, len(printerCols))
	for _, c := range printerCols {
		cols = append(cols, map[string]any{"name": c.Name, "type": c.Type, "jsonPath": c.JSONPath})
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apiextensions.k8s.io/v1",
		"kind":       "CustomResourceDefinition",
		"metadata": map[string]any{
			"name":              name,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"group": group,
			"names": map[string]any{"kind": kind, "plural": plural},
			"scope": scope,
			"versions": []any{
				map[string]any{
					"name": version, "served": true, "storage": true,
					"additionalPrinterColumns": cols,
				},
			},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Established", "status": status},
			},
		},
	}}
}

// demoCertificate builds a cert-manager.io/v1 Certificate instance. The
// not-ready message ("Issuing certificate as Secret does not exist") is the
// design doc's own §14d example — CONDITIONS renders it verbatim, never
// paraphrased.
func demoCertificate(name, ns string, ready bool, secret, issuer string, created metav1.Time) *unstructured.Unstructured {
	status, message := "True", "Certificate is up to date and has not expired"
	if !ready {
		status, message = "False", "Issuing certificate as Secret does not exist"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "Certificate",
		"metadata": map[string]any{
			"name": name, "namespace": ns,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"secretName": secret,
			"issuerRef":  map[string]any{"name": issuer, "kind": "ClusterIssuer"},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": status, "message": message},
			},
		},
	}}
}

func demoCertificateRequest(name, ns string, ready bool, issuer string, created metav1.Time) *unstructured.Unstructured {
	status := "True"
	if !ready {
		status = "False"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "CertificateRequest",
		"metadata": map[string]any{
			"name": name, "namespace": ns,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"issuerRef": map[string]any{"name": issuer, "kind": "ClusterIssuer"},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": status},
			},
		},
	}}
}

func demoClusterIssuer(name string, ready bool, created metav1.Time) *unstructured.Unstructured {
	status := "True"
	if !ready {
		status = "False"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "cert-manager.io/v1",
		"kind":       "ClusterIssuer",
		"metadata": map[string]any{
			"name":              name,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"acme": map[string]any{"email": "ops@demo.local"},
		},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Ready", "status": status},
			},
		},
	}}
}

func demoPod(name, ns string, created metav1.Time, phase corev1.PodPhase, qos corev1.PodQOSClass, node string, ready bool, restarts int32, terminated *corev1.ContainerStateTerminated) *corev1.Pod {
	state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{StartedAt: created}}
	if terminated != nil {
		state = corev1.ContainerState{Terminated: terminated}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec: corev1.PodSpec{
			NodeName: node,
			Containers: []corev1.Container{{
				Name: "app", Image: "app:1.0",
				// Every demo pod carries a request/limit pair so 2a/5a/6a's
				// CPU/MEM bars have a real denominator in --demo mode
				// (CLAUDE.md: "the fake provider must stay feature-complete
				// for tests/demo mode") — PodMetricsByNamespace below
				// synthesizes the matching usage numerator.
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("100m"),
						corev1.ResourceMemory: resource.MustParse("128Mi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			}},
		},
		Status: corev1.PodStatus{
			Phase:    phase,
			PodIP:    "10.0.0.5",
			QOSClass: qos,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: ready, RestartCount: restarts, State: state},
			},
		},
	}
}

func demoCrashLoopPod(name, ns string, created metav1.Time, node string) *corev1.Pod {
	pod := demoPod(name, ns, created, corev1.PodRunning, corev1.PodQOSBurstable, node, false, 6, nil)
	pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"},
	}
	pod.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{
			ExitCode:   1,
			Reason:     "Error",
			FinishedAt: metav1.NewTime(created.Add(4 * time.Minute)),
		},
	}
	return pod
}

func demoPendingPod(name, ns string, created metav1.Time) *corev1.Pod {
	pod := demoPod(name, ns, created, corev1.PodPending, corev1.PodQOSBurstable, "", false, 0, nil)
	pod.Status.ContainerStatuses[0].State = corev1.ContainerState{
		Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"},
	}
	return pod
}

func demoCompletedPod(name, ns string, created metav1.Time) *corev1.Pod {
	return demoPod(name, ns, created, corev1.PodSucceeded, corev1.PodQOSBestEffort, "node-a", true, 0,
		&corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed", FinishedAt: metav1.NewTime(created.Add(2 * time.Minute))})
}

func demoMidRolloutDeployment(name, ns string, created metav1.Time) *appsv1.Deployment {
	replicas := int32(3)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created, Generation: 2},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "api:2.1"}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:           3,
			UpdatedReplicas:    1,
			ReadyReplicas:      2,
			AvailableReplicas:  2,
			ObservedGeneration: 1, // < Generation: rollout still in progress
		},
	}
}

func demoReplicaSet(name, ns, deployment string, created metav1.Time) *appsv1.ReplicaSet {
	replicas := int32(1)
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, CreationTimestamp: created,
			OwnerReferences: []metav1.OwnerReference{{Kind: "Deployment", Name: deployment}},
			// The revision annotation is what a real kube-controller-manager
			// stamps on every ReplicaSet it owns — tasks/timeline's 16b
			// revision rail reads it (kube.TimelineFromRollouts), so demo
			// mode needs it too for that rail to have anything to show.
			Annotations: map[string]string{"deployment.kubernetes.io/revision": "1"},
		},
		Spec:   appsv1.ReplicaSetSpec{Replicas: &replicas},
		Status: appsv1.ReplicaSetStatus{Replicas: 1, ReadyReplicas: 1},
	}
}

// demoStatefulSet is the 1-replica case of demoStatefulSetN — kept as its
// own name since "worker"'s call site predates the general form.
func demoStatefulSet(name, ns string, created metav1.Time) *appsv1.StatefulSet {
	return demoStatefulSetN(name, ns, 1, created)
}

func demoStatefulSetN(name, ns string, replicas int32, created metav1.Time) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec:       appsv1.StatefulSetSpec{Replicas: &replicas},
		Status:     appsv1.StatefulSetStatus{Replicas: replicas, ReadyReplicas: replicas},
	}
}

// demoStableDeployment is a fully-ready Deployment with no rollout in
// progress — every add-on namespace's steady-state workloads use this
// rather than demoMidRolloutDeployment, which stays "api"'s own exemplar.
func demoStableDeployment(name, ns, image string, replicas int32, created metav1.Time) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created, Generation: 1},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: name, Image: image}}},
			},
		},
		Status: appsv1.DeploymentStatus{
			Replicas:           replicas,
			UpdatedReplicas:    replicas,
			ReadyReplicas:      replicas,
			AvailableReplicas:  replicas,
			ObservedGeneration: 1,
		},
	}
}

// demoDaemonSetReady is a fully-scheduled, fully-ready DaemonSet — every
// node in the demo cluster running its pod.
func demoDaemonSetReady(name, ns string, desired int32, created metav1.Time) *appsv1.DaemonSet {
	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: desired,
			NumberReady:            desired,
			NumberAvailable:        desired,
		},
	}
}

// demoOwnedPod is a running, ready pod owned by ownerKind/ownerName — the
// representative single pod each add-on Deployment/DaemonSet/StatefulSet
// above gets, the same asymmetry "api"'s own single apiPod already has
// against its 3-replica Deployment (a whole cluster of fixtures beats
// exhaustive replica-for-replica pod objects for a demo dataset).
func demoOwnedPod(name, ns string, created metav1.Time, node, ownerKind, ownerName string) *corev1.Pod {
	pod := demoPod(name, ns, created, corev1.PodRunning, corev1.PodQOSBurstable, node, true, 0, nil)
	pod.OwnerReferences = []metav1.OwnerReference{{Kind: ownerKind, Name: ownerName}}
	return pod
}

func demoService(name, ns string, selector map[string]string, created metav1.Time) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "10.0.0.10",
			Selector:  selector,
			Ports:     []corev1.ServicePort{{Port: 80}},
		},
	}
}

// demoLoadBalancerService is a Service with an assigned external IP — the
// shape an ingress controller/API-gateway Service takes, distinct from
// demoService's internal ClusterIP shape.
func demoLoadBalancerService(name, ns string, port int32, externalIP string, created metav1.Time) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeLoadBalancer,
			ClusterIP: "10.0.0.20",
			Ports:     []corev1.ServicePort{{Port: port}},
		},
		Status: corev1.ServiceStatus{
			LoadBalancer: corev1.LoadBalancerStatus{
				Ingress: []corev1.LoadBalancerIngress{{IP: externalIP}},
			},
		},
	}
}

func demoIngress(name, ns, backendService, host string, created metav1.Time) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec: networkingv1.IngressSpec{
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path:     "/",
							PathType: &pathType,
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: backendService,
									Port: networkingv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
		Status: networkingv1.IngressStatus{
			LoadBalancer: networkingv1.IngressLoadBalancerStatus{
				Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "203.0.113.10"}},
			},
		},
	}
}

// demoIngressWithTLS exercises 23a's TLS strip and both remaining backend
// glyphs beyond demoIngress' plain single-rule/single-● shape: goodBackend's
// path resolves ●, brokenBackend's path resolves ✕ (no such Service exists).
func demoIngressWithTLS(name, ns, goodBackend, brokenBackend, host, secretName string, created metav1.Time) *networkingv1.Ingress {
	pathType := networkingv1.PathTypePrefix
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Spec: networkingv1.IngressSpec{
			TLS: []networkingv1.IngressTLS{{Hosts: []string{host}, SecretName: secretName}},
			Rules: []networkingv1.IngressRule{{
				Host: host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{
							{
								Path: "/", PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{Name: goodBackend, Port: networkingv1.ServiceBackendPort{Number: 80}},
								},
							},
							{
								Path: "/admin", PathType: &pathType,
								Backend: networkingv1.IngressBackend{
									Service: &networkingv1.IngressServiceBackend{Name: brokenBackend, Port: networkingv1.ServiceBackendPort{Number: 80}},
								},
							},
						},
					},
				},
			}},
		},
		Status: networkingv1.IngressStatus{
			LoadBalancer: networkingv1.IngressLoadBalancerStatus{
				Ingress: []networkingv1.IngressLoadBalancerIngress{{IP: "203.0.113.11"}},
			},
		},
	}
}

// demoTLSSecret builds a kubernetes.io/tls Secret whose tls.crt is a real
// (self-signed) certificate expiring at notAfter — routetable's cert-expiry
// parsing (crypto/x509) needs actual DER/PEM data to exercise, not a stub
// byte slice.
func demoTLSSecret(name, ns string, notAfter time.Time, created metav1.Time) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Type:       corev1.SecretTypeTLS,
		Data:       map[string][]byte{"tls.crt": demoTLSCert(notAfter), "tls.key": []byte("demo-key")},
	}
}

// demoTLSCert self-signs a throwaway certificate valid from one year before
// notAfter through notAfter.
func demoTLSCert(notAfter time.Time) []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "demo"},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// demoGatewayAPIFixtures seeds 23b's Gateway API fixtures — discovered like
// any CRD (Gateway/HTTPRoute are never DefaultRegistry entries): a Gateway
// with an HTTPS+HTTP listener pair, an accepted HTTPRoute with a weighted
// canary split (demonstrates the "└ same match" + "%" rows), and a
// not-accepted HTTPRoute (demonstrates the ATTACHED red-row footgun
// docs/design README.md §23b calls out).
func demoGatewayAPIFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	const group = "gateway.networking.k8s.io"
	gwAge := age(45 * 24 * time.Hour)

	c.Seed(kube.KindCustomResourceDefinition,
		demoCRD("gateways."+group, group, "Gateway", "gateways", "Namespaced", "v1", true, nil, gwAge),
		demoCRD("httproutes."+group, group, "HTTPRoute", "httproutes", "Namespaced", "v1", true, nil, gwAge),
	)
	c.SeedDiscovered(kube.DiscoveredKind{
		GVR:         schema.GroupVersionResource{Group: group, Version: "v1", Resource: "gateways"},
		Kind:        "Gateway",
		Plural:      "gateways",
		Group:       group,
		Versions:    []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "gateways." + group,
	})
	c.SeedDiscovered(kube.DiscoveredKind{
		GVR:         schema.GroupVersionResource{Group: group, Version: "v1", Resource: "httproutes"},
		Kind:        "HTTPRoute",
		Plural:      "httproutes",
		Group:       group,
		Versions:    []kube.CRDVersion{{Name: "v1", Served: true, Storage: true}},
		Established: true,
		CRDName:     "httproutes." + group,
	})

	// web-canary has no matching pods (Ready=0) so its split leg renders ◐,
	// distinct from web's ● (the same Service demoProductionFixtures' pods
	// already match).
	c.Seed(kube.KindService, demoService("web-canary", "production", map[string]string{"app": "web-canary"}, gwAge))
	c.Seed(kube.KindSecret, demoTLSSecret("gw-tls", "production", time.Now().Add(100*24*time.Hour), gwAge))
	c.Seed(kube.ResourceKind("Gateway"), demoGateway("public", "production", gwAge, 1))
	c.Seed(kube.ResourceKind("HTTPRoute"),
		demoHTTPRoute("web-route", "production", "public", gwAge, true, []map[string]any{
			{"name": "web", "port": int64(80), "weight": int64(90)},
			{"name": "web-canary", "port": int64(80), "weight": int64(10)},
		}),
		demoHTTPRoute("orphan-route", "production", "public", gwAge, false, []map[string]any{
			{"name": "web", "port": int64(80), "weight": int64(1)},
		}),
	)
}

// demoGateway builds a Gateway with an HTTPS (TLS-terminating) and an HTTP
// listener; attachedHTTPS is the https listener's status.listeners
// attachedRoutes count (the 23b "N routes attached" cell).
func demoGateway(name, ns string, created metav1.Time, attachedHTTPS int64) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "Gateway",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         ns,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"gatewayClassName": "nginx",
			"listeners": []any{
				map[string]any{
					"name": "https", "protocol": "HTTPS", "port": int64(443), "hostname": "*.demo.local",
					"tls": map[string]any{"certificateRefs": []any{map[string]any{"name": "gw-tls"}}},
				},
				map[string]any{
					"name": "http", "protocol": "HTTP", "port": int64(80), "hostname": "*.demo.local",
				},
			},
		},
		"status": map[string]any{
			"listeners": []any{
				map[string]any{"name": "https", "attachedRoutes": attachedHTTPS},
				map[string]any{"name": "http", "attachedRoutes": int64(0)},
			},
		},
	}}
}

// demoHTTPRoute builds an HTTPRoute attached to parentName's "https"
// listener, one rule matching "/" with the given weighted backendRefs.
// accepted false renders the design's "verbatim condition message" ATTACHED
// footgun copy.
func demoHTTPRoute(name, ns, parentName string, created metav1.Time, accepted bool, backends []map[string]any) *unstructured.Unstructured {
	condStatus := "True"
	cond := map[string]any{"type": "Accepted", "status": condStatus}
	if !accepted {
		cond["status"] = "False"
		cond["message"] = "no matching listener hostname"
	}

	refs := make([]any, len(backends))
	for i, b := range backends {
		refs[i] = b
	}

	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1",
		"kind":       "HTTPRoute",
		"metadata": map[string]any{
			"name":              name,
			"namespace":         ns,
			"creationTimestamp": created.UTC().Format(time.RFC3339),
		},
		"spec": map[string]any{
			"parentRefs": []any{map[string]any{"name": parentName}},
			"hostnames":  []any{name + ".demo.local"},
			"rules": []any{
				map[string]any{
					"matches":     []any{map[string]any{"path": map[string]any{"type": "PathPrefix", "value": "/"}}},
					"backendRefs": refs,
				},
			},
		},
		"status": map[string]any{
			"parents": []any{
				map[string]any{
					"parentRef":  map[string]any{"name": parentName, "sectionName": "https"},
					"conditions": []any{cond},
				},
			},
		},
	}}
}

func demoNode(name string, ready, memoryPressure, cordoned bool) *corev1.Node {
	conditions := []corev1.NodeCondition{
		{Type: corev1.NodeReady, Status: boolCondition(ready)},
	}
	if memoryPressure {
		conditions = append(conditions, corev1.NodeCondition{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue})
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions: conditions,
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
		},
	}
}

func boolCondition(v bool) corev1.ConditionStatus {
	if v {
		return corev1.ConditionTrue
	}
	return corev1.ConditionFalse
}

func demoNamespace(name string) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	}
}

func demoConfigMap(name, ns string, created metav1.Time) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Data:       map[string]string{"key": "value"},
	}
}

func demoSecret(name, ns string, created metav1.Time) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: created},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"key": []byte("value")},
	}
}

func demoEvent(name, ns, kind, objName, typ, reason, message string, count int32, last metav1.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta:     metav1.ObjectMeta{Name: name, Namespace: ns},
		InvolvedObject: corev1.ObjectReference{Kind: kind, Name: objName, Namespace: ns},
		Type:           typ,
		Reason:         reason,
		Message:        message,
		Count:          count,
		FirstTimestamp: last,
		LastTimestamp:  last,
	}
}

// demoHelmReleaseFixtures seeds 18a's Helm Releases list — encoded as real
// helm.sh/release.v1 Secrets (kube.EncodeHelmReleaseSecret) so browsing goes
// through the exact same decode path a real cluster's release secrets would.
// Five releases match docs/design README.md §18a's own strip example
// verbatim ("3 deployed · 1 pending-upgrade · 1 failed"); postgresql and
// redis carry multiple superseded revisions so 'h' history has a real rail
// to show.
func demoHelmReleaseFixtures(c *Cluster, age func(time.Duration) metav1.Time) {
	rev := func(name, namespace, chart, chartVersion, appVersion, status, reason string, revision int, values string, d time.Duration) *corev1.Secret {
		return kube.EncodeHelmReleaseSecret(kube.HelmRelease{
			Name: name, Namespace: namespace, Chart: chart, ChartVersion: chartVersion, AppVersion: appVersion,
			Revision: revision, Status: status, StatusReason: reason, Updated: age(d).Time, Values: values,
			Manifest: "# Source: " + chart + "/templates/deployment.yaml\n",
		})
	}

	// postgresql (production): three revisions, the newest deployed — 'h'
	// shows a real superseded/superseded/deployed rail.
	c.Seed(kube.KindSecret,
		rev("postgresql", "production", "postgresql", "12.1.7", "15.3.0", "superseded", "", 1, "auth:\n  enablePostgresUser: true\n", 20*24*time.Hour),
		rev("postgresql", "production", "postgresql", "12.1.8", "15.4.0", "superseded", "", 2, "auth:\n  enablePostgresUser: true\nprimary:\n  persistence:\n    size: 8Gi\n", 10*24*time.Hour),
		rev("postgresql", "production", "postgresql", "12.1.9", "15.4.0", "deployed", "", 3, "auth:\n  enablePostgresUser: true\nprimary:\n  persistence:\n    size: 8Gi\n", 2*24*time.Hour),
	)
	// redis (production): two revisions, deployed.
	c.Seed(kube.KindSecret,
		rev("redis", "production", "redis", "18.1.4", "7.2.3", "superseded", "", 1, "architecture: standalone\n", 15*24*time.Hour),
		rev("redis", "production", "redis", "18.1.5", "7.2.4", "deployed", "", 2, "architecture: standalone\nauth:\n  enabled: true\n", 6*24*time.Hour),
	)
	// grafana (monitoring): deployed, single revision.
	c.Seed(kube.KindSecret,
		rev("grafana", "monitoring", "grafana", "7.3.0", "10.4.2", "deployed", "", 1, "adminUser: admin\npersistence:\n  enabled: true\n", 20*24*time.Hour),
	)
	// prometheus (monitoring): mid-upgrade — the strip's "◌ 1 pending-upgrade".
	c.Seed(kube.KindSecret,
		rev("prometheus", "monitoring", "kube-prometheus-stack", "58.2.1", "0.73.0", "pending-upgrade", "", 2, "grafana:\n  enabled: false\nalertmanager:\n  enabled: true\n", 3*time.Minute),
	)
	// broken-app (default): the strip's "✕ 1 failed" — STATUS carries the
	// reason verbatim per §18a ("failed · hook timeout").
	c.Seed(kube.KindSecret,
		rev("broken-app", "default", "mychart", "1.0.0", "2.1.0", "failed", "hook timeout", 2, "replicaCount: 2\n", 40*time.Minute),
	)
}
