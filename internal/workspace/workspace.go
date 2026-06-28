package workspace

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ravindu644/droidspaces-oss/webui/internal/socketd"
)

type Snapshot struct {
	Containers []socketd.Container `json:"containers"`
	Info       socketd.Info        `json:"info"`
	Source     string              `json:"source"`
}

func ReadSnapshot(workspace string, includeAll bool) (Snapshot, error) {
	if workspace == "" {
		return Snapshot{}, fmt.Errorf("workspace is empty")
	}

	configs := readContainerConfigs(workspace)
	running := readRunningPids(workspace)
	seen := map[string]bool{}
	var containers []socketd.Container

	for name, pid := range running {
		container := configs[name]
		if container.Name == "" {
			container.Name = name
		}
		container.PID = int32(pid)
		container.Running = true
		if container.NetMode == "" {
			container.NetMode = "unknown"
		}
		containers = append(containers, container)
		seen[name] = true
	}

	if includeAll {
		for name, container := range configs {
			if seen[name] {
				continue
			}
			if container.Name == "" {
				container.Name = name
			}
			if container.NetMode == "" {
				container.NetMode = "unknown"
			}
			containers = append(containers, container)
		}
	}

	info := socketd.Info{ContainersTotal: uint32(len(configs)), ContainersRunning: uint32(len(running))}
	if int(info.ContainersTotal) < len(containers) {
		info.ContainersTotal = uint32(len(containers))
	}
	if info.ContainersTotal > info.ContainersRunning {
		info.ContainersStopped = info.ContainersTotal - info.ContainersRunning
	}

	return Snapshot{Containers: containers, Info: info, Source: "workspace"}, nil
}

func Inspect(workspace string, target string) (socketd.Inspect, error) {
	snap, err := ReadSnapshot(workspace, true)
	if err != nil {
		return socketd.Inspect{}, err
	}
	for _, container := range snap.Containers {
		if container.Name == target || container.UUID == target {
			return socketd.Inspect{Container: container, ImageRef: container.RootFSPath}, nil
		}
	}
	return socketd.Inspect{}, fmt.Errorf("container %q not found in workspace", target)
}

func readRunningPids(workspace string) map[string]int {
	out := map[string]int{}
	entries, err := os.ReadDir(filepath.Join(workspace, "Pids"))
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".pid")
		if name == "" || strings.ContainsAny(name, `/\\`) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(workspace, "Pids", entry.Name()))
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			continue
		}
		out[name] = pid
	}
	return out
}

func readContainerConfigs(workspace string) map[string]socketd.Container {
	out := map[string]socketd.Container{}
	base := filepath.Join(workspace, "Containers")
	entries, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, entry := range entries {
		if !entry.IsDir() || strings.ContainsAny(entry.Name(), `/\\`) {
			continue
		}
		name := entry.Name()
		container := parseContainerConfig(filepath.Join(base, name, "container.config"), name)
		out[name] = container
	}
	return out
}

func parseContainerConfig(path string, fallbackName string) socketd.Container {
	container := socketd.Container{Name: fallbackName, NetMode: "unknown"}
	file, err := os.Open(path)
	if err != nil {
		return container
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "name":
			if value != "" {
				container.Name = value
			}
		case "uuid":
			container.UUID = value
		case "hostname":
			container.Hostname = value
		case "rootfs_path":
			container.RootFSPath = value
		case "custom_init":
			container.CustomInit = value
		case "net_mode":
			container.NetMode = value
		case "static_nat_ip":
			container.NATIP = value
		case "port_forwards":
			container.Ports = parsePorts(value)
		}
	}
	return container
}

func parsePorts(value string) []socketd.Port {
	if value == "" {
		return nil
	}
	var ports []socketd.Port
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		proto := "tcp"
		if left, right, ok := strings.Cut(item, "/"); ok {
			item = left
			if right != "" {
				proto = right
			}
		}
		hostSide := item
		containerSide := item
		if left, right, ok := strings.Cut(item, ":"); ok {
			hostSide = left
			containerSide = right
		}
		hostStart, hostEnd, hostOK := parsePortRange(hostSide)
		containerStart, containerEnd, containerOK := parsePortRange(containerSide)
		if !hostOK || !containerOK {
			continue
		}
		ports = append(ports, socketd.Port{HostPort: hostStart, HostPortEnd: hostEnd, ContainerPort: containerStart, ContainerPortEnd: containerEnd, Protocol: proto})
	}
	return ports
}

func parsePortRange(value string) (uint16, uint16, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, 0, false
	}
	if left, right, ok := strings.Cut(value, "-"); ok {
		start, err1 := strconv.Atoi(left)
		end, err2 := strconv.Atoi(right)
		if err1 != nil || err2 != nil || start <= 0 || end < start || end > 65535 {
			return 0, 0, false
		}
		return uint16(start), uint16(end), true
	}
	port, err := strconv.Atoi(value)
	if err != nil || port <= 0 || port > 65535 {
		return 0, 0, false
	}
	return uint16(port), 0, true
}
