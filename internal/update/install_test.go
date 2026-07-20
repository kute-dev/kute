package update

import "testing"

func TestDetectInstall(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		manager string
		command string
	}{
		{"macos homebrew cellar", "/opt/homebrew/Cellar/kute/0.2.0/bin/kute", "homebrew", homebrewCommand},
		{"linux homebrew cellar", "/home/linuxbrew/.linuxbrew/Cellar/kute/0.2.0/bin/kute", "homebrew", homebrewCommand},
		{"homebrew opt symlink target", "/usr/local/homebrew/Cellar/kute/0.2.0/bin/kute", "homebrew", homebrewCommand},
		{"install script default location", "/usr/local/bin/kute", "curl", curlCommand},
		{"install script home-local", "/home/user/.local/bin/kute", "curl", curlCommand},
		{"go run dev binary", "/tmp/go-build12345/b001/kute", "curl", curlCommand},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DetectInstall(tt.path)
			if got.Manager != tt.manager || got.Command != tt.command {
				t.Errorf("DetectInstall(%q) = %+v, want {%q %q}", tt.path, got, tt.manager, tt.command)
			}
		})
	}
}
