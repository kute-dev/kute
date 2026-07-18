package kube

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// PathCheck is one kubeconfig location kute looked at and why it didn't
// yield a usable config — the 10b "LOOKED IN" box's rows.
type PathCheck struct {
	Label  string // "$KUBECONFIG", "~/.kube/config"
	Path   string // resolved path; empty when the env var itself is unset
	Reason string // "not set", "no such file", or a parse error
}

// ConfigLookupError is returned when no kubeconfig could be found anywhere
// kute looks and in-cluster config also isn't available (10b) — a
// structured alternative to the raw clientcmd error so the setup screen can
// show exactly what was checked instead of one opaque message.
type ConfigLookupError struct {
	Paths []PathCheck
	cause error
}

func (e *ConfigLookupError) Error() string {
	return fmt.Sprintf("no kubeconfig found: %v", e.cause)
}

func (e *ConfigLookupError) Unwrap() error { return e.cause }

func buildConfigLookupError(envPath, defaultPath string, cause error) *ConfigLookupError {
	var checks []PathCheck
	if envPath == "" {
		checks = append(checks, PathCheck{Label: "$KUBECONFIG", Reason: "not set"})
	} else {
		checks = append(checks, PathCheck{Label: "$KUBECONFIG", Path: envPath, Reason: pathFailureReason(envPath)})
	}
	if defaultPath != "" {
		checks = append(checks, PathCheck{Label: "~/.kube/config", Path: defaultPath, Reason: pathFailureReason(defaultPath)})
	}
	return &ConfigLookupError{Paths: checks, cause: cause}
}

func pathFailureReason(path string) string {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "no such file"
		}
		return err.Error()
	}
	return "invalid kubeconfig"
}

type Client struct {
	Interface  kubernetes.Interface
	RESTConfig *rest.Config
	Context    Context
}

func NewClient() (Client, error) {
	// Empty context override means "use the kubeconfig's current-context".
	return newClientForContext("")
}

// NewClientForContext builds a Client pinned to the named kubeconfig context,
// overriding the file's current-context. It backs Cluster.SwitchContext.
func NewClientForContext(contextName string) (Client, error) {
	return newClientForContext(contextName)
}

func newClientForContext(contextName string) (Client, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	envPath := os.Getenv("KUBECONFIG")
	defaultPath := ""
	if home, err := os.UserHomeDir(); err == nil {
		defaultPath = filepath.Join(home, ".kube", "config")
	}
	if envPath != "" {
		loadingRules.ExplicitPath = envPath
	} else if defaultPath != "" {
		loadingRules.ExplicitPath = defaultPath
	}

	configOverrides := &clientcmd.ConfigOverrides{}
	if contextName != "" {
		configOverrides.CurrentContext = contextName
	}
	clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		if inClusterConfig, inClusterErr := rest.InClusterConfig(); inClusterErr == nil {
			restConfig = inClusterConfig
		} else {
			return Client{}, buildConfigLookupError(envPath, defaultPath, err)
		}
	}

	// client-go's defaults (QPS 5, Burst 10) trip client-side throttling
	// under the app's 2s metrics polling; raise them so requests aren't
	// queued for seconds at a time (k9s uses the same order of magnitude).
	restConfig.QPS = 50
	restConfig.Burst = 100

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return Client{}, err
	}

	rawConfig, err := clientConfig.RawConfig()
	ctx := Context{ClusterName: "cluster", Namespace: "default"}
	if err == nil {
		currentName := rawConfig.CurrentContext
		ctx.ContextName = currentName
		if current, ok := rawConfig.Contexts[currentName]; ok {
			ctx.ClusterName = current.Cluster
			ctx.Namespace = current.Namespace
			if ctx.Namespace == "" {
				ctx.Namespace = "default"
			}
			ctx.UserName = current.AuthInfo
			if authInfo, ok := rawConfig.AuthInfos[current.AuthInfo]; ok {
				if cn, orgs, ok := clientCertIdentity(authInfo); ok {
					// The client cert's Subject is the identity the API
					// server actually authenticates — often nothing like
					// the kubeconfig user entry's own name (see UserName's
					// doc comment). This is a strictly better answer than
					// the AuthInfo name whenever it's available.
					ctx.UserName = cn
					ctx.UserGroups = orgs
				}
			}
		}
	}

	return Client{Interface: clientset, RESTConfig: restConfig, Context: ctx}, nil
}

// clientCertIdentity extracts the real identity a client-certificate
// AuthInfo authenticates as: the leaf certificate's Subject CommonName
// (the username) and Organization fields (Kubernetes' own convention for
// encoding a client cert's group memberships — see
// k8s.io/apiserver's x509 authenticator). ok is false for any other auth
// mode (token, exec plugin, OIDC, …) or a cert that fails to parse.
func clientCertIdentity(authInfo *clientcmdapi.AuthInfo) (commonName string, organizations []string, ok bool) {
	data := authInfo.ClientCertificateData
	if len(data) == 0 && authInfo.ClientCertificate != "" {
		var err error
		data, err = os.ReadFile(authInfo.ClientCertificate)
		if err != nil {
			return "", nil, false
		}
	}
	if len(data) == 0 {
		return "", nil, false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", nil, false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", nil, false
	}
	if cert.Subject.CommonName == "" {
		return "", nil, false
	}
	return cert.Subject.CommonName, cert.Subject.Organization, true
}

// KubeconfigPath returns the kubeconfig file path kute resolves ($KUBECONFIG,
// else ~/.kube/config), and whether it could be determined — for the context
// palette's right-hand hint (7a: "~/.kube/config · 5 contexts").
func KubeconfigPath() (string, bool) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		return kubeconfig, true
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".kube", "config"), true
	}
	return "", false
}

// AvailableContexts returns the kubeconfig's context names (sorted) and the
// current-context name, for the context switcher. It reads the merged kubeconfig
// without contacting any cluster.
func AvailableContexts() (names []string, current string, err error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		loadingRules.ExplicitPath = kubeconfig
	} else if home, homeErr := os.UserHomeDir(); homeErr == nil {
		loadingRules.ExplicitPath = filepath.Join(home, ".kube", "config")
	}
	raw, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, &clientcmd.ConfigOverrides{}).RawConfig()
	if err != nil {
		return nil, "", err
	}
	names = make([]string, 0, len(raw.Contexts))
	for name := range raw.Contexts {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, raw.CurrentContext, nil
}
