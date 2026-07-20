package update

import (
	"path/filepath"
	"strings"
)

// InstallInfo is 28b's "installed via" box: which of kute's two real
// distribution channels (README.md's Install section) produced the running
// binary, and the exact command to re-run to upgrade.
type InstallInfo struct {
	Manager string // "homebrew" | "curl"
	Command string
}

const (
	homebrewCommand = "brew install kute-dev/tap/kute"
	curlCommand     = "curl -fsSL https://kute.dev/install.sh | sh"
)

// DetectInstall classifies execPath (typically os.Executable(), already
// resolved through filepath.EvalSymlinks so a Homebrew-managed symlink into
// /Cellar/ or /opt/homebrew/ is recognized even when the resolved path
// itself is what's inspected here, not a symlink) into one of kute's two
// real install channels. Anything that isn't clearly Homebrew is attributed
// to the install script — the only other channel README.md documents, and
// re-running it is also the correct upgrade path (there's no separate
// "plain binary, no command" case to fall back to).
func DetectInstall(execPath string) InstallInfo {
	if isHomebrewPath(execPath) {
		return InstallInfo{Manager: "homebrew", Command: homebrewCommand}
	}
	return InstallInfo{Manager: "curl", Command: curlCommand}
}

func isHomebrewPath(execPath string) bool {
	p := filepath.ToSlash(execPath)
	return strings.Contains(p, "/Cellar/") ||
		strings.Contains(p, "/homebrew/") ||
		strings.Contains(p, "linuxbrew")
}
