package app

const (
	DefaultAppName = "kute"
	UnknownCluster = "cluster unavailable"
)

type Config struct {
	AppName string
	Cluster string
	// Demo substitutes kube/fake for the real cluster behind the same
	// seams (--demo flag).
	Demo bool
	// Keycast enables a bottom-right chip echoing recent keypresses, for
	// demo recording (--keycast flag).
	Keycast bool
	// Theme overrides theme selection: "dark" or "light" (--theme flag).
	// Empty defers to the config file's theme: key, then terminal
	// background detection (decision #3, mvp-plan.md).
	Theme string
	// Version is kute's own running build version (main.go's ldflags-
	// injected version var) — threaded into Session.Version, the "you run
	// X" side of every 28a/28b comparison. Empty (a plain `go run`/test
	// build with no ldflags) falls back to tui.Version in BuildSession.
	Version string
	// Commit and Date are the other two ldflags-injected build vars. They
	// are diagnostics-only: nothing in the UI shows them, but every crash
	// report and `--version` line names the exact build.
	Commit string
	Date   string
	// LogFile is --log-file's destination for the error/klog stream. Empty
	// keeps the stream in memory only, where a crash report can still reach
	// the tail of it (see internal/diag).
	LogFile string
	// Context pins the kubeconfig context to launch against (--context),
	// overriding both the last-used context and the kubeconfig's own
	// current-context. Empty keeps the normal restore.
	Context string
	// Namespace pins the namespace to launch in (-n/--namespace), the
	// highest-precedence source: it beats the persisted per-context
	// namespace and the context's own default.
	Namespace string
	// Kubeconfig pins the kubeconfig file to read (--kubeconfig), ahead of
	// $KUBECONFIG and ~/.kube/config. Applied via kube.SetKubeconfigPath
	// before any client is built.
	Kubeconfig string
	// NoUpdateCheck disables the 28a/28b ambient release-feed check for this
	// invocation only (--no-update-check flag). It overrides the loaded
	// config file's update.check in memory — see BuildSession — and is
	// never persisted back to ~/.config/kute/config.yaml.
	NoUpdateCheck bool
}

func DefaultConfig() Config {
	return Config{
		AppName: DefaultAppName,
		Cluster: UnknownCluster,
	}
}
