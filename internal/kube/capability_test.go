package kube

import "testing"

func TestClassifyTimeZoneCapability(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		major string
		minor string
		want  TimeZoneCapability
	}{
		{"1.27 supported", "1", "27", TimeZoneCapabilitySupported},
		{"1.30 supported", "1", "30", TimeZoneCapabilitySupported},
		{"2.0 supported", "2", "0", TimeZoneCapabilitySupported},
		{"1.24 unsupported", "1", "24", TimeZoneCapabilityUnsupported},
		{"1.20 unsupported", "1", "20", TimeZoneCapabilityUnsupported},
		{"1.25 unknown", "1", "25", TimeZoneCapabilityUnknown},
		{"1.26 unknown", "1", "26", TimeZoneCapabilityUnknown},
		{"gke plus-suffixed minor", "1", "27+", TimeZoneCapabilitySupported},
		{"gke plus-suffixed major", "1+", "27", TimeZoneCapabilitySupported},
		{"unparseable major", "v1", "27", TimeZoneCapabilityUnknown},
		{"unparseable minor", "1", "many", TimeZoneCapabilityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyTimeZoneCapability(tc.major, tc.minor); got != tc.want {
				t.Errorf("classifyTimeZoneCapability(%q, %q) = %v, want %v", tc.major, tc.minor, got, tc.want)
			}
		})
	}
}
