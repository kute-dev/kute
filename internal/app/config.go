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
	// Theme overrides theme selection: "dark" or "light" (--theme flag).
	// Empty defers to the config file's theme: key, then terminal
	// background detection (decision #3, mvp-plan.md).
	Theme string
}

func DefaultConfig() Config {
	return Config{
		AppName: DefaultAppName,
		Cluster: UnknownCluster,
	}
}
