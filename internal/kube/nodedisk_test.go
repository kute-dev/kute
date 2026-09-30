package kube

import "testing"

func TestParseNodeDiskSummary(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    NodeDisk
		wantErr bool
	}{
		{
			name: "node fs present",
			raw:  `{"node":{"nodeName":"n1","fs":{"availableBytes":10,"capacityBytes":100,"usedBytes":42}},"pods":[{"podRef":{"name":"p"}}]}`,
			want: NodeDisk{UsedBytes: 42, CapacityBytes: 100},
		},
		{name: "fs missing", raw: `{"node":{"nodeName":"n1"}}`, wantErr: true},
		{name: "used missing", raw: `{"node":{"fs":{"capacityBytes":100}}}`, wantErr: true},
		{name: "not json", raw: `<html>`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseNodeDiskSummary([]byte(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
