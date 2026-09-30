package poddetail

import (
	"cmp"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/kute-dev/kute/internal/kube"
)

// kubeRootCAConfigMap is the ConfigMap every pod's auto-injected
// kube-api-access-* projected volume mounts. It's a real reference, so
// ENV & MOUNTS still shows it, but listing it in RELATED would put the same
// link on every pod in the cluster.
const kubeRootCAConfigMap = "kube-root-ca.crt"

// configRef is one Secret or ConfigMap the pod spec names. Every configRef
// comes from the spec alone: poddetail never reads the Secret or ConfigMap
// caches to check existence. A Secret read would start the cluster-wide Secret
// informer (Helm release Secrets alone can be megabytes), and many users can't
// list Secrets at all. The spec is already the authoritative list of what the
// pod references.
type configRef struct {
	Kind kube.ResourceKind
	Name string
}

// envSource is one ENV & MOUNTS env row: a single variable drawn from a
// Secret/ConfigMap key (secretKeyRef/configMapKeyRef), or a whole object
// imported through envFrom (All set, Var holding the optional prefix). It
// never carries a value.
type envSource struct {
	Var  string
	Kind kube.ResourceKind
	Name string
	Key  string
	All  bool
}

// mountSource is one ENV & MOUNTS mount row: a volumeMount of the
// container, with its volume's source pre-formatted (Source), since a volume
// can be any of a dozen types and only the Secret/ConfigMap ones are links.
type mountSource struct {
	Path     string
	SubPath  string
	ReadOnly bool
	Source   string
}

// containerSources is one container's env and mount references, keyed by
// container name in Model.sources.
type containerSources struct {
	Env    []envSource
	Mounts []mountSource
}

// configError is one container stuck in CreateContainerConfigError: the
// kubelet refused to build it because a referenced Secret/ConfigMap (or
// one of their keys) is missing. Message is the kubelet's own text, which
// already names the object and key.
type configError struct {
	Container string
	Message   string
}

// podConfigRefs returns every Secret and ConfigMap spec references: volumes
// (including projected sources), each container's envFrom and env valueFrom,
// and imagePullSecrets. Secrets come first, then ConfigMaps, each sorted by
// name and deduped. The kube-root-ca.crt ConfigMap from the injected service
// account volume is skipped.
func podConfigRefs(spec corev1.PodSpec) []configRef {
	seen := map[configRef]bool{}
	var refs []configRef
	add := func(kind kube.ResourceKind, name string) {
		ref := configRef{Kind: kind, Name: name}
		if name == "" || seen[ref] {
			return
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	for _, v := range spec.Volumes {
		switch {
		case v.Secret != nil:
			add(kube.KindSecret, v.Secret.SecretName)
		case v.ConfigMap != nil:
			add(kube.KindConfigMap, v.ConfigMap.Name)
		case v.Projected != nil:
			for _, s := range v.Projected.Sources {
				if s.Secret != nil {
					add(kube.KindSecret, s.Secret.Name)
				}
				if s.ConfigMap != nil && s.ConfigMap.Name != kubeRootCAConfigMap {
					add(kube.KindConfigMap, s.ConfigMap.Name)
				}
			}
		}
	}
	forEachContainerEnv(spec, func(_ string, envFrom []corev1.EnvFromSource, env []corev1.EnvVar) {
		for _, ef := range envFrom {
			if ef.SecretRef != nil {
				add(kube.KindSecret, ef.SecretRef.Name)
			}
			if ef.ConfigMapRef != nil {
				add(kube.KindConfigMap, ef.ConfigMapRef.Name)
			}
		}
		for _, e := range env {
			if e.ValueFrom == nil {
				continue
			}
			if r := e.ValueFrom.SecretKeyRef; r != nil {
				add(kube.KindSecret, r.Name)
			}
			if r := e.ValueFrom.ConfigMapKeyRef; r != nil {
				add(kube.KindConfigMap, r.Name)
			}
		}
	})
	for _, s := range spec.ImagePullSecrets {
		add(kube.KindSecret, s.Name)
	}
	slices.SortStableFunc(refs, func(a, b configRef) int {
		return cmp.Or(
			cmp.Compare(configRefKindOrder(a.Kind), configRefKindOrder(b.Kind)),
			cmp.Compare(a.Name, b.Name),
		)
	})
	return refs
}

func configRefKindOrder(kind kube.ResourceKind) int {
	if kind == kube.KindSecret {
		return 0
	}
	return 1
}

// forEachContainerEnv visits every container (regular, init and ephemeral)
// with its name, envFrom and env.
func forEachContainerEnv(spec corev1.PodSpec, fn func(name string, envFrom []corev1.EnvFromSource, env []corev1.EnvVar)) {
	for _, c := range spec.Containers {
		fn(c.Name, c.EnvFrom, c.Env)
	}
	for _, c := range spec.InitContainers {
		fn(c.Name, c.EnvFrom, c.Env)
	}
	for _, c := range spec.EphemeralContainers {
		fn(c.Name, c.EnvFrom, c.Env)
	}
}

// forEachContainerMounts visits every container's volumeMounts.
func forEachContainerMounts(spec corev1.PodSpec, fn func(name string, mounts []corev1.VolumeMount)) {
	for _, c := range spec.Containers {
		fn(c.Name, c.VolumeMounts)
	}
	for _, c := range spec.InitContainers {
		fn(c.Name, c.VolumeMounts)
	}
	for _, c := range spec.EphemeralContainers {
		fn(c.Name, c.VolumeMounts)
	}
}

// podContainerSources builds each container's ENV & MOUNTS rows, keyed by
// container name. Container names are unique across regular, init and
// ephemeral containers, so a single map serves every grid. Env rows cover
// only Secret/ConfigMap references: a literal value is never shown (plain
// env is a common place for credentials too), and fieldRef/resourceFieldRef
// refer to the pod itself, not to config objects. Named variables come first,
// then envFrom imports, each in spec order.
func podContainerSources(spec corev1.PodSpec) map[string]containerSources {
	volumes := make(map[string]corev1.Volume, len(spec.Volumes))
	for _, v := range spec.Volumes {
		volumes[v.Name] = v
	}
	out := map[string]containerSources{}
	forEachContainerEnv(spec, func(name string, envFrom []corev1.EnvFromSource, env []corev1.EnvVar) {
		var rows []envSource
		for _, e := range env {
			if e.ValueFrom == nil {
				continue
			}
			if r := e.ValueFrom.SecretKeyRef; r != nil {
				rows = append(rows, envSource{Var: e.Name, Kind: kube.KindSecret, Name: r.Name, Key: r.Key})
			}
			if r := e.ValueFrom.ConfigMapKeyRef; r != nil {
				rows = append(rows, envSource{Var: e.Name, Kind: kube.KindConfigMap, Name: r.Name, Key: r.Key})
			}
		}
		for _, ef := range envFrom {
			if ef.SecretRef != nil {
				rows = append(rows, envSource{Var: ef.Prefix, Kind: kube.KindSecret, Name: ef.SecretRef.Name, All: true})
			}
			if ef.ConfigMapRef != nil {
				rows = append(rows, envSource{Var: ef.Prefix, Kind: kube.KindConfigMap, Name: ef.ConfigMapRef.Name, All: true})
			}
		}
		if len(rows) > 0 {
			cs := out[name]
			cs.Env = rows
			out[name] = cs
		}
	})
	forEachContainerMounts(spec, func(name string, mounts []corev1.VolumeMount) {
		var rows []mountSource
		for _, vm := range mounts {
			rows = append(rows, mountSource{
				Path:     vm.MountPath,
				SubPath:  vm.SubPath,
				ReadOnly: vm.ReadOnly,
				Source:   volumeSourceText(volumes[vm.Name], vm.Name),
			})
		}
		if len(rows) > 0 {
			cs := out[name]
			cs.Mounts = rows
			out[name] = cs
		}
	})
	return out
}

// volumeSourceText describes a volume's source for a mount row —
// "Secret/tls", "PersistentVolumeClaim/data", "emptyDir", or, for a
// projected volume, its sources joined with commas. A mount naming a volume
// the spec doesn't declare (only possible on a malformed object) falls back
// to the volume's own name.
func volumeSourceText(v corev1.Volume, name string) string {
	switch {
	case v.Secret != nil:
		return string(kube.KindSecret) + "/" + v.Secret.SecretName
	case v.ConfigMap != nil:
		return string(kube.KindConfigMap) + "/" + v.ConfigMap.Name
	case v.PersistentVolumeClaim != nil:
		return string(kube.KindPersistentVolumeClaim) + "/" + v.PersistentVolumeClaim.ClaimName
	case v.Projected != nil:
		var parts []string
		for _, s := range v.Projected.Sources {
			switch {
			case s.Secret != nil:
				parts = append(parts, string(kube.KindSecret)+"/"+s.Secret.Name)
			case s.ConfigMap != nil:
				parts = append(parts, string(kube.KindConfigMap)+"/"+s.ConfigMap.Name)
			case s.ServiceAccountToken != nil:
				parts = append(parts, "serviceaccount token")
			case s.DownwardAPI != nil:
				parts = append(parts, "downwardAPI")
			case s.ClusterTrustBundle != nil:
				parts = append(parts, "clusterTrustBundle")
			}
		}
		return "projected · " + strings.Join(parts, ", ")
	case v.EmptyDir != nil:
		return "emptyDir"
	case v.HostPath != nil:
		return "hostPath " + v.HostPath.Path
	case v.DownwardAPI != nil:
		return "downwardAPI"
	case v.CSI != nil:
		return "csi " + v.CSI.Driver
	case v.Ephemeral != nil:
		return "ephemeral volume"
	case v.NFS != nil:
		return "nfs " + v.NFS.Server + ":" + v.NFS.Path
	case v.Image != nil:
		return "image " + v.Image.Reference
	case v.Name == "":
		return name
	default:
		return "volume " + v.Name
	}
}

// podConfigErrors lists every container (init first, since those block the
// rest) whose current state is Waiting with reason CreateContainerConfigError.
func podConfigErrors(status corev1.PodStatus) []configError {
	var out []configError
	for _, statuses := range [][]corev1.ContainerStatus{status.InitContainerStatuses, status.ContainerStatuses} {
		for _, s := range statuses {
			w := s.State.Waiting
			if w == nil || w.Reason != "CreateContainerConfigError" {
				continue
			}
			out = append(out, configError{Container: s.Name, Message: w.Message})
		}
	}
	return out
}
