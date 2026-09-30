package nodedetail

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kute-dev/kute/internal/kube"
	"github.com/kute-dev/kute/internal/testutil/goldentest"
)

// fakeDisk is a NodeDiskReader stub; the golden model feeds diskLoadedMsg
// directly, so its answer only matters to tests that run the returned Cmd.
type fakeDisk struct {
	disk kube.NodeDisk
	err  error
}

func (f fakeDisk) NodeDiskUsage(context.Context, string) (kube.NodeDisk, error) {
	return f.disk, f.err
}

func TestDiskLineStates(t *testing.T) {
	forbidden := fmt.Errorf("%w: nope", kube.ErrNodeStatsForbidden)
	ok := diskLoadedMsg{disk: kube.NodeDisk{UsedBytes: 20 * giByte, CapacityBytes: 100 * giByte}}
	tests := []struct {
		name string
		msgs []diskLoadedMsg
		want string
	}{
		{name: "pending", want: "disk — reading kubelet stats…"},
		{name: "reading", msgs: []diskLoadedMsg{ok}, want: "disk"},
		{name: "forbidden", msgs: []diskLoadedMsg{{err: forbidden}}, want: "disk — no access (needs get nodes/proxy)"},
		{name: "unavailable", msgs: []diskLoadedMsg{{err: errors.New("dial")}}, want: "disk — kubelet stats unavailable"},
		{name: "transient error keeps last reading", msgs: []diskLoadedMsg{ok, {err: errors.New("dial")}}, want: "disk"},
		{name: "forbidden after reading", msgs: []diskLoadedMsg{ok, {err: forbidden}}, want: "disk — no access"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeDisk: fakeDisk{}, NodeName: "n"})
			for _, msg := range tt.msgs {
				m.applyDisk(msg)
			}
			line, shown := m.diskLine(m.Theme())
			if !shown {
				t.Fatal("disk row not shown with a disk seam wired")
			}
			got := goldentest.Plain(line)
			if !strings.HasPrefix(got, tt.want) {
				t.Fatalf("disk line = %q, want prefix %q", got, tt.want)
			}
			if tt.want == "disk" && !strings.Contains(got, "/ 100") {
				t.Fatalf("disk line = %q, want used / capacity figures", got)
			}
		})
	}
}

func TestDiskLineOmittedWithoutSeam(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeName: "n"})
	if _, shown := m.diskLine(m.Theme()); shown {
		t.Fatal("disk row shown with no disk seam wired")
	}
}

// The disk read rides the usage-poll chain at 1/diskPollEvery of its rate,
// and stops for good once the answer is Forbidden.
func TestDiskPollCadence(t *testing.T) {
	m := New(Config{Session: newSession(), Lister: fakeLister{}, NodeDisk: fakeDisk{}, NodeMetrics: fakeNodeMetricsStub{}, NodeName: "n"})
	reads := 0
	for range diskPollEvery * 2 {
		m.diskTicks++
		if m.diskDue() {
			m.diskTicks = 0
			reads++
		}
	}
	if reads != 2 {
		t.Fatalf("disk reads over %d ticks = %d, want 2", diskPollEvery*2, reads)
	}
	m.applyDisk(diskLoadedMsg{err: kube.ErrNodeStatsForbidden})
	m.diskTicks = diskPollEvery
	if m.diskDue() {
		t.Fatal("disk read still due after Forbidden")
	}
}

type fakeNodeMetricsStub struct{}

func (fakeNodeMetricsStub) NodeMetrics(context.Context) (map[string]kube.NodeMetric, error) {
	return nil, nil
}
