package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadSnapshotFromPidsAndConfigs(t *testing.T) {
	dir := t.TempDir()
	mustMkdir(t, filepath.Join(dir, "Pids"))
	mustMkdir(t, filepath.Join(dir, "Containers", "Ubuntu24.04"))
	mustWrite(t, filepath.Join(dir, "Pids", "Ubuntu24.04.pid"), "23035\n")
	mustWrite(t, filepath.Join(dir, "Containers", "Ubuntu24.04", "container.config"), `name=Ubuntu24.04
uuid=abc123
hostname=Ubuntu24.04
rootfs_path=/data/local/Droidspaces/Containers/Ubuntu24.04/rootfs
net_mode=host
port_forwards=2222:22/tcp
`)

	snap, err := ReadSnapshot(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Info.ContainersRunning != 1 || len(snap.Containers) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snap)
	}
	container := snap.Containers[0]
	if !container.Running || container.PID != 23035 || container.Name != "Ubuntu24.04" {
		t.Fatalf("unexpected container: %#v", container)
	}
	if len(container.Ports) != 1 || container.Ports[0].HostPort != 2222 || container.Ports[0].ContainerPort != 22 {
		t.Fatalf("unexpected ports: %#v", container.Ports)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
