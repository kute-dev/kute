package podlogs

import (
	"testing"

	"github.com/kute-dev/kute/internal/kube"
)

func TestFromPodKeepsSelectedInitContainer(t *testing.T) {
	pod := kube.Pod{
		Namespace:          "default",
		Name:               "software-escrow",
		Containers:         []string{"app"},
		ContainerInfos:     []kube.ContainerInfo{{Name: "app"}},
		InitContainerInfos: []kube.ContainerInfo{{Name: "prepare-ssh", State: "Waiting", Reason: "ImagePullBackOff"}},
	}
	m := FromPod(nil, nil, pod, "prepare-ssh", nil)
	if got, ok := m.activeContainer(); !ok || got != "prepare-ssh" {
		t.Fatalf("selectedContainer = %q, %v; want prepare-ssh, true", got, ok)
	}
	if len(m.pod.Containers) != 2 || m.pod.Containers[1] != "prepare-ssh" {
		t.Fatalf("containers = %v, want app and prepare-ssh", m.pod.Containers)
	}
}
