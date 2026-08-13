package browse

import "testing"

func TestFirstServicePort(t *testing.T) {
	for _, tt := range []struct {
		ports string
		want  string
	}{
		{ports: "80,443", want: "80"},
		{ports: "80", want: "80"},
		{ports: "", want: ""},
	} {
		if got := firstServicePort(tt.ports); got != tt.want {
			t.Errorf("firstServicePort(%q) = %q, want %q", tt.ports, got, tt.want)
		}
	}
}
