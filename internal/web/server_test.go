package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/rootfs"
	"github.com/ravindu644/droidspaces-oss/webui/internal/socketd"
)

func newTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	templateRoot := filepath.Join(workspace, "rootfs")
	corePath := filepath.Join(base, "bin")
	if err := os.MkdirAll(templateRoot, 0755); err != nil {
		t.Fatal(err)
	}
	installFakeBusybox(t, corePath)
	installFakeDroidspaces(t, corePath)
	t.Setenv("WEBUI_ROOTFS_IMG_MOCK", "1")
	srv, err := NewServer(Options{
		DroidspacesPath:             filepath.Join(corePath, "droidspaces"),
		Workspace:                   workspace,
		CorePath:                    corePath,
		ImageRoot:                   filepath.Join(workspace, "images"),
		TemplateImageRoot:           templateRoot,
		AuthToken:                   "secret",
		DisableBatterySampler:       true,
		DisableRootfsCatalogRefresh: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, workspace, templateRoot
}

func TestNewServerDefaultsPathsFromWorkspace(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "workspace")
	srv, err := NewServer(Options{
		Workspace:                   workspace,
		DisableBatterySampler:       true,
		DisableRootfsCatalogRefresh: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })

	if got, want := srv.droidspacesPath, filepath.Join(workspace, "bin", "droidspaces"); got != want {
		t.Fatalf("droidspacesPath = %q, want %q", got, want)
	}
	if got, want := srv.corePath, filepath.Join(workspace, "bin"); got != want {
		t.Fatalf("corePath = %q, want %q", got, want)
	}
	if got, want := srv.templateImageRoot, filepath.Join(workspace, "rootfs"); got != want {
		t.Fatalf("templateImageRoot = %q, want %q", got, want)
	}
	if srv.imageRoot != srv.templateImageRoot {
		t.Fatalf("imageRoot = %q, want %q", srv.imageRoot, srv.templateImageRoot)
	}
}

func TestSystemSettingsEmptyPathsUseWorkspaceLayout(t *testing.T) {
	srv, _, _ := newTestServer(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	settings, err := srv.normalizeSystemSettings(systemSettingsRequest{
		Mode:      config.ModeLocal,
		Port:      9090,
		Workspace: workspace,
		RootfsRepositories: []config.RootfsRepository{{
			Name: "Test",
			URL:  "https://example.test/rootfs.json",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := settings.DroidspacesPath, filepath.Join(workspace, "bin", "droidspaces"); got != want {
		t.Fatalf("droidspacesPath = %q, want %q", got, want)
	}
	if got, want := settings.CorePath, filepath.Join(workspace, "bin"); got != want {
		t.Fatalf("corePath = %q, want %q", got, want)
	}
	if got, want := settings.TemplateImageRoot, filepath.Join(workspace, "rootfs"); got != want {
		t.Fatalf("templateImageRoot = %q, want %q", got, want)
	}
	if settings.ImageRoot != settings.TemplateImageRoot {
		t.Fatalf("imageRoot = %q, want %q", settings.ImageRoot, settings.TemplateImageRoot)
	}
}

func TestCollectContainerUsagePrefersCoreRSSAndRetainsCgroupMemory(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cgroupRoot := filepath.Join(t.TempDir(), "droidspaces")
	srv.cgroupRoot = cgroupRoot
	cgroupDir := filepath.Join(cgroupRoot, "demo-container")
	mustWriteFile(t, filepath.Join(cgroupDir, "memory.current"), []byte("142876673\n"), 0644)
	mustWriteFile(t, filepath.Join(cgroupDir, "memory.max"), []byte("4294967296\n"), 0644)
	mustWriteFile(t, filepath.Join(cgroupDir, "memory.stat"), []byte("anon 62914560\nfile 67108864\nkernel_stack 1048576\npagetables 2097152\npercpu 524288\nsock 262144\nvmalloc 0\nslab 4194304\n"), 0644)
	t.Setenv("FAKE_USAGE", "UPTIME=9m 22s\nRAM_USED_KB=93200\nRAM_TOTAL_KB=15400164\nCPU_PERMILL=125\n")

	usage, err := srv.collectContainerUsage(context.Background(), "demo container")
	if err != nil {
		t.Fatal(err)
	}
	if usage.RAMUsedKB == nil || *usage.RAMUsedKB != 93200 {
		t.Fatalf("used memory = %#v, want core RSS", usage.RAMUsedKB)
	}
	if usage.RAMTotalKB != nil || usage.RAMPercent != nil {
		t.Fatalf("core RSS must not use the core host total: %#v", usage)
	}
	if usage.MemoryUsageSource != "core-rss" {
		t.Fatalf("memory usage source = %q, want core-rss", usage.MemoryUsageSource)
	}
	if usage.MemoryUsage == nil || usage.MemoryUsage.UsedBytes != 93200*1024 || usage.MemoryUsage.TotalBytes != 0 || usage.MemoryUsage.Percent != nil {
		t.Fatalf("primary memory usage = %#v, want exact core RSS bytes", usage.MemoryUsage)
	}
	if usage.CgroupMemoryUsage == nil || usage.CgroupMemoryUsage.UsedBytes != 142876673 || usage.CgroupMemoryUsage.TotalBytes != 4294967296 {
		t.Fatalf("cgroup memory usage = %#v, want exact cgroup bytes", usage.CgroupMemoryUsage)
	}
	if usage.CgroupMemoryUsage.Percent == nil || *usage.CgroupMemoryUsage.Percent <= 3 || *usage.CgroupMemoryUsage.Percent >= 4 {
		t.Fatalf("cgroup memory percent = %#v, want cgroup percent", usage.CgroupMemoryUsage.Percent)
	}
	if usage.CgroupMemoryUsage.AnonBytes == nil || *usage.CgroupMemoryUsage.AnonBytes != 62914560 ||
		usage.CgroupMemoryUsage.FileBytes == nil || *usage.CgroupMemoryUsage.FileBytes != 67108864 ||
		usage.CgroupMemoryUsage.KernelBytes == nil || *usage.CgroupMemoryUsage.KernelBytes != 8126464 {
		t.Fatalf("cgroup memory breakdown = %#v", usage.CgroupMemoryUsage)
	}
	view := newContainerView(socketd.Container{Name: "demo container", Running: true})
	view.applyUsage(usage)
	if view.MemoryUsage == nil || view.MemoryUsage.UsedBytes != 93200*1024 || view.MemoryUsageSource != "core-rss" {
		t.Fatalf("view primary memory usage = %#v", view)
	}
	if view.CgroupMemoryUsage == nil || view.CgroupMemoryUsage.UsedBytes != 142876673 || view.CgroupMemoryUsage.FileBytes == nil || *view.CgroupMemoryUsage.FileBytes != 67108864 {
		t.Fatalf("view cgroup memory usage = %#v", view.CgroupMemoryUsage)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"memoryUsageSource":"core-rss"`,
		`"cgroupMemoryUsage":`,
		`"anonBytes":62914560`,
		`"fileBytes":67108864`,
		`"kernelBytes":8126464`,
	} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("container usage JSON missing %s: %s", want, encoded)
		}
	}
	if usage.Uptime != "9m 22s" || usage.CPUUsage == nil || *usage.CPUUsage != 12.5 {
		t.Fatalf("non-memory usage fields were not retained: %#v", usage)
	}
}

func TestCollectContainerUsageFallsBackToCgroupMemory(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cgroupRoot := filepath.Join(t.TempDir(), "droidspaces")
	srv.cgroupRoot = cgroupRoot
	cgroupDir := filepath.Join(cgroupRoot, "demo")
	mustWriteFile(t, filepath.Join(cgroupDir, "memory.current"), []byte("104857600\n"), 0644)
	mustWriteFile(t, filepath.Join(cgroupDir, "memory.max"), []byte("1073741824\n"), 0644)
	t.Setenv("FAKE_USAGE", "UPTIME=1m\nCPU_PERMILL=0\n")

	usage, err := srv.collectContainerUsage(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if usage.MemoryUsageSource != "cgroup-memory.current" || usage.MemoryUsage == nil || usage.MemoryUsage.UsedBytes != 104857600 {
		t.Fatalf("cgroup fallback primary memory = %#v", usage)
	}
	if usage.CgroupMemoryUsage == nil || usage.CgroupMemoryUsage.UsedBytes != 104857600 {
		t.Fatalf("cgroup fallback details = %#v", usage.CgroupMemoryUsage)
	}
	if usage.RAMUsedKB == nil || *usage.RAMUsedKB != 104857600/1024 || usage.RAMTotalKB == nil || *usage.RAMTotalKB != 1073741824/1024 {
		t.Fatalf("cgroup fallback legacy fields = %#v", usage)
	}
}

func TestCollectContainerUsageDoesNotUseHostMemoryTotal(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.cgroupRoot = filepath.Join(t.TempDir(), "missing")
	t.Setenv("FAKE_USAGE", "UPTIME=1m\nRAM_USED_KB=93200\nRAM_TOTAL_KB=15400164\nCPU_PERMILL=0\n")

	usage, err := srv.collectContainerUsage(context.Background(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if usage.RAMUsedKB == nil || *usage.RAMUsedKB != 93200 {
		t.Fatalf("process memory = %#v, want 93200", usage.RAMUsedKB)
	}
	if usage.RAMTotalKB != nil || usage.RAMPercent != nil {
		t.Fatalf("host memory must not be exposed as a container total: %#v", usage)
	}
}

func TestKnownZeroResourceUsageIsSerialized(t *testing.T) {
	memoryJSON, err := json.Marshal(containerMemoryUsage{UsedKB: 0, UsedBytes: 0})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(memoryJSON), `"usedKb":0`) || !strings.Contains(string(memoryJSON), `"usedBytes":0`) {
		t.Fatalf("known zero memory usage was omitted: %s", memoryJSON)
	}
	diskJSON, err := json.Marshal(containerDiskUsage{UsedBytes: 0, TotalBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(diskJSON), `"usedBytes":0`) {
		t.Fatalf("known zero disk usage was omitted: %s", diskJSON)
	}
}

func TestContainerViewReportsSparseImageDiskUsage(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "demo")
	imagePath := filepath.Join(containerDir, "rootfs.img")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatal(err)
	}
	image, err := os.OpenFile(imagePath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := image.Truncate(8 << 20); err != nil {
		_ = image.Close()
		t.Fatal(err)
	}
	if _, err := image.WriteAt([]byte("rootfs"), 0); err != nil {
		_ = image.Close()
		t.Fatal(err)
	}
	if err := image.Close(); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=demo\nrootfs_path="+imagePath+"\nuse_sparse_image=1\n"), 0644)

	view := newContainerView(socketd.Container{Name: "demo", RootFSPath: imagePath})
	srv.enrichContainerView(context.Background(), &view)
	if !view.UseSparseImage || view.DiskUsage == nil {
		t.Fatalf("sparse image disk usage missing: %#v", view)
	}
	if view.DiskUsage.TotalBytes != 8<<20 || view.DiskUsage.UsedBytes <= 0 || view.DiskUsage.UsedBytes%512 != 0 {
		t.Fatalf("disk usage = %#v, want allocated 512-byte blocks for an 8 MiB image", view.DiskUsage)
	}
	if view.DiskUsage.Percent == nil || *view.DiskUsage.Percent <= 0 || *view.DiskUsage.Percent > 100 {
		t.Fatalf("disk percentage = %#v", view.DiskUsage.Percent)
	}
}

func installFakeBusybox(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "busybox")
	script := `#!/bin/sh
cmd="$1"
shift
case "$cmd" in
  tar) exec tar "$@" ;;
  cp) exec cp "$@" ;;
  xzcat) exec xzcat "$@" ;;
  *) echo "unsupported busybox applet: $cmd" >&2; exit 127 ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

func installFakeDroidspaces(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "droidspaces")
	script := `#!/bin/sh
printf '%s\n' "$*" >> ./droidspaces-calls.log
if [ "$1" = "--name" ] && [ "$3" = "enter" ]; then
  exec /bin/sh -i
fi
if [ "$1" = "--name" ] && [ "$3" = "pid" ]; then
  if [ -n "$FAKE_PID" ]; then printf '%s\n' "$FAKE_PID"; exit 0; fi
  exit 1
fi
if [ "$1" = "pid" ]; then
  if [ -n "$FAKE_PID" ]; then printf '%s\n' "$FAKE_PID"; exit 0; fi
  exit 1
fi
if [ "$1" = "version" ]; then
  printf '%s\n' "${FAKE_VERSION:-v6.4.5}"
  exit 0
fi
if [ "$1" = "--format" ] && [ "$2" = "show" ]; then
  if [ -n "$FAKE_SHOW" ]; then printf '%s\n' "$FAKE_SHOW"; fi
  exit 0
fi
if [ "$1" = "--name" ] && [ "$3" = "--format" ] && [ "$4" = "info" ]; then
  if [ -n "$FAKE_INFO" ]; then printf '%s\n' "$FAKE_INFO"; fi
  exit 0
fi
if [ "$1" = "--name" ] && [ "$3" = "usage" ]; then
  if [ -n "$FAKE_USAGE" ]; then printf '%s\n' "$FAKE_USAGE"; else printf 'UPTIME=1m 2s\nRAM_USED_KB=1024\nRAM_TOTAL_KB=4096\nCPU_PERMILL=125\n'; fi
  exit 0
fi
if [ "$1" = "--name" ] && [ "$3" = "run" ]; then
  if [ -n "$FAKE_USERS" ] && printf '%s' "$*" | grep -q '/etc/passwd'; then printf '%s
' "$FAKE_USERS"; exit 0; fi
  if [ -n "$FAKE_IP" ]; then printf '%s
' "$FAKE_IP"; else printf '10.0.2.15
'; fi
  exit 0
fi
printf 'fake droidspaces %s\n' "$*"
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func coreUpdateTestResponse(request *http.Request, body []byte) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        make(http.Header),
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func coreUpdateTestArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "droidspaces-release.tar.gz")
	writeTarGz(t, path, files)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func coreUpdateTestRelease(t *testing.T, archive []byte, assetURL string) []byte {
	t.Helper()
	sum := sha256.Sum256(archive)
	release := githubCoreRelease{
		TagName:     "v6.5.0",
		Name:        "Droidspaces v6.5.0",
		HTMLURL:     "https://github.com/ravindu644/Droidspaces-OSS/releases/tag/v6.5.0",
		PublishedAt: "2026-08-09T06:39:05Z",
		Assets: []githubCoreReleaseAsset{{
			Name:               "droidspaces-v6.5.0-test.tar.gz",
			BrowserDownloadURL: assetURL,
			Digest:             "sha256:" + hex.EncodeToString(sum[:]),
			Size:               int64(len(archive)),
			ContentType:        "application/gzip",
		}},
	}
	data, err := json.Marshal(release)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func coreUpdateTestClient(t *testing.T, metadata []byte, assetURL string, archive []byte) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.String() {
		case officialCoreReleaseAPIURL:
			return coreUpdateTestResponse(request, metadata), nil
		case assetURL:
			return coreUpdateTestResponse(request, archive), nil
		default:
			return nil, fmt.Errorf("unexpected core update request %s", request.URL)
		}
	})}
}

func TestReadBestBatteryValueSkipsZeroCandidate(t *testing.T) {
	dir := t.TempDir()
	zero := filepath.Join(dir, "battery_current_now")
	nonzero := filepath.Join(dir, "bms_current_now")
	if err := os.WriteFile(zero, []byte(`0
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nonzero, []byte(`-415000
`), 0644); err != nil {
		t.Fatal(err)
	}
	value, ok, source := readBestBatteryValue([]batteryValueCandidate{
		{Path: zero, Scale: 0.001},
		{Path: nonzero, Scale: 0.001},
	}, true)
	if !ok || source != nonzero || value != -415 {
		t.Fatalf("unexpected battery value value=%v ok=%v source=%s", value, ok, source)
	}
}

func TestReadBestBatteryValueUnitScales(t *testing.T) {
	dir := t.TempDir()
	current := filepath.Join(dir, "current_now")
	voltage := filepath.Join(dir, "voltage_now")
	if err := os.WriteFile(current, []byte(`-500000
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(voltage, []byte(`4100000
`), 0644); err != nil {
		t.Fatal(err)
	}
	currentMA, ok, _ := readBestBatteryValue([]batteryValueCandidate{{Path: current, Scale: 0.001}}, true)
	if !ok || currentMA != -500 {
		t.Fatalf("current scale failed: %v ok=%v", currentMA, ok)
	}
	voltageV, ok, _ := readBestBatteryValue([]batteryValueCandidate{{Path: voltage, Scale: 0.000001}}, true)
	if !ok || voltageV != 4.1 {
		t.Fatalf("voltage scale failed: %v ok=%v", voltageV, ok)
	}
	powerW := currentMA * voltageV / 1000
	if powerW != -2.05 {
		t.Fatalf("power computation failed: %v", powerW)
	}
}

func TestDistroNameFromOSRelease(t *testing.T) {
	if got, want := distroNameFromOSRelease("ID=debian\nNAME=Debian GNU/Linux\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n"), "Debian GNU/Linux 12 (bookworm)"; got != want {
		t.Fatalf("distro name = %q, want %q", got, want)
	}
	if got, want := distroNameFromOSRelease("ID=alpine\n"), "alpine"; got != want {
		t.Fatalf("ID fallback = %q, want %q", got, want)
	}
}

func TestContainerViewReadsDistroFromDirectoryRootfs(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	rootfs := filepath.Join(workspace, "Containers", "demo", "rootfs")
	mustWriteFile(t, filepath.Join(rootfs, "etc", "os-release"), []byte("NAME=Fedora Linux\nPRETTY_NAME=\"Fedora Linux 42\"\n"), 0644)
	view := newContainerView(socketd.Container{Name: "demo", RootFSPath: rootfs})
	srv.enrichContainerView(context.Background(), &view)
	if got, want := view.DistroName, "Fedora Linux 42"; got != want {
		t.Fatalf("distro name = %q, want %q", got, want)
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"distroName":"Fedora Linux 42"`) {
		t.Fatalf("distro field was not serialized: %s", encoded)
	}
}

func TestResetContainerDistroOnStart(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.containerDistroCache["demo"] = containerDistroCacheEntry{DistroName: "Debian"}
	srv.resetContainerDistroOnStart("demo", "stop")
	if _, ok := srv.containerDistroCache["demo"]; !ok {
		t.Fatal("stop must retain the distro cache")
	}
	srv.resetContainerDistroOnStart("demo", "restart")
	if _, ok := srv.containerDistroCache["demo"]; ok {
		t.Fatal("restart must clear the distro cache")
	}
}

func TestReadBatteryReportUsesInputPowerWhenBatteryCurrentIsZero(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	usb := filepath.Join(dir, "usb")
	if err := os.MkdirAll(battery, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(usb, 0755); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(battery, "voltage_now"): "8229000\n",
		filepath.Join(battery, "power_now"):   "0\n",
		filepath.Join(battery, "temp"):        "304\n",
		filepath.Join(usb, "type"):            "USB_DCP\n",
		filepath.Join(usb, "online"):          "1\n",
		filepath.Join(usb, "current_now"):     "222000\n",
		filepath.Join(usb, "voltage_now"):     "9191000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.Available || report.Status != "Charging" || report.CapacityPercent != 80 {
		t.Fatalf("unexpected report header: %+v", report)
	}
	if report.HasCurrent || report.HasPower {
		t.Fatalf("zero battery current/power must not be treated as reported: %+v", report)
	}
	if !report.HasVoltage || report.VoltageV != 8.229 || !report.HasTemperature || report.TemperatureC != 30.4 {
		t.Fatalf("battery voltage/temp not parsed: %+v", report)
	}
	if !report.HasInputCurrent || !report.HasInputVoltage || !report.HasInputPower {
		t.Fatalf("input power not reported: %+v", report)
	}
	if !report.InputOnline {
		t.Fatalf("online external input not reported: %+v", report)
	}
	if math.Abs(report.InputPowerW-2.040402) > 0.000001 {
		t.Fatalf("input power mismatch: %+v", report)
	}
}

func TestReadBatteryReportPrefersPhysicalUSBPDMeasurementOverUCSIContract(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	ucsi := filepath.Join(dir, "ucsi-source-psy")
	usb := filepath.Join(dir, "usb")
	for _, path := range []string{battery, ucsi, usb} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(ucsi, "type"):           "USB\n",
		filepath.Join(ucsi, "online"):         "1\n",
		filepath.Join(ucsi, "current_now"):    "2000000\n",
		filepath.Join(ucsi, "voltage_now"):    "9000000\n",
		filepath.Join(usb, "type"):            "USB_PD\n",
		filepath.Join(usb, "online"):          "1\n",
		filepath.Join(usb, "current_now"):     "125000\n",
		filepath.Join(usb, "voltage_now"):     "9009000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.HasInputPower || !report.HasInputCurrent || !report.HasInputVoltage {
		t.Fatalf("physical USB PD telemetry was not read: %+v", report)
	}
	if report.InputPowerKind != "measured" {
		t.Fatalf("input power kind = %q, want measured: %+v", report.InputPowerKind, report)
	}
	if math.Abs(report.InputPowerW-1.126125) > 0.000001 || report.InputCurrentMA != 125 || report.InputVoltageV != 9.009 {
		t.Fatalf("input power must use physical USB PD measurement, got %+v", report)
	}
	if !strings.Contains(report.InputSource, filepath.Join("usb", "current_now")) {
		t.Fatalf("input source must not use UCSI contract values: %q", report.InputSource)
	}
}

func TestReadBatteryReportDoesNotTreatUCSIContractAsInputEnergy(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	ucsi := filepath.Join(dir, "ucsi-source-psy")
	for _, path := range []string{battery, ucsi} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(ucsi, "type"):           "USB\n",
		filepath.Join(ucsi, "online"):         "1\n",
		filepath.Join(ucsi, "current_now"):    "2000000\n",
		filepath.Join(ucsi, "voltage_now"):    "9000000\n",
		filepath.Join(ucsi, "power_now"):      "18000000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.InputOnline {
		t.Fatalf("UCSI source must still report external power online: %+v", report)
	}
	if report.HasInputCurrent || report.HasInputVoltage || report.HasInputPower || report.InputPowerW != 0 {
		t.Fatalf("UCSI PD contract must not be treated as input energy: %+v", report)
	}

	srv, _, _ := newTestServer(t)
	now := time.Unix(1700000000, 0)
	stats := srv.updateBatteryStats(report, now)
	stats = srv.updateBatteryStats(report, now.Add(10*time.Minute))
	if stats.InputWh != 0 || stats.CurrentInputPowerW != 0 {
		t.Fatalf("UCSI contract must not enter input power statistics: %+v", stats)
	}
}

func TestReadBatteryReportDoesNotUseInputVoltageMaxAsLivePower(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	usb := filepath.Join(dir, "usb")
	for _, path := range []string{battery, usb} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(usb, "type"):            "USB_PD\n",
		filepath.Join(usb, "online"):          "1\n",
		filepath.Join(usb, "current_now"):     "2000000\n",
		filepath.Join(usb, "voltage_max"):     "9000000\n",
		filepath.Join(usb, "input_power"):     "18000000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.InputOnline || !report.HasInputCurrent {
		t.Fatalf("online input current was not retained: %+v", report)
	}
	if report.HasInputVoltage || report.HasInputPower || report.InputPowerW != 0 {
		t.Fatalf("voltage_max or generic input_power must not fabricate live input power: %+v", report)
	}
}

func TestReadBatteryReportPrefersExplicitInputPowerOverCalculatedPower(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	usb := filepath.Join(dir, "usb")
	for _, path := range []string{battery, usb} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(usb, "type"):            "USB_PD\n",
		filepath.Join(usb, "online"):          "1\n",
		filepath.Join(usb, "current_now"):     "2000000\n",
		filepath.Join(usb, "voltage_now"):     "9000000\n",
		filepath.Join(usb, "input_power_now"): "1234000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.HasInputPower || report.InputPowerKind != "measured" || report.InputPowerW != 1.234 {
		t.Fatalf("explicit input power must take precedence: %+v", report)
	}
	if !strings.HasSuffix(report.InputSource, "/input_power_now") {
		t.Fatalf("explicit input power source = %q", report.InputSource)
	}
}

func TestReadBatteryReportUsesOnlineInputForDirectPower(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	usb := filepath.Join(dir, "usb")
	if err := os.MkdirAll(battery, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(usb, 0755); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):        "Battery\n",
		filepath.Join(battery, "status"):      "Charging\n",
		filepath.Join(battery, "capacity"):    "80\n",
		filepath.Join(battery, "current_now"): "0\n",
		filepath.Join(usb, "type"):            "USB_DCP\n",
		filepath.Join(usb, "online"):          "1\n",
		filepath.Join(usb, "current_now"):     "0\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.InputOnline || report.HasInputCurrent || report.HasInputPower {
		t.Fatalf("expected an online input without a measured flow: %+v", report)
	}
	got := normalizeBatteryReport(report, true)
	if got.PowerMode != "direct" || !got.DirectPowerActive {
		t.Fatalf("online idle input should be direct power: %+v", got)
	}
}

func TestNormalizeBatteryReportPowerModes(t *testing.T) {
	tests := []struct {
		name              string
		directPower       bool
		report            batteryReport
		wantMode          string
		wantDirection     string
		wantDirect        bool
		wantSignedCurrent float64
		wantSignedPower   float64
	}{
		{
			name:        "charging status normalizes vendor negative polarity",
			directPower: false,
			report: batteryReport{
				Available: true, Status: "Charging", CurrentMA: -500, VoltageV: 4, PowerW: -2,
				HasCurrent: true, HasVoltage: true, HasPower: true,
			},
			wantMode: "charging", wantDirection: "charging", wantSignedCurrent: 500, wantSignedPower: 2,
		},
		{
			name:        "discharging status normalizes vendor positive polarity",
			directPower: false,
			report: batteryReport{
				Available: true, Status: "Discharging", CurrentMA: 500, VoltageV: 4, PowerW: 2,
				HasCurrent: true, HasVoltage: true, HasPower: true,
			},
			wantMode: "discharging", wantDirection: "discharging", wantSignedCurrent: -500, wantSignedPower: -2,
		},
		{
			name:        "configured direct power has active input and idle battery",
			directPower: true,
			report: batteryReport{
				Available: true, Status: "Charging", InputPowerW: 10,
				HasInputPower: true,
			},
			wantMode: "direct", wantDirection: "idle", wantDirect: true,
		},
		{
			name:        "configured direct power accepts an online input without measurements",
			directPower: true,
			report: batteryReport{
				Available: true, Status: "Charging", InputOnline: true,
			},
			wantMode: "direct", wantDirection: "idle", wantDirect: true,
		},
		{
			name:        "unconfigured direct capable hardware stays external",
			directPower: false,
			report: batteryReport{
				Available: true, Status: "Not charging", InputPowerW: 10,
				HasInputPower: true,
			},
			wantMode: "external", wantDirection: "idle",
		},
		{
			name:        "unknown status falls back to measured battery flow",
			directPower: false,
			report: batteryReport{
				Available: true, Status: "Unknown", CurrentMA: -250, VoltageV: 4,
				HasCurrent: true, HasVoltage: true,
			},
			wantMode: "discharging", wantDirection: "discharging", wantSignedCurrent: -250, wantSignedPower: -1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeBatteryReport(tc.report, tc.directPower)
			if got.PowerMode != tc.wantMode || got.BatteryDirection != tc.wantDirection {
				t.Fatalf("mode=%q direction=%q, want mode=%q direction=%q: %+v", got.PowerMode, got.BatteryDirection, tc.wantMode, tc.wantDirection, got)
			}
			if got.DirectPowerActive != tc.wantDirect {
				t.Fatalf("directPowerActive=%v, want %v: %+v", got.DirectPowerActive, tc.wantDirect, got)
			}
			if tc.wantSignedCurrent == 0 {
				if got.HasSignedCurrent {
					t.Fatalf("unexpected signed current: %+v", got)
				}
			} else if !got.HasSignedCurrent || math.Abs(got.SignedCurrentMA-tc.wantSignedCurrent) > 0.000001 {
				t.Fatalf("signed current=%v reported=%v, want %v: %+v", got.SignedCurrentMA, got.HasSignedCurrent, tc.wantSignedCurrent, got)
			}
			if tc.wantSignedPower == 0 {
				if got.HasSignedPower {
					t.Fatalf("unexpected signed power: %+v", got)
				}
			} else if !got.HasSignedPower || math.Abs(got.SignedPowerW-tc.wantSignedPower) > 0.000001 {
				t.Fatalf("signed power=%v reported=%v, want %v: %+v", got.SignedPowerW, got.HasSignedPower, tc.wantSignedPower, got)
			}
		})
	}
}

func TestBatteryStatsRuntimeOnlyForNormalizedDischarge(t *testing.T) {
	directServer, _, _ := newTestServer(t)
	directServer.batteryDirectPower = true
	direct := batteryReport{
		Available: true, Status: "Charging", CapacityPercent: 80,
		InputPowerW: 10, VoltageV: 4, ChargeMah: 3000, FullChargeMah: 4000,
		HasCapacity: true, HasInputPower: true, HasVoltage: true, HasCharge: true, HasFullCharge: true,
	}
	if got := directServer.normalizeBatteryReport(direct); got.PowerMode != "direct" || !got.DirectPowerActive {
		t.Fatalf("direct report did not normalize as direct: %+v", got)
	}
	if stats := directServer.updateBatteryStats(direct, time.Unix(1700000000, 0)); stats.HasRuntime {
		t.Fatalf("direct power must not produce a runtime estimate: %+v", stats)
	}

	dischargeServer, _, _ := newTestServer(t)
	discharging := batteryReport{
		Available: true, Status: "Discharging", CapacityPercent: 50,
		CurrentMA: 500, VoltageV: 4, PowerW: 2, EnergyWh: 10,
		HasCapacity: true, HasCurrent: true, HasVoltage: true, HasPower: true, HasEnergy: true,
	}
	if got := dischargeServer.normalizeBatteryReport(discharging); got.PowerMode != "discharging" {
		t.Fatalf("discharge report did not normalize as discharging: %+v", got)
	}
	stats := dischargeServer.updateBatteryStats(discharging, time.Unix(1700000000, 0))
	if !stats.HasRuntime || math.Abs(stats.RuntimeHours-5) > 0.000001 {
		t.Fatalf("discharging report should produce a runtime estimate: %+v", stats)
	}

	if _, ok := batterySampleDischargePowerW(batteryStatsSample{PowerMode: "direct", PowerW: -2, HasPower: true}); ok {
		t.Fatal("direct power sample must never be treated as battery discharge")
	}
}

func TestReadBatteryReportReadsChargeEnergyAndHealth(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	if err := os.MkdirAll(battery, 0755); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):               "Battery\n",
		filepath.Join(battery, "status"):             "Discharging\n",
		filepath.Join(battery, "capacity"):           "50\n",
		filepath.Join(battery, "current_now"):        "-1000000\n",
		filepath.Join(battery, "voltage_now"):        "4000000\n",
		filepath.Join(battery, "charge_now"):         "2000000\n",
		filepath.Join(battery, "charge_full"):        "3600000\n",
		filepath.Join(battery, "charge_full_design"): "4000000\n",
		filepath.Join(battery, "energy_now"):         "8000000\n",
		filepath.Join(battery, "energy_full"):        "14400000\n",
		filepath.Join(battery, "energy_full_design"): "16000000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.Available || !report.HasCharge || !report.HasFullCharge || !report.HasDesignCharge || !report.HasEnergy || !report.HasFullEnergy || !report.HasDesignEnergy || !report.HasHealth {
		t.Fatalf("expected charge, energy, and health fields: %+v", report)
	}
	if report.ChargeMah != 2000 || report.FullChargeMah != 3600 || report.DesignChargeMah != 4000 {
		t.Fatalf("charge scaling mismatch: %+v", report)
	}
	if math.Abs(report.EnergyWh-8) > 0.000001 || math.Abs(report.FullEnergyWh-14.4) > 0.000001 || math.Abs(report.DesignEnergyWh-16) > 0.000001 {
		t.Fatalf("energy scaling mismatch: %+v", report)
	}
	if math.Abs(report.HealthPercent-90) > 0.000001 {
		t.Fatalf("health mismatch: %+v", report)
	}
}

func TestBatteryStatsTracksDischargeAndRuntime(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	now := time.Unix(1700000000, 0)
	first := batteryReport{
		Available:       true,
		Status:          "Discharging",
		CapacityPercent: 80,
		CurrentMA:       1000,
		VoltageV:        4,
		PowerW:          4,
		EnergyWh:        32,
		HasCapacity:     true,
		HasCurrent:      true,
		HasVoltage:      true,
		HasPower:        true,
		HasEnergy:       true,
	}
	second := first
	second.CapacityPercent = 70
	second.EnergyWh = 28

	stats := srv.updateBatteryStats(first, now)
	if stats.SampleCount != 1 {
		t.Fatalf("unexpected first stats: %+v", stats)
	}
	stats = srv.updateBatteryStats(second, now.Add(10*time.Minute))
	if stats.SampleCount != 2 {
		t.Fatalf("expected two samples: %+v", stats)
	}
	if math.Abs(stats.DischargeWh-4) > 0.000001 {
		t.Fatalf("discharge Wh mismatch: %+v", stats)
	}
	if !stats.HasEstimatedUsableWh || math.Abs(stats.EstimatedUsableWh-40) > 0.000001 {
		t.Fatalf("usable Wh mismatch: %+v", stats)
	}
	if !stats.HasEstimatedRemainingWh || math.Abs(stats.EstimatedRemainingWh-28) > 0.000001 {
		t.Fatalf("remaining Wh mismatch: %+v", stats)
	}
	if !stats.HasRuntime || math.Abs(stats.RuntimeHours-7) > 0.000001 {
		t.Fatalf("runtime mismatch: %+v", stats)
	}
	data, err := os.ReadFile(filepath.Join(workspace, batteryStatsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 2 {
		t.Fatalf("expected two persisted samples, got %d: %s", got, data)
	}
	rawCheckpoint, err := os.ReadFile(filepath.Join(workspace, batteryStatsDBFileName))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint batteryStatsCheckpoint
	if err := json.Unmarshal(rawCheckpoint, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if checkpoint.SampleCount != 2 || math.Abs(checkpoint.DischargeWh-4) > 0.000001 {
		t.Fatalf("checkpoint mismatch: %+v", checkpoint)
	}
}

func TestBatteryStatsLoadsCheckpointAndReplaysLaterSamples(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	first := batteryStatsSample{
		Time:            1700000000,
		Status:          "Discharging",
		CapacityPercent: 80,
		EnergyWh:        32,
		HasCapacity:     true,
		HasEnergy:       true,
	}
	second := first
	second.Time = 1700000600
	second.CapacityPercent = 70
	second.EnergyWh = 28
	third := second
	third.Time = 1700001200
	third.CapacityPercent = 60
	third.EnergyWh = 24

	state := batteryStatsState{
		path:                  filepath.Join(workspace, batteryStatsFileName),
		checkpointPath:        filepath.Join(workspace, batteryStatsDBFileName),
		loaded:                true,
		sampleCount:           2,
		lastSample:            second,
		hasLastSample:         true,
		dischargeWh:           4,
		trackedRemainingWh:    28,
		hasTrackedRemaining:   true,
		trackedSource:         "energy",
		loadedFromCheckpoint:  true,
		checkpointSampleTime:  second.Time,
		checkpointSampleCount: 2,
	}
	if err := appendBatteryStatsSample(state.path, first); err != nil {
		t.Fatal(err)
	}
	if err := appendBatteryStatsSample(state.path, second); err != nil {
		t.Fatal(err)
	}
	if err := writeBatteryStatsCheckpoint(state.checkpointPath, state); err != nil {
		t.Fatal(err)
	}
	if err := appendBatteryStatsSample(state.path, third); err != nil {
		t.Fatal(err)
	}

	srv.loadBatteryStatsLocked(filepath.Join(workspace, batteryStatsFileName))
	if srv.batteryStats.sampleCount != 3 {
		t.Fatalf("sample count mismatch after checkpoint replay: %+v", srv.batteryStats)
	}
	if math.Abs(srv.batteryStats.dischargeWh-8) > 0.000001 {
		t.Fatalf("discharge mismatch after checkpoint replay: %+v", srv.batteryStats)
	}
	if srv.batteryStats.lastSample.Time != third.Time {
		t.Fatalf("last sample mismatch: %+v", srv.batteryStats.lastSample)
	}
}

func TestBatteryStatsBuffersSamplesUntilWriteInterval(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	atomic.StoreInt64(&srv.batteryStatsSampleSecs, 3)
	atomic.StoreInt64(&srv.batteryStatsWriteMins, 5)
	now := time.Unix(1700000000, 0)
	first := batteryReport{
		Available:       true,
		Status:          "Discharging",
		CapacityPercent: 80,
		CurrentMA:       -1000,
		VoltageV:        4,
		PowerW:          -4,
		HasCapacity:     true,
		HasCurrent:      true,
		HasVoltage:      true,
		HasPower:        true,
	}
	second := first
	second.CapacityPercent = 79
	third := first
	third.CapacityPercent = 70

	stats := srv.updateBatteryStats(first, now)
	if stats.SampleCount != 1 || stats.PendingSampleCount != 1 {
		t.Fatalf("unexpected first buffered stats: %+v", stats)
	}
	stats = srv.updateBatteryStats(second, now.Add(3*time.Second))
	if stats.SampleCount != 2 || stats.PendingSampleCount != 2 {
		t.Fatalf("expected second sample buffered in memory: %+v", stats)
	}
	if _, err := os.Stat(filepath.Join(workspace, batteryStatsFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sample log should not be written before write interval, err=%v", err)
	}
	stats = srv.updateBatteryStats(third, now.Add(5*time.Minute))
	if stats.SampleCount != 3 || stats.PendingSampleCount != 0 {
		t.Fatalf("expected pending samples flushed: %+v", stats)
	}
	data, err := os.ReadFile(filepath.Join(workspace, batteryStatsFileName))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 3 {
		t.Fatalf("expected three samples flushed together, got %d: %s", got, data)
	}
}

func TestBatteryPowerRangeIncludesPendingSamples(t *testing.T) {
	srv, _, _ := newTestServer(t)
	atomic.StoreInt64(&srv.batteryStatsSampleSecs, 3)
	atomic.StoreInt64(&srv.batteryStatsWriteMins, 5)
	now := time.Unix(1700000000, 0)
	first := batteryReport{
		Available:       true,
		Status:          "Discharging",
		CapacityPercent: 80,
		CurrentMA:       -1000,
		VoltageV:        4,
		PowerW:          -4,
		InputPowerW:     9,
		HasCapacity:     true,
		HasCurrent:      true,
		HasVoltage:      true,
		HasPower:        true,
		HasInputPower:   true,
	}
	second := first
	second.PowerW = -8
	second.InputPowerW = 12
	srv.updateBatteryStats(first, now.Add(-10*time.Minute))
	srv.updateBatteryStats(second, now.Add(-9*time.Minute))

	report, err := srv.batteryPowerRangeReport(1, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.SampleCount != 2 || report.DischargeSampleCount != 2 || report.InputSampleCount != 2 {
		t.Fatalf("unexpected sample counts: %+v", report)
	}
	if math.Abs(report.AvgDischargeW-6) > 0.000001 || math.Abs(report.MaxDischargeW-8) > 0.000001 {
		t.Fatalf("unexpected discharge stats: %+v", report)
	}
	if math.Abs(report.AvgInputW-10.5) > 0.000001 || math.Abs(report.MaxInputW-12) > 0.000001 {
		t.Fatalf("unexpected input stats: %+v", report)
	}
	if len(report.RecentSamples) != 2 || !report.RecentSamples[0].HasBattery || !report.RecentSamples[0].HasInput {
		t.Fatalf("recent samples missing power data: %+v", report.RecentSamples)
	}
	if report.RecentSamples[0].PowerMode != "discharging" || report.RecentSamples[0].BatteryDirection != "discharging" {
		t.Fatalf("recent samples missing normalized power state: %+v", report.RecentSamples)
	}
	if len(report.ChartSamples) != 2 || report.ChartSamples[0].Time != report.RecentSamples[0].Time || report.ChartSamples[1].Time != report.RecentSamples[1].Time {
		t.Fatalf("chart samples missing full range data: %+v", report.ChartSamples)
	}
}

func TestBatteryStatsScalesSeriesCellChargeCapacity(t *testing.T) {
	oldCells := configuredBatterySeriesCells
	configuredBatterySeriesCells = 0
	t.Cleanup(func() { configuredBatterySeriesCells = oldCells })
	sample := batteryStatsSample{
		Time:            1700000000,
		Status:          "Discharging",
		CapacityPercent: 71,
		VoltageV:        7.992,
		FullChargeMah:   5800,
		DesignChargeMah: 6650,
		CurrentMA:       -50,
		PowerW:          -0.3996,
		HasCapacity:     true,
		HasVoltage:      true,
		HasFullCharge:   true,
		HasDesignCharge: true,
		HasCurrent:      true,
		HasPower:        true,
	}
	stats := batteryStatsState{sampleCount: 1, lastSample: sample, hasLastSample: true}.report(sample, 3*time.Second, 5*time.Minute)
	expectedUsableWh := 5800.0 / 2 * 7.992 / 1000
	expectedRemainingWh := expectedUsableWh * 0.71
	expectedHealth := 5800.0 / 6650 * 100
	if !stats.HasEstimatedUsableWh || math.Abs(stats.EstimatedUsableWh-expectedUsableWh) > 0.000001 {
		t.Fatalf("usable Wh mismatch: %+v expected=%v", stats, expectedUsableWh)
	}
	if !stats.HasEstimatedRemainingWh || math.Abs(stats.EstimatedRemainingWh-expectedRemainingWh) > 0.000001 {
		t.Fatalf("remaining Wh mismatch: %+v expected=%v", stats, expectedRemainingWh)
	}
	if !stats.HasEstimatedHealthPercent || math.Abs(stats.EstimatedHealthPercent-expectedHealth) > 0.000001 {
		t.Fatalf("health mismatch: %+v expected=%v", stats, expectedHealth)
	}
	if !stats.HasRuntime || math.Abs(stats.RuntimeHours-(expectedRemainingWh/0.3996)) > 0.000001 {
		t.Fatalf("runtime mismatch: %+v", stats)
	}
}

func TestBatterySeriesCellsSettingOverridesAutoDetection(t *testing.T) {
	oldCells := configuredBatterySeriesCells
	t.Cleanup(func() { configuredBatterySeriesCells = oldCells })
	configuredBatterySeriesCells = 1
	if got := batteryPackChargeMAh(5800, 7.992); got != 5800 {
		t.Fatalf("forced 1S should keep reported mAh, got %v", got)
	}
	configuredBatterySeriesCells = 2
	if got := batteryPackChargeMAh(5800, 4.2); got != 2900 {
		t.Fatalf("forced 2S should halve reported mAh, got %v", got)
	}
}

func TestBatteryStatsTracksRemainingByDatabaseWhenCapacityStalls(t *testing.T) {
	oldCells := configuredBatterySeriesCells
	configuredBatterySeriesCells = 0
	t.Cleanup(func() { configuredBatterySeriesCells = oldCells })
	srv, _, _ := newTestServer(t)
	now := time.Unix(1700000000, 0)
	first := batteryReport{
		Available:       true,
		Status:          "Discharging",
		CapacityPercent: 71,
		VoltageV:        7.992,
		FullChargeMah:   5800,
		CurrentMA:       -50,
		PowerW:          -0.3996,
		HasCapacity:     true,
		HasVoltage:      true,
		HasFullCharge:   true,
		HasCurrent:      true,
		HasPower:        true,
	}
	second := first
	second.PowerW = -0.6

	stats := srv.updateBatteryStats(first, now)
	initialRemaining := 5800.0 / 2 * 7.992 / 1000 * 0.71
	if !stats.HasEstimatedRemainingWh || math.Abs(stats.EstimatedRemainingWh-initialRemaining) > 0.000001 || stats.RemainingSource != "capacity" {
		t.Fatalf("unexpected initial remaining: %+v expected=%v", stats, initialRemaining)
	}
	stats = srv.updateBatteryStats(second, now.Add(10*time.Minute))
	expectedRemaining := initialRemaining - ((0.3996+0.6)/2)*(10.0/60.0)
	if !stats.HasEstimatedRemainingWh || math.Abs(stats.EstimatedRemainingWh-expectedRemaining) > 0.000001 {
		t.Fatalf("database remaining mismatch: %+v expected=%v", stats, expectedRemaining)
	}
	if stats.RemainingSource != "database" {
		t.Fatalf("remaining should come from database integration: %+v", stats)
	}
}

func TestBatteryStatsTracksInputPowerWithoutBatteryCurrent(t *testing.T) {
	srv, _, _ := newTestServer(t)
	now := time.Unix(1700000000, 0)
	first := batteryReport{
		Available:       true,
		Status:          "Not charging",
		CapacityPercent: 80,
		InputPowerW:     10,
		HasCapacity:     true,
		HasInputPower:   true,
	}
	second := first

	stats := srv.updateBatteryStats(first, now)
	if stats.SampleCount != 1 {
		t.Fatalf("unexpected first stats: %+v", stats)
	}
	stats = srv.updateBatteryStats(second, now.Add(10*time.Minute))
	if stats.SampleCount != 2 {
		t.Fatalf("expected two samples: %+v", stats)
	}
	if math.Abs(stats.InputWh-(10.0/6.0)) > 0.000001 {
		t.Fatalf("input Wh mismatch: %+v", stats)
	}
	if stats.DischargeWh != 0 || stats.ChargeWh != 0 || stats.HasRuntime {
		t.Fatalf("pure input power must not be counted as battery charge/discharge: %+v", stats)
	}
	if stats.CurrentInputPowerW != 10 {
		t.Fatalf("current input power missing: %+v", stats)
	}
}

func TestBatteryInputPowerOnBatteryNodeDoesNotCountAsBatteryPower(t *testing.T) {
	dir := t.TempDir()
	battery := filepath.Join(dir, "battery")
	if err := os.MkdirAll(battery, 0755); err != nil {
		t.Fatal(err)
	}
	writes := map[string]string{
		filepath.Join(battery, "type"):            "Battery\n",
		filepath.Join(battery, "status"):          "Not charging\n",
		filepath.Join(battery, "capacity"):        "80\n",
		filepath.Join(battery, "current_now"):     "0\n",
		filepath.Join(battery, "voltage_now"):     "4000000\n",
		filepath.Join(battery, "power_now"):       "0\n",
		filepath.Join(battery, "input_power_now"): "10000000\n",
	}
	for path, value := range writes {
		if err := os.WriteFile(path, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
	}
	oldRoot := powerSupplyRoot
	powerSupplyRoot = dir
	t.Cleanup(func() { powerSupplyRoot = oldRoot })

	report := readBatteryReport()
	if !report.Available || !report.HasInputPower || report.InputPowerW != 10 {
		t.Fatalf("battery-node input power not parsed as input: %+v", report)
	}
	if report.HasPower || report.PowerW != 0 {
		t.Fatalf("input_power_now must not be treated as battery power: %+v", report)
	}

	srv, _, _ := newTestServer(t)
	now := time.Unix(1700000000, 0)
	stats := srv.updateBatteryStats(report, now)
	stats = srv.updateBatteryStats(report, now.Add(10*time.Minute))
	if math.Abs(stats.InputWh-(10.0/6.0)) > 0.000001 {
		t.Fatalf("input Wh mismatch: %+v", stats)
	}
	if stats.ChargeWh != 0 || stats.DischargeWh != 0 || stats.HasRuntime {
		t.Fatalf("input power leaked into battery charge/discharge stats: %+v", stats)
	}
}

func TestContainerUsersMatchAndroidAppFiltering(t *testing.T) {
	srv, _, _ := newTestServer(t)
	t.Setenv("FAKE_USERS", strings.Join([]string{
		"root|0|0|/root|/bin/sh",
		"daemon|1|1|/usr/sbin|/usr/sbin/nologin",
		"www|1000|1000|/home/www|/bin/bash",
		"nixbld1|30001|30000|/var/empty|/sbin/nologin",
		"nobody|65534|65534|/nonexistent|/usr/sbin/nologin",
	}, "\n"))
	users, err := srv.containerUsers(context.Background(), "ubuntu24")
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(users))
	for _, user := range users {
		names = append(names, user.Name)
	}
	if strings.Join(names, ",") != "root,www" {
		t.Fatalf("unexpected users: %#v", users)
	}
}

func TestTerminalEnvAddsAndroidRootPath(t *testing.T) {
	srv, _, _ := newTestServer(t)
	t.Setenv("ANDROID_ROOT", "/system")
	t.Setenv("ANDROID_DATA", "")
	t.Setenv("ANDROID_STORAGE", "")
	t.Setenv("EXTERNAL_STORAGE", "")
	t.Setenv("PATH", "/custom/bin")
	env := srv.terminalEnv()
	values := map[string]string{}
	for _, item := range env {
		if key, value, ok := strings.Cut(item, "="); ok {
			values[key] = value
		}
	}
	pathCount := 0
	for _, item := range env {
		if strings.HasPrefix(item, "PATH=") {
			pathCount++
		}
	}
	if pathCount != 1 {
		t.Fatalf("expected exactly one PATH entry, got %d in %#v", pathCount, env)
	}
	pathValue := values["PATH"]
	for _, want := range []string{srv.corePath, "/system/bin", "/system/xbin", "/vendor/bin", "/custom/bin"} {
		if !strings.Contains(pathValue, want) {
			t.Fatalf("PATH missing %s: %s", want, pathValue)
		}
	}
	if values["ANDROID_DATA"] != "/data" || values["ANDROID_STORAGE"] != "/storage" || values["EXTERNAL_STORAGE"] != "/sdcard" {
		t.Fatalf("android env defaults missing: %#v", values)
	}
}

func TestAuthProtectsAPIsAndAcceptsQueryToken(t *testing.T) {
	srv, _, _ := newTestServer(t)
	handler := srv.Handler()

	unauth := httptest.NewRecorder()
	handler.ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if unauth.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauth.Code)
	}

	login := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.Header.Set("Authorization", "Bearer secret")
	handler.ServeHTTP(login, req)
	if login.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", login.Code, login.Body.String())
	}

	auth := httptest.NewRecorder()
	handler.ServeHTTP(auth, httptest.NewRequest(http.MethodGet, "/api/status?token=secret", nil))
	if auth.Code != http.StatusOK {
		t.Fatalf("query token status = %d body=%s", auth.Code, auth.Body.String())
	}
}

func TestWebUILogEndpointReadsBoundedAuthenticatedTail(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	logPath := filepath.Join(workspace, "Logs", "webui.log")
	mustWriteFile(t, logPath, []byte("first\nsecond\r\nthird\nfourth\n"), 0644)
	handler := srv.Handler()

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/logs/webui", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized log status = %d", unauthorized.Code)
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/logs/webui?tail=2&path=/etc/passwd", nil))
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("query path must not bypass authentication, status=%d body=%s", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/logs/webui?tail=2&path=/etc/passwd&token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("webui log status = %d body=%s", res.Code, res.Body.String())
	}
	if got := res.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control=%q, want no-store", got)
	}
	var data webUILogResponse
	if err := json.Unmarshal(res.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if !data.Exists || data.Path != "Logs/webui.log" || data.Tail != 2 || data.ReturnedLines != 2 {
		t.Fatalf("unexpected log metadata: %#v", data)
	}
	if strings.Join(data.Lines, "\n") != "third\nfourth" || !data.Truncated {
		t.Fatalf("unexpected log tail: %#v", data)
	}
	if data.SizeBytes <= 0 || data.ModifiedAt <= 0 {
		t.Fatalf("log file metadata missing: %#v", data)
	}
}

func TestWebUILogEndpointHandlesMissingLogAndLimits(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	handler := srv.Handler()

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/logs/webui?tail=3&token=secret", nil))
	if missing.Code != http.StatusOK {
		t.Fatalf("missing log status = %d body=%s", missing.Code, missing.Body.String())
	}
	var empty webUILogResponse
	if err := json.Unmarshal(missing.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Exists || len(empty.Lines) != 0 || empty.ReturnedLines != 0 || empty.Tail != 3 {
		t.Fatalf("missing log response = %#v", empty)
	}

	for _, tail := range []string{"0", "1001", "abc"} {
		t.Run("tail="+tail, func(t *testing.T) {
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/logs/webui?tail="+tail+"&token=secret", nil))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("tail=%q status=%d body=%s", tail, res.Code, res.Body.String())
			}
		})
	}

	method := httptest.NewRecorder()
	handler.ServeHTTP(method, httptest.NewRequest(http.MethodPost, "/api/logs/webui?token=secret", nil))
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status = %d body=%s", method.Code, method.Body.String())
	}

	large := strings.Repeat("discarded line\n", maxWebUILogReadBytes/len("discarded line\n")+1) + "newest one\nnewest two\n"
	mustWriteFile(t, filepath.Join(workspace, "Logs", "webui.log"), []byte(large), 0644)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/logs/webui?tail=2&token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("large log status=%d body=%s", res.Code, res.Body.String())
	}
	var bounded webUILogResponse
	if err := json.Unmarshal(res.Body.Bytes(), &bounded); err != nil {
		t.Fatal(err)
	}
	if !bounded.Truncated || strings.Join(bounded.Lines, "\n") != "newest one\nnewest two" || bounded.ReturnedLines != 2 {
		t.Fatalf("bounded log response = %#v", bounded)
	}
}

func TestWebUILogEndpointRejectsSymlink(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	logPath := filepath.Join(workspace, "Logs", "webui.log")
	outside := filepath.Join(t.TempDir(), "outside.log")
	mustWriteFile(t, outside, []byte("not a WebUI log\n"), 0644)
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, logPath); err != nil {
		if errors.Is(err, os.ErrPermission) {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/logs/webui?token=secret", nil))
	if res.Code != http.StatusForbidden {
		t.Fatalf("symlink log status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestStaticUIResponsesRevalidate(t *testing.T) {
	srv, _, _ := newTestServer(t)
	for _, path := range []string{"/", "/app.js", "/styles.css", "/assets/distro/ubuntu.svg"} {
		t.Run(path, func(t *testing.T) {
			res := httptest.NewRecorder()
			srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			if res.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if got := res.Header().Get("Cache-Control"); got != "no-cache" {
				t.Fatalf("Cache-Control=%q, want no-cache", got)
			}
		})
	}
}

func TestContainerNetworkDiagnose(t *testing.T) {
	srv, _, _ := newTestServer(t)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers/alp/network-diagnose?token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("network diagnose status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		OK       bool     `json:"ok"`
		ExitCode int      `json:"exitCode"`
		Output   string   `json:"output"`
		Args     []string `json:"args"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK || body.ExitCode != 0 || !strings.Contains(body.Output, "10.0.2.15") {
		t.Fatalf("unexpected diagnose response: %#v", body)
	}
	if len(body.Args) < 5 || body.Args[0] != "--name" || body.Args[2] != "run" {
		t.Fatalf("unexpected diagnose args: %#v", body.Args)
	}
}

func TestRootfsListUsesDiskCacheAndManualRefresh(t *testing.T) {
	var requests atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generation := requests.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":         fmt.Sprintf("Cached template %d", generation),
			"description":  "Official image description",
			"architecture": rootfs.DeviceArch(),
			"download_url": repositoryURL(r, "/rootfs.tar.xz"),
			"size_bytes":   42,
			"build_date":   "2026-08-10",
		}})
	}))
	defer repository.Close()

	srv, _, templateRoot := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "Cache test", URL: repository.URL + "/rootfs.json"}}
	handler := srv.Handler()
	arch := rootfs.DeviceArch()
	load := func(refresh bool) rootfsListTestResponse {
		t.Helper()
		path := "/api/rootfs?token=secret&arch=" + url.QueryEscape(arch)
		if refresh {
			path += "&refresh=1"
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("rootfs list status=%d body=%s", res.Code, res.Body.String())
		}
		var body rootfsListTestResponse
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	first := load(false)
	if requests.Load() != 1 || len(first.Assets) != 1 || first.Assets[0].Name != "Cached template 1" || first.Assets[0].Description != "Official image description" {
		t.Fatalf("first response=%#v requests=%d", first, requests.Load())
	}
	if first.Cache.CachedAt.IsZero() || first.Cache.Stale {
		t.Fatalf("first cache metadata=%#v", first.Cache)
	}
	cachePath := rootfsListCachePath(templateRoot, rootfsListCacheFingerprint(arch, srv.rootfsRepos))
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache was not written at %s: %v", cachePath, err)
	}

	second := load(false)
	if requests.Load() != 1 || len(second.Assets) != 1 || second.Assets[0].Name != "Cached template 1" || second.Assets[0].Description != "Official image description" {
		t.Fatalf("cached response=%#v requests=%d", second, requests.Load())
	}

	refreshed := load(true)
	if requests.Load() != 2 || len(refreshed.Assets) != 1 || refreshed.Assets[0].Name != "Cached template 2" {
		t.Fatalf("refresh response=%#v requests=%d", refreshed, requests.Load())
	}
}

func TestNextRootfsListCacheRefreshUsesLocal0010(t *testing.T) {
	location := time.FixedZone("device", 8*60*60)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{
			name: "before scheduled time",
			now:  time.Date(2026, time.August, 21, 0, 9, 59, 0, location),
			want: time.Date(2026, time.August, 21, 0, 10, 0, 0, location),
		},
		{
			name: "at scheduled time advances one day",
			now:  time.Date(2026, time.August, 21, 0, 10, 0, 0, location),
			want: time.Date(2026, time.August, 22, 0, 10, 0, 0, location),
		},
		{
			name: "after scheduled time advances one day",
			now:  time.Date(2026, time.August, 21, 15, 30, 0, 0, location),
			want: time.Date(2026, time.August, 22, 0, 10, 0, 0, location),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := nextRootfsListCacheRefresh(test.now, location); !got.Equal(test.want) {
				t.Fatalf("next refresh=%s, want %s", got, test.want)
			}
		})
	}
}

func TestScheduledRootfsCatalogRefreshClearsListCacheOnly(t *testing.T) {
	var requests atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		generation := requests.Add(1)
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":         fmt.Sprintf("Scheduled template %d", generation),
			"architecture": rootfs.DeviceArch(),
			"download_url": repositoryURL(r, "/rootfs.tar.xz"),
		}})
	}))
	defer repository.Close()

	srv, workspace, templateRoot := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "Scheduled cache", URL: repository.URL + "/rootfs.json"}}
	arch := rootfs.DeviceArch()
	initial := srv.cachedRootfsList(context.Background(), arch, false)
	if requests.Load() != 1 || len(initial.Assets) != 1 || initial.Assets[0].Name != "Scheduled template 1" {
		t.Fatalf("initial catalog=%#v requests=%d", initial, requests.Load())
	}

	cacheDirectory := rootfsListCacheDirectory(templateRoot)
	mustWriteFile(t, filepath.Join(cacheDirectory, "catalog-obsolete.json"), []byte("obsolete"), 0600)
	mustWriteFile(t, filepath.Join(cacheDirectory, ".catalog-obsolete.tmp"), []byte("temporary"), 0600)
	mustWriteFile(t, filepath.Join(cacheDirectory, "unrelated.json"), []byte("keep"), 0600)
	legacyCacheDirectory := filepath.Join(workspace, rootfsLegacyCacheDirectory)
	mustWriteFile(t, filepath.Join(legacyCacheDirectory, "rootfs-list-obsolete.json"), []byte("legacy"), 0600)

	srv.refreshRootfsCatalogCache(context.Background())
	if requests.Load() != 2 {
		t.Fatalf("scheduled refresh requests=%d, want 2", requests.Load())
	}
	for _, name := range []string{"catalog-obsolete.json", ".catalog-obsolete.tmp"} {
		if _, err := os.Stat(filepath.Join(cacheDirectory, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("obsolete catalog cache %q remains, err=%v", name, err)
		}
	}
	if data := mustReadFile(t, filepath.Join(cacheDirectory, "unrelated.json")); string(data) != "keep" {
		t.Fatalf("unrelated cache file changed: %q", data)
	}
	if _, err := os.Stat(filepath.Join(legacyCacheDirectory, "rootfs-list-obsolete.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy workspace cache remains, err=%v", err)
	}
	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.Path == filepath.Join(cacheDirectory, "unrelated.json") {
			t.Fatalf("catalog metadata was listed as a local rootfs item: %#v", item)
		}
	}

	refreshed := srv.cachedRootfsList(context.Background(), arch, false)
	if requests.Load() != 2 || len(refreshed.Assets) != 1 || refreshed.Assets[0].Name != "Scheduled template 2" {
		t.Fatalf("refreshed catalog=%#v requests=%d", refreshed, requests.Load())
	}
}

func TestScheduledRootfsCatalogRefreshKeepsLastCacheWhenFetchFails(t *testing.T) {
	var fail atomic.Bool
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":         "Last known catalog",
			"architecture": rootfs.DeviceArch(),
			"download_url": repositoryURL(r, "/rootfs.tar.xz"),
		}})
	}))
	defer repository.Close()

	srv, _, templateRoot := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "Flaky scheduled cache", URL: repository.URL + "/rootfs.json"}}
	arch := rootfs.DeviceArch()
	initial := srv.cachedRootfsList(context.Background(), arch, false)
	if len(initial.Assets) != 1 || initial.Assets[0].Name != "Last known catalog" {
		t.Fatalf("initial catalog=%#v", initial)
	}
	cachePath := rootfsListCachePath(templateRoot, rootfsListCacheFingerprint(arch, srv.rootfsRepos))
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("initial cache missing: %v", err)
	}

	fail.Store(true)
	srv.refreshRootfsCatalogCache(context.Background())
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("failed refresh removed the last usable cache: %v", err)
	}
	retained := srv.cachedRootfsList(context.Background(), arch, false)
	if len(retained.Assets) != 1 || retained.Assets[0].Name != "Last known catalog" || retained.Cache.Stale {
		t.Fatalf("retained catalog=%#v", retained)
	}
}

func TestRootfsListDoesNotReuseCacheAfterRepositoryChange(t *testing.T) {
	makeRepository := func(name string, requests *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"name":         name,
				"architecture": rootfs.DeviceArch(),
				"download_url": repositoryURL(r, "/rootfs.tar.xz"),
			}})
		}))
	}
	var firstRequests atomic.Int32
	var secondRequests atomic.Int32
	firstRepository := makeRepository("First repository", &firstRequests)
	defer firstRepository.Close()
	secondRepository := makeRepository("Second repository", &secondRequests)
	defer secondRepository.Close()

	srv, _, _ := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "First", URL: firstRepository.URL + "/rootfs.json"}}
	handler := srv.Handler()
	load := func() rootfsListTestResponse {
		t.Helper()
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/rootfs?token=secret&arch="+url.QueryEscape(rootfs.DeviceArch()), nil))
		if res.Code != http.StatusOK {
			t.Fatalf("rootfs list status=%d body=%s", res.Code, res.Body.String())
		}
		var body rootfsListTestResponse
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	first := load()
	if len(first.Assets) != 1 || first.Assets[0].Name != "First repository" || firstRequests.Load() != 1 {
		t.Fatalf("first response=%#v first requests=%d", first, firstRequests.Load())
	}

	updateBody := strings.NewReader(`{"repositories":[{"name":"Second","url":"` + secondRepository.URL + `/rootfs.json"}]}`)
	update := httptest.NewRecorder()
	handler.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/rootfs/repositories?token=secret", updateBody))
	if update.Code != http.StatusOK {
		t.Fatalf("repository update status=%d body=%s", update.Code, update.Body.String())
	}

	second := load()
	if len(second.Assets) != 1 || second.Assets[0].Name != "Second repository" || secondRequests.Load() != 1 {
		t.Fatalf("repository-change response=%#v second requests=%d", second, secondRequests.Load())
	}
}

func TestRootfsListReturnsStaleCacheWhenRefreshFails(t *testing.T) {
	var requests atomic.Int32
	var failRequests atomic.Bool
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if failRequests.Load() {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":         "Available template",
			"architecture": rootfs.DeviceArch(),
			"download_url": repositoryURL(r, "/rootfs.tar.xz"),
		}})
	}))
	defer repository.Close()

	srv, _, _ := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "Flaky", URL: repository.URL + "/rootfs.json"}}
	handler := srv.Handler()
	load := func(refresh bool) rootfsListTestResponse {
		t.Helper()
		path := "/api/rootfs?token=secret&arch=" + url.QueryEscape(rootfs.DeviceArch())
		if refresh {
			path += "&refresh=1"
		}
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("rootfs list status=%d body=%s", res.Code, res.Body.String())
		}
		var body rootfsListTestResponse
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	if first := load(false); len(first.Assets) != 1 || first.Cache.Stale {
		t.Fatalf("initial response=%#v", first)
	}
	failRequests.Store(true)
	stale := load(true)
	if requests.Load() != 2 || !stale.Cache.Stale || len(stale.Assets) != 1 || stale.Assets[0].Name != "Available template" {
		t.Fatalf("stale response=%#v requests=%d", stale, requests.Load())
	}
	if !strings.Contains(strings.Join(stale.Errors, "\n"), "stale cached rootfs list") {
		t.Fatalf("stale errors=%#v", stale.Errors)
	}
}

func TestRootfsListConcurrentRequestsShareOneRefresh(t *testing.T) {
	var requests atomic.Int32
	repository := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(25 * time.Millisecond)
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"name":         "Concurrent template",
			"architecture": rootfs.DeviceArch(),
			"download_url": repositoryURL(r, "/rootfs.tar.xz"),
		}})
	}))
	defer repository.Close()

	srv, _, _ := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "Concurrent", URL: repository.URL + "/rootfs.json"}}
	handler := srv.Handler()
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/rootfs?token=secret&arch="+url.QueryEscape(rootfs.DeviceArch()), nil))
			if res.Code != http.StatusOK {
				t.Errorf("rootfs list status=%d body=%s", res.Code, res.Body.String())
			}
		}()
	}
	close(start)
	wait.Wait()
	if requests.Load() != 1 {
		t.Fatalf("upstream requests=%d, want one", requests.Load())
	}
}

type rootfsListTestResponse struct {
	Assets []rootfs.Asset          `json:"assets"`
	Errors []string                `json:"errors"`
	Cache  rootfsListCacheMetadata `json:"cache"`
}

func repositoryURL(r *http.Request, suffix string) string {
	return "http://" + r.Host + suffix
}

func TestLocalRootfsListAndDownload(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	mustWriteFile(t, filepath.Join(templateRoot, "rootfs.img"), []byte("image-data"), 0644)
	mustWriteFile(t, filepath.Join(templateRoot, "ubuntu.tar.gz"), []byte("archive-data"), 0644)
	if err := os.MkdirAll(filepath.Join(templateRoot, "debian"), 0755); err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(templateRoot, "exports", "backup.tar.gz"), []byte("backup-data"), 0644)

	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, item := range items {
		kinds[item.Name] = item.Kind
	}
	if kinds["debian"] != "directory" || kinds["rootfs.img"] != "image" || kinds["ubuntu.tar.gz"] != "archive" || kinds["backup.tar.gz"] != "backup" {
		t.Fatalf("unexpected local rootfs items: %#v", kinds)
	}

	handler := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/rootfs/local/download?token=secret&path="+url.QueryEscape(filepath.Join(templateRoot, "rootfs.img")), nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "image-data" {
		t.Fatalf("download status=%d body=%q", res.Code, res.Body.String())
	}

	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/api/rootfs/local/download?token=secret&path="+url.QueryEscape(filepath.Join(t.TempDir(), "outside.img")), nil))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("outside path status=%d", blocked.Code)
	}

	containerImage := filepath.Join(workspace, "Containers", "demo", "rootfs.img")
	mustWriteFile(t, containerImage, []byte("container-rootfs"), 0644)
	containerRes := httptest.NewRecorder()
	handler.ServeHTTP(containerRes, httptest.NewRequest(http.MethodGet, "/api/rootfs/local/download?token=secret&path="+url.QueryEscape(containerImage), nil))
	if containerRes.Code != http.StatusForbidden {
		t.Fatalf("container rootfs download status=%d body=%s", containerRes.Code, containerRes.Body.String())
	}

	outsideTarget := filepath.Join(t.TempDir(), "outside-target.img")
	mustWriteFile(t, outsideTarget, []byte("outside-target"), 0644)
	linkPath := filepath.Join(templateRoot, "linked.img")
	if err := os.Symlink(outsideTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	linkRes := httptest.NewRecorder()
	handler.ServeHTTP(linkRes, httptest.NewRequest(http.MethodGet, "/api/rootfs/local/download?token=secret&path="+url.QueryEscape(linkPath), nil))
	if linkRes.Code != http.StatusForbidden {
		t.Fatalf("symlink rootfs download status=%d body=%s", linkRes.Code, linkRes.Body.String())
	}
}

func TestLocalRootfsItemsSeparateStorageSources(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	customRepository := config.RootfsRepository{Name: "My Mirror", URL: "https://mirror.example.test/catalog/rootfs.json"}
	srv.rootfsRepos = []config.RootfsRepository{
		{Name: "Droidspaces Official", URL: config.OfficialRootfsRepositoryURL},
		{Name: config.LinuxContainersRepositoryName, URL: config.LinuxContainersRepositoryURL},
		customRepository,
	}
	customSource := rootfsTemplateStorageSourceForRepository(customRepository)

	paths := map[string]string{
		filepath.Join(templateRoot, "legacy.tar.gz"):                                                "旧模板目录",
		filepath.Join(templateRoot, "Debian-trixie-Linux-Containers-aarch64-20260810_05-24.tar.xz"): "lxc-image（旧目录）",
		filepath.Join(templateRoot, "Alpine-Droidspaces-developers-aarch64-20260802.tar.xz"):        "Droidspaces Official（旧目录）",
		filepath.Join(templateRoot, rootfsDroidspacesOfficialDirectory, "official.tar.xz"):          "Droidspaces Official",
		filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "linux-containers.tar.xz"):      config.LinuxContainersRepositoryName,
		filepath.Join(templateRoot, rootfsUploadsDirectory, "upload.tar.gz"):                        "本地上传",
		filepath.Join(templateRoot, customSource.directory, "mirror.tar.gz"):                        "My Mirror",
		filepath.Join(templateRoot, rootfsExportsDirectory, "backup.tar.gz"):                        "备份导出",
		filepath.Join(srv.imageRoot, "core-image.tar.gz"):                                           "Core 镜像目录",
	}
	for path := range paths {
		mustWriteFile(t, path, []byte(filepath.Base(path)), 0644)
	}

	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	itemsByPath := make(map[string]localRootfsItem, len(items))
	for _, item := range items {
		itemsByPath[filepath.Clean(item.Path)] = item
	}
	for path, wantSource := range paths {
		item, ok := itemsByPath[filepath.Clean(path)]
		if !ok {
			t.Fatalf("missing template %q from %#v", path, items)
		}
		if item.Source != wantSource {
			t.Fatalf("template %q source=%q, want %q", path, item.Source, wantSource)
		}
	}
	for _, sourceDirectory := range []string{rootfsDroidspacesOfficialDirectory, rootfsLinuxContainersDirectory, rootfsUploadsDirectory, customSource.directory} {
		if _, ok := itemsByPath[filepath.Join(templateRoot, sourceDirectory)]; ok {
			t.Fatalf("storage directory %q must not be listed as a template", sourceDirectory)
		}
	}
	listRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/rootfs/local?token=secret", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("local rootfs list status=%d body=%s", listRes.Code, listRes.Body.String())
	}
	var listBody struct {
		Items []localRootfsItem `json:"items"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &listBody); err != nil {
		t.Fatal(err)
	}
	apiItemsByPath := make(map[string]localRootfsItem, len(listBody.Items))
	for _, item := range listBody.Items {
		apiItemsByPath[filepath.Clean(item.Path)] = item
	}
	if item := apiItemsByPath[filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "linux-containers.tar.xz")]; item.Source != config.LinuxContainersRepositoryName {
		t.Fatalf("local rootfs API source label=%q, want %q", item.Source, config.LinuxContainersRepositoryName)
	}

	uploadPath := filepath.Join(templateRoot, rootfsUploadsDirectory, "upload.tar.gz")
	if !srv.localRootfsFileAllowed(uploadPath) {
		t.Fatalf("uploaded template %q should remain allowed", uploadPath)
	}
	deleteRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRes, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(uploadPath), nil))
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("uploaded template delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
}

func TestRootfsTemplateStorageSourceDirectories(t *testing.T) {
	official := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{
		Name: "official mirror name is ignored",
		URL:  config.OfficialRootfsRepositoryURL,
	})
	if official.directory != rootfsDroidspacesOfficialDirectory || official.label != "Droidspaces Official" {
		t.Fatalf("official storage source=%#v", official)
	}

	linuxContainers := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{
		Name: "Linux Containers mirror",
		URL:  config.LinuxContainersRepositoryURL + "streams/v1/images.json",
	})
	if linuxContainers.directory != rootfsLinuxContainersDirectory || linuxContainers.label != config.LinuxContainersRepositoryName {
		t.Fatalf("lxc-image storage source=%#v", linuxContainers)
	}

	njuMirror := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{
		Name: config.LinuxContainersNJURepositoryName,
		URL:  config.LinuxContainersNJURepositoryURL,
	})
	if njuMirror.directory != rootfsLinuxContainersDirectory || njuMirror.label != config.LinuxContainersRepositoryName {
		t.Fatalf("lxc-image NJU storage source=%#v", njuMirror)
	}
	if !isLinuxContainersRootfsAsset(rootfs.Asset{DownloadURL: config.LinuxContainersNJURepositoryURL + "images/debian/bookworm/arm64/cloud/rootfs.tar.xz"}) {
		t.Fatal("NJU Linux Containers image URL should retain Linux Containers classification")
	}

	legacyNamed := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{Name: "Linux Containers"})
	if legacyNamed.directory != rootfsLinuxContainersDirectory || legacyNamed.label != config.LinuxContainersRepositoryName {
		t.Fatalf("legacy Linux Containers storage source=%#v", legacyNamed)
	}

	first := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{Name: "A source", URL: "https://mirror.example.test/rootfs.json"})
	second := rootfsTemplateStorageSourceForRepository(config.RootfsRepository{Name: "Renamed source", URL: "https://mirror.example.test/rootfs.json"})
	if first.directory != second.directory || !strings.HasPrefix(first.directory, rootfsRepositoryDirectoryPrefix) {
		t.Fatalf("custom repository directory must be deterministic and namespaced: first=%#v second=%#v", first, second)
	}
	if strings.ContainsAny(first.directory, "/\\ ") {
		t.Fatalf("custom repository directory must be safe: %q", first.directory)
	}
}

func TestLinuxContainersStorageMigratesVerifiedLegacyArchive(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	asset := rootfs.Asset{
		Name:           "Debian 14 (forky)",
		Architecture:   rootfs.DeviceArch(),
		Variant:        "cloud",
		Author:         "Linux Containers",
		SourceRepoName: config.LinuxContainersRepositoryName,
		DownloadURL:    "https://images.linuxcontainers.org/images/debian/forky/arm64/cloud/rootfs.tar.xz",
		SizeBytes:      int64(len("legacy-cloud-rootfs")),
	}
	asset.UniqueFilename = rootfs.UniqueFilename(asset)
	legacyPath := filepath.Join(templateRoot, rootfsLinuxContainersPreviousDirectory, "cloud", asset.UniqueFilename)
	mustWriteFile(t, legacyPath, []byte("legacy-cloud-rootfs"), 0644)

	job, started, err := srv.beginSharedRootfsDownload(asset)
	if err != nil {
		t.Fatal(err)
	}
	if started {
		t.Fatal("verified legacy archive should be reused without a network download")
	}
	<-job.done
	if job.err != nil {
		t.Fatal(job.err)
	}
	wantPath := filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "cloud", asset.UniqueFilename)
	if job.path != wantPath {
		t.Fatalf("reused path = %q, want %q", job.path, wantPath)
	}
	if got := string(mustReadFile(t, wantPath)); got != "legacy-cloud-rootfs" {
		t.Fatalf("migrated archive = %q", got)
	}
	if _, err := os.Stat(legacyPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("legacy archive should be moved, stat err=%v", err)
	}
}

func TestLinuxContainersStorageKeepsLegacyDirectoriesDiscoverable(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	previousPath := filepath.Join(templateRoot, rootfsLinuxContainersPreviousDirectory, "cloud", "previous-cloud.tar.xz")
	legacyPath := filepath.Join(templateRoot, rootfsLinuxContainersLegacyDir, "default", "legacy-default.tar.xz")
	mustWriteFile(t, previousPath, []byte("previous"), 0644)
	mustWriteFile(t, legacyPath, []byte("legacy"), 0644)

	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]localRootfsItem, len(items))
	for _, item := range items {
		byPath[filepath.Clean(item.Path)] = item
	}
	for _, path := range []string{previousPath, legacyPath} {
		item, ok := byPath[filepath.Clean(path)]
		if !ok || item.Source != "lxc-image（旧目录）" {
			t.Fatalf("legacy item %q = %#v, found=%v", path, item, ok)
		}
	}
}

func TestLinuxContainersCloudTemplatesRecognizeNewAndLegacyNamespaces(t *testing.T) {
	for _, directory := range []string{
		rootfsLinuxContainersDirectory,
		rootfsLinuxContainersPreviousDirectory,
		rootfsLinuxContainersLegacyDir,
	} {
		path := filepath.Join("/data/local/Droidspaces/rootfs", directory, "cloud", "debian.tar.xz")
		if !isLinuxContainersCloudTemplate(path) {
			t.Fatalf("cloud template in %q should be recognized", directory)
		}
	}
	if isLinuxContainersCloudTemplate(filepath.Join("/data/local/Droidspaces/rootfs", rootfsLinuxContainersDirectory, "default", "debian.tar.xz")) {
		t.Fatal("default Linux Containers template must not be treated as a cloud template")
	}
}

func TestLinuxContainersTemplatesAreStoredAndListedByVariant(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	cloud := rootfs.Asset{
		Name:           "Debian 13 (trixie)",
		Architecture:   rootfs.DeviceArch(),
		Variant:        "cloud",
		Author:         "Linux Containers",
		SourceRepoName: config.LinuxContainersRepositoryName,
		DownloadURL:    "https://images.linuxcontainers.org/images/debian/trixie/arm64/cloud/rootfs.tar.xz",
	}
	defaultTemplate := cloud
	defaultTemplate.Variant = "default"

	for _, test := range []struct {
		asset rootfs.Asset
		want  string
	}{
		{asset: cloud, want: filepath.Join(rootfsLinuxContainersDirectory, "cloud")},
		{asset: defaultTemplate, want: filepath.Join(rootfsLinuxContainersDirectory, "default")},
	} {
		if got := srv.rootfsTemplateStorageDirectoryForAsset(test.asset); got != test.want {
			t.Fatalf("storage directory for %#v = %q, want %q", test.asset.Variant, got, test.want)
		}
	}

	cloudPath := filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "cloud", "debian-cloud.tar.xz")
	defaultPath := filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "default", "debian-default.tar.xz")
	mustWriteFile(t, cloudPath, []byte("cloud"), 0644)
	mustWriteFile(t, defaultPath, []byte("default"), 0644)

	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	byPath := make(map[string]localRootfsItem, len(items))
	for _, item := range items {
		byPath[filepath.Clean(item.Path)] = item
	}
	if item, ok := byPath[cloudPath]; !ok || item.Source != config.LinuxContainersRepositoryName || item.Variant != "cloud" {
		t.Fatalf("cloud item = %#v, found=%v", item, ok)
	}
	if item, ok := byPath[defaultPath]; !ok || item.Source != config.LinuxContainersRepositoryName || item.Variant != "default" {
		t.Fatalf("default item = %#v, found=%v", item, ok)
	}
	if _, found := byPath[filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "cloud")]; found {
		t.Fatal("cloud storage directory must not be exposed as a rootfs template")
	}
}

func TestCloudInitNoCloudSeedUsesDefaultsAndCustomDocuments(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	rootfsDir := filepath.Join(workspace, "Containers", "cloud-init", "rootfs")
	mustWriteFile(t, filepath.Join(rootfsDir, "var", "lib", "cloud", "instances", "stale", "marker"), []byte("old"), 0644)
	mustWriteFile(t, filepath.Join(rootfsDir, "etc", "cloud", "cloud-init.disabled"), []byte("disabled\n"), 0644)

	req := createContainerRequest{
		Name:              "cloud-init",
		Hostname:          "cloud-host",
		CloudInitUserData: "#cloud-config\npackages:\n  - curl",
		CloudInitNetwork:  "version: 2\nethernets:\n  eth0: { dhcp4: true }",
	}
	if err := srv.applyCloudInitToRootfs(context.Background(), rootfsDir, req); err != nil {
		t.Fatal(err)
	}
	seedDir := filepath.Join(rootfsDir, "var", "lib", "cloud", "seed", "nocloud")
	assertFile(t, filepath.Join(seedDir, "user-data"), "#cloud-config\npackages:\n  - curl\n")
	assertFile(t, filepath.Join(seedDir, "network-config"), "version: 2\nethernets:\n  eth0: { dhcp4: true }\n")
	meta := string(mustReadFile(t, filepath.Join(seedDir, "meta-data")))
	if !strings.Contains(meta, "instance-id: \"droidspaces-cloud-init-") || !strings.Contains(meta, "local-hostname: \"cloud-host\"") {
		t.Fatalf("meta-data = %q", meta)
	}
	configText := string(mustReadFile(t, filepath.Join(rootfsDir, "etc", "cloud", "cloud.cfg.d", "99-droidspaces-nocloud.cfg")))
	if !strings.Contains(configText, "datasource_list: [ NoCloud, None ]") || strings.Contains(configText, "config: disabled") {
		t.Fatalf("cloud config = %q", configText)
	}
	if _, err := os.Stat(filepath.Join(rootfsDir, "var", "lib", "cloud", "instances")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cloud-init instance cache should be cleared: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootfsDir, "etc", "cloud", "cloud-init.disabled")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cloud-init disabled marker should be removed: %v", err)
	}

	defaultRootfs := filepath.Join(workspace, "Containers", "cloud-default", "rootfs")
	if err := os.MkdirAll(defaultRootfs, 0755); err != nil {
		t.Fatal(err)
	}
	if err := srv.applyCloudInitToRootfs(context.Background(), defaultRootfs, createContainerRequest{Name: "cloud-default", Hostname: "default-host"}); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(defaultRootfs, "var", "lib", "cloud", "seed", "nocloud", "user-data"), "#cloud-config\nhostname: \"default-host\"\nmanage_etc_hosts: true\npreserve_hostname: false\n")
	defaultConfig := string(mustReadFile(t, filepath.Join(defaultRootfs, "etc", "cloud", "cloud.cfg.d", "99-droidspaces-nocloud.cfg")))
	if !strings.Contains(defaultConfig, "network:\n  config: disabled\n") {
		t.Fatalf("default cloud config = %q", defaultConfig)
	}
}

func TestLinuxContainersCloudVariantEnablesCloudInitByDefault(t *testing.T) {
	request := createContainerRequest{}
	enableCloudInitForAsset(&request, rootfs.Asset{
		Variant:        "cloud",
		Author:         "Linux Containers",
		DownloadURL:    "https://images.linuxcontainers.org/images/debian/trixie/arm64/cloud/rootfs.tar.xz",
		SourceRepoName: config.LinuxContainersRepositoryName,
	})
	if !cloudInitEnabled(request) {
		t.Fatal("Linux Containers cloud asset should enable cloud-init by default")
	}

	disabled := false
	request = createContainerRequest{CloudInitEnabled: &disabled}
	enableCloudInitForAsset(&request, rootfs.Asset{Variant: "cloud", Author: "Linux Containers"})
	if cloudInitEnabled(request) {
		t.Fatal("explicit cloud-init disable must be preserved")
	}

	local := createContainerRequest{}
	enableCloudInitForLocalTemplate(&local, filepath.Join("/data/local/Droidspaces/rootfs", rootfsLinuxContainersDirectory, "cloud", "debian.tar.xz"))
	if !cloudInitEnabled(local) {
		t.Fatal("stored Linux Containers cloud template should enable cloud-init by default")
	}

	generic := createContainerRequest{}
	enableCloudInitForAsset(&generic, rootfs.Asset{Variant: "cloud", Author: "Another Repository"})
	if !cloudInitEnabled(generic) {
		t.Fatal("any cloud rootfs asset should enable cloud-init by default")
	}
}

func TestCloudNATUsesNoCloudStaticNetworkConfiguration(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	rootfsDir := filepath.Join(workspace, "Containers", "cloud-nat", "rootfs")
	dhcpNetworkPath := filepath.Join(rootfsDir, "etc", "systemd", "network", "10-eth-dhcp.network")
	mustWriteFile(t, dhcpNetworkPath, []byte("[Network]\nDHCP=yes\n"), 0644)

	request := createContainerRequest{
		Name:        "cloud-nat",
		Hostname:    "cloud-nat-host",
		NetMode:     "nat",
		StaticNATIP: "172.28.100.42",
		DNSServers:  "9.9.9.9, invalid, 1.1.1.1, 9.9.9.9",
	}
	generated, err := prepareCloudInitNATNetworkConfig(&request)
	if err != nil {
		t.Fatal(err)
	}
	if !generated || !request.CloudInitNATStatic {
		t.Fatal("cloud NAT request should generate a static NoCloud network configuration")
	}
	wantNetwork := "version: 2\n" +
		"ethernets:\n" +
		"  eth0:\n" +
		"    dhcp4: false\n" +
		"    addresses:\n" +
		"      - \"172.28.100.42/16\"\n" +
		"    routes:\n" +
		"      - to: 0.0.0.0/0\n" +
		"        via: \"172.28.0.1\"\n" +
		"    nameservers:\n" +
		"      addresses:\n" +
		"        - \"9.9.9.9\"\n" +
		"        - \"1.1.1.1\"\n"
	if request.CloudInitNetwork != wantNetwork {
		t.Fatalf("generated cloud network config = %q, want %q", request.CloudInitNetwork, wantNetwork)
	}
	if err := srv.applyCloudInitToRootfs(context.Background(), rootfsDir, request); err != nil {
		t.Fatal(err)
	}
	seedDir := filepath.Join(rootfsDir, "var", "lib", "cloud", "seed", "nocloud")
	assertFile(t, filepath.Join(seedDir, "network-config"), wantNetwork)
	assertFile(t, dhcpNetworkPath, "[Network]\nDHCP=yes\n")
	cloudConfig := string(mustReadFile(t, filepath.Join(rootfsDir, "etc", "cloud", "cloud.cfg.d", "99-droidspaces-nocloud.cfg")))
	if !strings.Contains(cloudConfig, "system_info:\n  network:\n    renderers: [networkd]\n") || strings.Contains(cloudConfig, "config: disabled") {
		t.Fatalf("cloud config = %q", cloudConfig)
	}

	content := srv.containerConfigContent("cloud-nat", "cloud-nat", rootfsDir, "nat", request)
	if !strings.Contains(content, "static_nat_ip=172.28.100.42") || strings.Contains(content, "nat_direct_static=") {
		t.Fatalf("cloud NAT config should only use the official static reservation:\n%s", content)
	}

	fallback := cloudInitDNSServers("")
	if got := strings.Join(fallback, ","); got != "1.1.1.1,8.8.8.8" {
		t.Fatalf("default cloud NAT DNS = %q", got)
	}

	custom := createContainerRequest{NetMode: "nat", StaticNATIP: "172.28.100.43", CloudInitNetwork: "version: 2\nethernets:\n  eth0: { dhcp4: true }\n"}
	generated, err = prepareCloudInitNATNetworkConfig(&custom)
	if err != nil || generated || custom.CloudInitNATStatic || !strings.Contains(custom.CloudInitNetwork, "dhcp4: true") {
		t.Fatalf("custom cloud network config must remain authoritative: generated=%v err=%v config=%q", generated, err, custom.CloudInitNetwork)
	}
}

func TestStaticNATIPRequiresHostOctet(t *testing.T) {
	if err := validateStaticNATIP("172.28.1.0"); err == nil {
		t.Fatal("NAT address ending in .0 must be rejected")
	}
	if _, _, ok := parseStaticNATIPParts("172.28.1.0"); ok {
		t.Fatal("NAT address ending in .0 must not be used by the allocator")
	}

	srv, _, _ := newTestServer(t)
	srv.defaultNATThirdOctet = 99
	ip, err := srv.nextNATIPForDefaultThirdOctetLocked("new-container")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "172.28.99.1" {
		t.Fatalf("first generated NAT address = %q, want 172.28.99.1", ip)
	}
}

func TestContainerCreateInitializesStoredLinuxContainersCloudNATTemplate(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	templatePath := filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "cloud", "debian-cloud.tar.gz")
	writeTarGz(t, templatePath, map[string]string{"etc/os-release": "ID=debian\n"})

	payload, err := json.Marshal(map[string]any{
		"name":              "cloud-local",
		"hostname":          "cloud-local-host",
		"rootfsSource":      "local",
		"rootfsPath":        templatePath,
		"useSparseImage":    false,
		"rootfsStorageMode": "directory",
		"netMode":           "nat",
		"staticNatIp":       "172.28.100.50",
		"dnsServers":        "9.9.9.9",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", bytes.NewReader(payload)))
	if res.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("task response=%s err=%v", res.Body.String(), err)
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	if !strings.Contains(task.Output, "cloud-init NoCloud initialization data is ready") {
		t.Fatalf("cloud-init task output missing:\n%s", task.Output)
	}
	rootfsDir := filepath.Join(workspace, "Containers", "cloud-local", "rootfs")
	assertFile(t, filepath.Join(rootfsDir, "var", "lib", "cloud", "seed", "nocloud", "user-data"), "#cloud-config\nhostname: \"cloud-local-host\"\nmanage_etc_hosts: true\npreserve_hostname: false\n")
	assertFile(t, filepath.Join(rootfsDir, "var", "lib", "cloud", "seed", "nocloud", "network-config"), "version: 2\nethernets:\n  eth0:\n    dhcp4: false\n    addresses:\n      - \"172.28.100.50/16\"\n    routes:\n      - to: 0.0.0.0/0\n        via: \"172.28.0.1\"\n    nameservers:\n      addresses:\n        - \"9.9.9.9\"\n")
	configText := string(mustReadFile(t, filepath.Join(workspace, "Containers", "cloud-local", "container.config")))
	if !strings.Contains(configText, "static_nat_ip=172.28.100.50") || strings.Contains(configText, "nat_direct_static=") {
		t.Fatalf("container config = %q", configText)
	}
}

func TestLocalRootfsDeleteOnlyManagedFiles(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	archive := filepath.Join(templateRoot, "ubuntu.tar.gz")
	image := filepath.Join(templateRoot, "rootfs.img")
	dir := filepath.Join(templateRoot, "debian")
	outside := filepath.Join(t.TempDir(), "outside.tar.gz")
	mustWriteFile(t, archive, []byte("archive-data"), 0644)
	mustWriteFile(t, image, []byte("image-data"), 0644)
	mustWriteFile(t, outside, []byte("outside"), 0644)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	handler := srv.Handler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(archive), nil))
	if res.Code != http.StatusOK {
		t.Fatalf("delete archive status=%d body=%s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("archive still exists or stat failed unexpectedly: %v", err)
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(image), nil))
	if res.Code != http.StatusOK {
		t.Fatalf("delete image status=%d body=%s", res.Code, res.Body.String())
	}

	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(outside), nil))
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("outside delete status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file should remain: %v", err)
	}

	dirRes := httptest.NewRecorder()
	handler.ServeHTTP(dirRes, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(dir), nil))
	if dirRes.Code != http.StatusForbidden {
		t.Fatalf("directory delete status=%d body=%s", dirRes.Code, dirRes.Body.String())
	}

	containerImage := filepath.Join(workspace, "Containers", "demo", "rootfs.img")
	mustWriteFile(t, containerImage, []byte("container-rootfs"), 0644)
	containerRes := httptest.NewRecorder()
	handler.ServeHTTP(containerRes, httptest.NewRequest(http.MethodDelete, "/api/rootfs/local/delete?token=secret&path="+url.QueryEscape(containerImage), nil))
	if containerRes.Code != http.StatusForbidden {
		t.Fatalf("container rootfs delete status=%d body=%s", containerRes.Code, containerRes.Body.String())
	}
	if _, err := os.Stat(containerImage); err != nil {
		t.Fatalf("container rootfs should remain: %v", err)
	}
}

func TestLocalRootfsUploadAndRepositorySettings(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	configPath := filepath.Join(t.TempDir(), "webui.json")
	srv.configPath = configPath
	handler := srv.Handler()

	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "Ubuntu Rootfs.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("archive-data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/rootfs/local/upload?token=secret", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", res.Code, res.Body.String())
	}
	if _, err := os.Stat(filepath.Join(templateRoot, rootfsUploadsDirectory, "Ubuntu-Rootfs.tar.gz")); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	repoBody := strings.NewReader(`{"repositories":[{"name":"Mirror","url":"https://example.com/rootfs.json"}]}`)
	repoRes := httptest.NewRecorder()
	handler.ServeHTTP(repoRes, httptest.NewRequest(http.MethodPut, "/api/rootfs/repositories?token=secret", repoBody))
	if repoRes.Code != http.StatusOK {
		t.Fatalf("repo update status=%d body=%s", repoRes.Code, repoRes.Body.String())
	}
	if len(srv.rootfsRepos) != 1 || srv.rootfsRepos[0].Name != "Mirror" {
		t.Fatalf("repos not updated: %#v", srv.rootfsRepos)
	}
	configText := string(mustReadFile(t, configPath))
	if !strings.Contains(configText, "rootfsRepositories") || !strings.Contains(configText, "https://example.com/rootfs.json") {
		t.Fatalf("config not persisted: %s", configText)
	}
}

func TestLocalRootfsUploadRejectsOversizedFile(t *testing.T) {
	srv, _, _ := newTestServer(t)
	oldLimit := maxRootfsUploadBytes
	maxRootfsUploadBytes = 8
	t.Cleanup(func() { maxRootfsUploadBytes = oldLimit })

	var body strings.Builder
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "big-rootfs.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("0123456789")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/rootfs/local/upload?token=secret", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusRequestEntityTooLarge && res.Code != http.StatusBadRequest {
		t.Fatalf("oversized upload status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestSystemSettingsPersistConfig(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	// This test verifies settings persistence only. Prevent an opt-in setting
	// from spawning a real host policy-route monitor during the test.
	srv.disableNATCompatRuntime = true
	srv.configPath = filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(srv.configPath, []byte(`{"_note":"keep","defaultNatUpstreamIfname":"wlan0","defaultNatUpstreamIfnames":"wlan0","natUpstreamIfname":"wlan0","natUpstreamIfnames":"wlan0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	payload := map[string]any{
		"mode":                        "public",
		"host":                        "0.0.0.0",
		"port":                        9191,
		"authToken":                   "newsecret",
		"droidspacesPath":             srv.droidspacesPath,
		"corePath":                    srv.corePath,
		"imageRoot":                   srv.imageRoot,
		"templateImageRoot":           srv.templateImageRoot,
		"workspace":                   workspace,
		"socketdEnabled":              false,
		"rootfsSkipTLSVerify":         false,
		"defaultNatCIDR":              config.DefaultNATCIDR,
		"defaultNatThirdOctet":        42,
		"nestedAndroidNatCompat":      true,
		"batteryDirectPowerSupported": true,
		"batterySeriesCells":          2,
		"overviewPowerEnabled":        false,
		"batteryMonitoringEnabled":    false,
		"batteryDetailEnabled":        false,
		"batteryStatsSampleSeconds":   5,
		"batteryStatsWriteMinutes":    6,
		"overviewRefreshSeconds":      9,
		"rootfsRepositories": []map[string]string{{
			"name": "Mirror",
			"url":  "https://example.com/rootfs.json",
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/settings?token=secret", strings.NewReader(string(body))))
	if res.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", res.Code, res.Body.String())
	}
	var settingsResp map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &settingsResp); err != nil {
		t.Fatal(err)
	}
	if settingsResp["restartRequired"] != true {
		t.Fatalf("restartRequired not reported: %#v", settingsResp)
	}
	if srv.mode != "local" || srv.host != "127.0.0.1" || srv.port != 9090 {
		t.Fatalf("runtime listener changed before restart: mode=%s host=%s port=%d", srv.mode, srv.host, srv.port)
	}
	if srv.authToken != "newsecret" {
		t.Fatalf("runtime auth not updated: %s", srv.authToken)
	}
	if srv.socketdEnabled || srv.rootfsSkipTLSVerify || len(srv.rootfsRepos) != 1 || srv.rootfsRepos[0].Name != "Mirror" {
		t.Fatalf("runtime advanced settings not updated: socketd=%v tls=%v repos=%#v", srv.socketdEnabled, srv.rootfsSkipTLSVerify, srv.rootfsRepos)
	}
	if !srv.nestedAndroidNATCompatEnabled() || settingsResp["nestedAndroidNatCompat"] != true {
		t.Fatalf("nested Android NAT compatibility setting not applied: runtime=%v response=%#v", srv.nestedAndroidNATCompatEnabled(), settingsResp)
	}
	if !srv.batteryDirectPower {
		t.Fatalf("battery direct power setting not applied")
	}
	if srv.batterySeriesCells != 2 {
		t.Fatalf("battery series cells setting not applied: %d", srv.batterySeriesCells)
	}
	if srv.batteryDetailEnabled {
		t.Fatalf("battery detail setting not applied")
	}
	if srv.overviewPowerEnabledSetting() {
		t.Fatalf("overview power setting not applied")
	}
	if srv.batteryMonitoringEnabledSetting() {
		t.Fatalf("battery monitoring setting not applied")
	}
	if got := srv.batteryStatsSampleSeconds(); got != 5 {
		t.Fatalf("battery stats sample setting not applied: %d", got)
	}
	if got := srv.batteryStatsWriteMinutes(); got != 6 {
		t.Fatalf("battery stats write setting not applied: %d", got)
	}
	if srv.overviewRefreshSecs != 9 {
		t.Fatalf("overview refresh setting not applied: %d", srv.overviewRefreshSecs)
	}
	var persisted map[string]any
	if err := json.Unmarshal(mustReadFile(t, srv.configPath), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["_note"] != "keep" {
		t.Fatalf("existing config keys not preserved: %#v", persisted)
	}
	for _, key := range []string{"defaultNatUpstreamIfname", "defaultNatUpstreamIfnames", "natUpstreamIfname", "natUpstreamIfnames"} {
		if _, ok := persisted[key]; ok {
			t.Fatalf("legacy upstream field %q should be removed: %#v", key, persisted)
		}
	}
	if persisted["mode"] != "public" || persisted["host"] != "0.0.0.0" || int(persisted["port"].(float64)) != 9191 || persisted["authToken"] != "newsecret" {
		t.Fatalf("core settings not persisted: %#v", persisted)
	}
	if persisted["batteryDirectPowerSupported"] != true {
		t.Fatalf("battery direct power setting not persisted: %#v", persisted)
	}
	if int(persisted["batterySeriesCells"].(float64)) != 2 {
		t.Fatalf("battery series cells setting not persisted: %#v", persisted)
	}
	if persisted["batteryDetailEnabled"] != false {
		t.Fatalf("battery detail setting not persisted: %#v", persisted)
	}
	if persisted["overviewPowerEnabled"] != false || persisted["batteryMonitoringEnabled"] != false {
		t.Fatalf("battery feature settings not persisted: %#v", persisted)
	}
	if int(persisted["batteryStatsSampleSeconds"].(float64)) != 5 {
		t.Fatalf("battery stats sample setting not persisted: %#v", persisted)
	}
	if int(persisted["batteryStatsWriteMinutes"].(float64)) != 6 {
		t.Fatalf("battery stats write setting not persisted: %#v", persisted)
	}
	if int(persisted["overviewRefreshSeconds"].(float64)) != 9 {
		t.Fatalf("overview refresh setting not persisted: %#v", persisted)
	}
	if persisted["nestedAndroidNatCompat"] != true {
		t.Fatalf("nested Android NAT compatibility setting not persisted: %#v", persisted)
	}
	repos, ok := persisted["rootfsRepositories"].([]any)
	if !ok || len(repos) != 1 {
		t.Fatalf("repos not persisted: %#v", persisted["rootfsRepositories"])
	}
	first, ok := repos[0].(map[string]any)
	if !ok || first["url"] != "https://example.com/rootfs.json" {
		t.Fatalf("repo data not persisted: %#v", repos[0])
	}
	status := httptest.NewRecorder()
	handler.ServeHTTP(status, httptest.NewRequest(http.MethodGet, "/api/status?token=newsecret", nil))
	if status.Code != http.StatusOK {
		t.Fatalf("status with updated token=%d body=%s", status.Code, status.Body.String())
	}
}

func TestBatteryMonitoringDisabledEndpoints(t *testing.T) {
	monitoringEnabled := false
	srv, err := NewServer(Options{
		DroidspacesPath:          filepath.Join(t.TempDir(), "droidspaces"),
		Workspace:                t.TempDir(),
		AuthToken:                "secret",
		BatteryMonitoringEnabled: &monitoringEnabled,
		DisableBatterySampler:    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })

	handler := srv.Handler()
	hostRes := httptest.NewRecorder()
	handler.ServeHTTP(hostRes, httptest.NewRequest(http.MethodGet, "/api/host?token=secret", nil))
	if hostRes.Code != http.StatusOK {
		t.Fatalf("host status=%d body=%s", hostRes.Code, hostRes.Body.String())
	}
	var host map[string]any
	if err := json.Unmarshal(hostRes.Body.Bytes(), &host); err != nil {
		t.Fatal(err)
	}
	battery, ok := host["battery"].(map[string]any)
	if !ok || battery["enabled"] != false || battery["summary"] != "电池监控已关闭" {
		t.Fatalf("disabled host battery response=%#v", host["battery"])
	}

	powerRes := httptest.NewRecorder()
	handler.ServeHTTP(powerRes, httptest.NewRequest(http.MethodGet, "/api/battery/power?token=secret", nil))
	if powerRes.Code != http.StatusOK {
		t.Fatalf("power status=%d body=%s", powerRes.Code, powerRes.Body.String())
	}
	var power map[string]any
	if err := json.Unmarshal(powerRes.Body.Bytes(), &power); err != nil {
		t.Fatal(err)
	}
	if power["enabled"] != false || power["message"] != "电池监控已关闭" {
		t.Fatalf("disabled power response=%#v", power)
	}
}

func currentSystemSettingsForTest(srv *Server) normalizedSystemSettings {
	return normalizedSystemSettings{
		Mode:                     srv.mode,
		Host:                     srv.host,
		Port:                     srv.port,
		AuthToken:                srv.authToken,
		DroidspacesPath:          srv.droidspacesPath,
		CorePath:                 srv.corePath,
		ImageRoot:                srv.imageRoot,
		TemplateImageRoot:        srv.templateImageRoot,
		Workspace:                srv.workspace,
		SocketdEnabled:           srv.socketdEnabled,
		RootfsSkipTLSVerify:      srv.rootfsSkipTLSVerify,
		DefaultNATCIDR:           srv.defaultNATCIDR,
		DefaultNATThirdOctet:     srv.defaultNATThirdOctet,
		NestedAndroidNATCompat:   srv.nestedAndroidNATCompatEnabled(),
		BatteryDirectPower:       srv.batteryDirectPower,
		BatterySeriesCells:       srv.batterySeriesCells,
		OverviewPowerEnabled:     srv.overviewPowerEnabledSetting(),
		BatteryMonitoringEnabled: srv.batteryMonitoringEnabledSetting(),
		BatteryDetailEnabled:     srv.batteryDetailEnabled,
		BatteryStatsSampleSecs:   srv.batteryStatsSampleSeconds(),
		BatteryStatsWriteMins:    srv.batteryStatsWriteMinutes(),
		OverviewRefreshSecs:      srv.overviewRefreshSecs,
		RootfsRepositories:       append([]config.RootfsRepository(nil), srv.rootfsRepos...),
	}
}

func batterySamplerRunning(srv *Server) bool {
	srv.batterySamplerMu.Lock()
	defer srv.batterySamplerMu.Unlock()
	return srv.batterySamplerCancel != nil
}

func TestOverviewPowerSettingPreservesBufferedBatteryStats(t *testing.T) {
	srv, _, _ := newTestServer(t)
	now := time.Now().Unix()
	srv.batteryStatsMu.Lock()
	srv.batteryStats = batteryStatsState{
		sampleCount:    3,
		pendingSince:   now,
		pendingSamples: []batteryStatsSample{{Time: now, PowerW: 1, HasPower: true}},
	}
	srv.batteryStatsMu.Unlock()

	settings := currentSystemSettingsForTest(srv)
	settings.OverviewPowerEnabled = false
	srv.applySystemSettings(settings, false)

	srv.batteryStatsMu.Lock()
	defer srv.batteryStatsMu.Unlock()
	if srv.batteryStats.sampleCount != 3 || len(srv.batteryStats.pendingSamples) != 1 {
		t.Fatalf("overview display setting reset buffered stats: %+v", srv.batteryStats)
	}
}

func TestBatteryMonitoringSettingStopsAndRestartsSampler(t *testing.T) {
	enabled := true
	srv, err := NewServer(Options{
		DroidspacesPath:          filepath.Join(t.TempDir(), "droidspaces"),
		Workspace:                t.TempDir(),
		AuthToken:                "secret",
		BatteryMonitoringEnabled: &enabled,
		BatteryStatsSampleSecs:   60,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	if !batterySamplerRunning(srv) {
		t.Fatal("enabled battery monitoring did not start sampler")
	}

	settings := currentSystemSettingsForTest(srv)
	settings.BatteryMonitoringEnabled = false
	srv.applySystemSettings(settings, false)
	if srv.batteryMonitoringEnabledSetting() || batterySamplerRunning(srv) {
		t.Fatal("disabled battery monitoring left sampler running")
	}

	settings = currentSystemSettingsForTest(srv)
	settings.BatteryMonitoringEnabled = true
	srv.applySystemSettings(settings, false)
	if !srv.batteryMonitoringEnabledSetting() || !batterySamplerRunning(srv) {
		t.Fatal("re-enabled battery monitoring did not restart sampler")
	}
}

func TestRepositoryUpdatesConsolidateLegacyLinuxContainersPair(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.configPath = filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(srv.configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	legacyRepositories := []map[string]string{
		{"name": "Droidspaces Official", "url": config.OfficialRootfsRepositoryURL},
		{"name": config.LinuxContainersRepositoryName, "url": config.LinuxContainersRepositoryURL},
		{"name": config.LinuxContainersNJURepositoryName, "url": config.LinuxContainersNJURepositoryURL},
	}

	repoBody, err := json.Marshal(map[string]any{"repositories": legacyRepositories})
	if err != nil {
		t.Fatal(err)
	}
	repoResponse := httptest.NewRecorder()
	handler.ServeHTTP(repoResponse, httptest.NewRequest(http.MethodPut, "/api/rootfs/repositories?token=secret", bytes.NewReader(repoBody)))
	if repoResponse.Code != http.StatusOK {
		t.Fatalf("repository update status=%d body=%s", repoResponse.Code, repoResponse.Body.String())
	}
	if len(srv.rootfsRepos) != 2 || srv.rootfsRepos[1] != (config.RootfsRepository{Name: config.LinuxContainersRepositoryName, URL: config.LinuxContainersNJURepositoryURL}) {
		t.Fatalf("repository update did not consolidate: %#v", srv.rootfsRepos)
	}

	payload := map[string]any{
		"mode":                 "local",
		"host":                 "127.0.0.1",
		"port":                 9090,
		"authToken":            "secret",
		"droidspacesPath":      srv.droidspacesPath,
		"corePath":             srv.corePath,
		"imageRoot":            srv.imageRoot,
		"templateImageRoot":    srv.templateImageRoot,
		"workspace":            workspace,
		"defaultNatCIDR":       config.DefaultNATCIDR,
		"defaultNatThirdOctet": config.DefaultNATThirdOctet,
		"rootfsRepositories":   legacyRepositories,
	}
	settingsBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	settingsResponse := httptest.NewRecorder()
	handler.ServeHTTP(settingsResponse, httptest.NewRequest(http.MethodPut, "/api/settings?token=secret", bytes.NewReader(settingsBody)))
	if settingsResponse.Code != http.StatusOK {
		t.Fatalf("settings update status=%d body=%s", settingsResponse.Code, settingsResponse.Body.String())
	}
	if len(srv.rootfsRepos) != 2 || srv.rootfsRepos[1] != (config.RootfsRepository{Name: config.LinuxContainersRepositoryName, URL: config.LinuxContainersNJURepositoryURL}) {
		t.Fatalf("settings update did not consolidate: %#v", srv.rootfsRepos)
	}
}

func TestSystemSettingsLocalModeForcesLoopbackHost(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.configPath = filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(srv.configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	handler := srv.Handler()
	payload := map[string]any{
		"mode":                 "local",
		"host":                 "192.168.1.10",
		"port":                 9090,
		"authToken":            "",
		"droidspacesPath":      srv.droidspacesPath,
		"corePath":             srv.corePath,
		"imageRoot":            srv.imageRoot,
		"templateImageRoot":    srv.templateImageRoot,
		"workspace":            workspace,
		"defaultNatCIDR":       config.DefaultNATCIDR,
		"defaultNatThirdOctet": config.DefaultNATThirdOctet,
		"rootfsRepositories": []map[string]string{{
			"name": "Droidspaces Official",
			"url":  config.OfficialRootfsRepositoryURL,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/settings?token=secret", strings.NewReader(string(body))))
	if res.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", res.Code, res.Body.String())
	}
	if srv.host != "127.0.0.1" {
		t.Fatalf("runtime local host = %q, want 127.0.0.1", srv.host)
	}
	var persisted map[string]any
	if err := json.Unmarshal(mustReadFile(t, srv.configPath), &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted["host"] != "127.0.0.1" {
		t.Fatalf("persisted local host = %#v, want 127.0.0.1", persisted["host"])
	}
}

func TestSystemSettingsPublicModeGeneratesAuthToken(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.configPath = filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(srv.configPath, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"mode":                 "public",
		"host":                 "0.0.0.0",
		"port":                 9090,
		"authToken":            "",
		"droidspacesPath":      srv.droidspacesPath,
		"corePath":             srv.corePath,
		"imageRoot":            srv.imageRoot,
		"templateImageRoot":    srv.templateImageRoot,
		"workspace":            workspace,
		"defaultNatCIDR":       config.DefaultNATCIDR,
		"defaultNatThirdOctet": config.DefaultNATThirdOctet,
		"rootfsRepositories": []map[string]string{{
			"name": "Droidspaces Official",
			"url":  config.OfficialRootfsRepositoryURL,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/settings?token=secret", strings.NewReader(string(body))))
	if res.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", res.Code, res.Body.String())
	}
	var persisted map[string]any
	if err := json.Unmarshal(mustReadFile(t, srv.configPath), &persisted); err != nil {
		t.Fatal(err)
	}
	token, _ := persisted["authToken"].(string)
	if len(token) != 8 {
		t.Fatalf("generated persisted authToken = %q", token)
	}
	if srv.authToken != token {
		t.Fatalf("runtime authToken = %q, want persisted token %q", srv.authToken, token)
	}
}

func TestAndroidNATCreateDoesNotWriteUpstreamConfiguration(t *testing.T) {
	t.Setenv("ANDROID_ROOT", "/system")
	srv, workspace, templateRoot := newTestServer(t)
	srv.configPath = filepath.Join(t.TempDir(), "webui.json")
	archive := filepath.Join(templateRoot, "ubuntu.tar.gz")
	writeTarGz(t, archive, map[string]string{"etc/issue": "ubuntu"})
	handler := srv.Handler()

	settings := httptest.NewRecorder()
	handler.ServeHTTP(settings, httptest.NewRequest(http.MethodPut, "/api/network/settings?token=secret", strings.NewReader(`{"defaultNatCIDR":"172.28.0.0/16","defaultNatThirdOctet":99,"defaultNatUpstreamIfname":"wlan0,r_rmnet_data*"}`)))
	if settings.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", settings.Code, settings.Body.String())
	}
	webConfig := string(mustReadFile(t, srv.configPath))
	if strings.Contains(webConfig, "defaultNatUpstreamIfname") {
		t.Fatalf("legacy NAT upstream setting should not be persisted: %s", webConfig)
	}
	mustWriteFile(t, filepath.Join(workspace, "Containers", "existing", "container.config"), []byte("name=existing\nnet_mode=nat\nstatic_nat_ip=172.28.100.1\n"), 0644)

	payload := `{
		"name":"advanced",
		"rootfsPath":"` + archive + `",
		"rootfsSource":"local",
		"useSparseImage":false,
		"netMode":"nat",
		"natUpstreamIfname":"wlan0 r_rmnet_data*",
		"portForwards":"2222:22/tcp",
		"privilegedMode":"nomask,shared",
		"termuxX11":false,
		"tx11ExtraFlags":"--should-not-persist",
		"virgl":true,
		"virglExtraFlags":"--renderer angle",
		"disableIPv6":false,
		"blockNestedNamespaces":true
	}`
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", strings.NewReader(payload)))
	if res.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad task response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	if task.Kind != "container-create" || task.Path == "" {
		t.Fatalf("unexpected create task: %#v", task)
	}
	configText := string(mustReadFile(t, filepath.Join(workspace, "Containers", "advanced", "container.config")))
	for _, want := range []string{"use_sparse_image=0", "net_mode=nat", "disable_ipv6=1", "static_nat_ip=172.28.99.1", "privileged=nomask,shared", "enable_termux_x11=0", "enable_virgl=1", "virgl_extra_flags=--renderer angle", "block_nested_ns=1"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
	for _, legacyKey := range []string{"nat_upstream_ifnames=", "upstream_interfaces="} {
		if strings.Contains(configText, legacyKey) {
			t.Fatalf("Android NAT create pinned an upstream with %q:\n%s", legacyKey, configText)
		}
	}
	if strings.Contains(configText, "tx11_extra_flags=") {
		t.Fatalf("disabled Termux:X11 extra flags should not be persisted:\n%s", configText)
	}
}

func TestNATConfigPatchRemovesLegacyUpstreamInterfaces(t *testing.T) {
	t.Setenv("ANDROID_ROOT", "/system")
	srv, workspace, _ := newTestServer(t)
	configPath := filepath.Join(workspace, "Containers", "android-nat", "container.config")
	mustWriteFile(t, configPath, []byte("name=android-nat\nnet_mode=nat\nnat_upstream_ifnames=wlan0,r_rmnet_data0\nupstream_interfaces=wlan0,r_rmnet_data0\n"), 0644)

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/api/containers/android-nat/config?token=secret", strings.NewReader(`{"natUpstreamIfnames":"wlan0,r_rmnet_data0","natUpstreamIfname":"wlan0","hostname":"android-nat-host"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("config patch status=%d body=%s", res.Code, res.Body.String())
	}
	updated := string(mustReadFile(t, configPath))
	if strings.Contains(updated, "upstream_interfaces=") || strings.Contains(updated, "nat_upstream_ifnames=") {
		t.Fatalf("NAT config patch left a fixed upstream configuration:\n%s", updated)
	}
	if !strings.Contains(updated, "hostname=android-nat-host") {
		t.Fatalf("config patch did not apply ordinary updates:\n%s", updated)
	}
}

func TestNATNetworkSettingsReportsCoreAutoDetection(t *testing.T) {
	t.Setenv("ANDROID_ROOT", "/system")
	srv, workspace, _ := newTestServer(t)
	content := srv.containerConfigContent("android-nat", "android-nat", filepath.Join(workspace, "Containers", "android-nat", "rootfs"), "nat", createContainerRequest{
		NATUpstreamIfnames: "wlan0,r_rmnet_data0",
		NATUpstreamIfname:  "wlan0",
	})
	for _, key := range []string{"nat_upstream_ifnames=", "upstream_interfaces="} {
		if strings.Contains(content, key) {
			t.Fatalf("Android NAT config must let the core auto-detect instead of writing %q:\n%s", key, content)
		}
	}
	settings := httptest.NewRecorder()
	srv.Handler().ServeHTTP(settings, httptest.NewRequest(http.MethodGet, "/api/network/settings?token=secret", nil))
	if settings.Code != http.StatusOK {
		t.Fatalf("network settings status=%d body=%s", settings.Code, settings.Body.String())
	}
	var response struct {
		UpstreamMode              string `json:"upstreamMode"`
		AndroidNATUpstreamPresets bool   `json:"androidNATUpstreamPresets"`
	}
	if err := json.Unmarshal(settings.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.UpstreamMode != "core-auto-detect" || response.AndroidNATUpstreamPresets {
		t.Fatalf("network settings = %#v", response)
	}
}

func TestLocalRootfsListEmptyItemsAreArray(t *testing.T) {
	srv, _, _ := newTestServer(t)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/rootfs/local?token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		Items []localRootfsItem `json:"items"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Items == nil {
		t.Fatalf("items decoded as nil; body=%s", res.Body.String())
	}
	if len(body.Items) != 0 {
		t.Fatalf("items len=%d want 0: %#v", len(body.Items), body.Items)
	}
}

func TestSocketdDisabledUsesConfiguredWorkspace(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.socketdEnabled = false
	containerDir := filepath.Join(workspace, "Containers", "demo")
	rootfsDir := filepath.Join(containerDir, "rootfs")
	mustWriteFile(t, filepath.Join(rootfsDir, "etc", "issue"), []byte("demo"), 0644)
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=demo\nrootfs_path="+rootfsDir+"\nnet_mode=host\n"), 0644)

	handler := srv.Handler()
	statusRes := httptest.NewRecorder()
	handler.ServeHTTP(statusRes, httptest.NewRequest(http.MethodGet, "/api/status?token=secret", nil))
	if statusRes.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", statusRes.Code, statusRes.Body.String())
	}
	var status map[string]any
	if err := json.Unmarshal(statusRes.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["socketdEnabled"] != false || status["backend"] != "socketd-disabled" {
		t.Fatalf("unexpected status: %#v", status)
	}

	listRes := httptest.NewRecorder()
	handler.ServeHTTP(listRes, httptest.NewRequest(http.MethodGet, "/api/containers?all=1&token=secret", nil))
	if listRes.Code != http.StatusOK {
		t.Fatalf("list code=%d body=%s", listRes.Code, listRes.Body.String())
	}
	var list struct {
		Containers []socketd.Container `json:"containers"`
		Source     string              `json:"source"`
	}
	if err := json.Unmarshal(listRes.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Source != "workspace" {
		t.Fatalf("source = %q", list.Source)
	}
	if len(list.Containers) != 1 || list.Containers[0].Name != "demo" {
		t.Fatalf("unexpected containers: %#v", list.Containers)
	}
	if !strings.HasPrefix(list.Containers[0].RootFSPath, workspace) {
		t.Fatalf("container rootfs %q outside workspace %q", list.Containers[0].RootFSPath, workspace)
	}

	srvEnv := "CONT_external=9999\nTOTAL_CONTAINERS=2"
	t.Setenv("FAKE_SHOW", srvEnv)
	t.Setenv("FAKE_INFO", "CONTAINER_NAME=external\nCONTAINER_PID=9999\nROOTFS_PATH=/outside/rootfs")
	statusRes = httptest.NewRecorder()
	handler.ServeHTTP(statusRes, httptest.NewRequest(http.MethodGet, "/api/status?token=secret", nil))
	if statusRes.Code != http.StatusOK {
		t.Fatalf("status with fake cli code=%d body=%s", statusRes.Code, statusRes.Body.String())
	}
	if err := json.Unmarshal(statusRes.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	info, ok := status["info"].(map[string]any)
	if !ok || info["containersTotal"] != float64(1) {
		t.Fatalf("status used external cli info: %#v", status)
	}

	inspectRes := httptest.NewRecorder()
	handler.ServeHTTP(inspectRes, httptest.NewRequest(http.MethodGet, "/api/containers/demo?token=secret", nil))
	if inspectRes.Code != http.StatusOK {
		t.Fatalf("inspect code=%d body=%s", inspectRes.Code, inspectRes.Body.String())
	}
	var inspect inspectResponse
	if err := json.Unmarshal(inspectRes.Body.Bytes(), &inspect); err != nil {
		t.Fatal(err)
	}
	if inspect.Source != "workspace" || inspect.Name != "demo" || !strings.HasPrefix(inspect.RootFSPath, workspace) {
		t.Fatalf("inspect used external cli data: %#v", inspect)
	}
}

func TestStatusIncludesBackendDiagnosticLog(t *testing.T) {
	srv, _, _ := newTestServer(t)
	srv.socketdEnabled = false
	srv.recordBackendDiagnostic("status/socketd-ping", errors.New("dial unix droidspaces-socketd-backend: connect: connection refused"))

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/status?token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status code=%d body=%s", res.Code, res.Body.String())
	}

	var status struct {
		Backend       string                   `json:"backend"`
		BackendErrors []backendDiagnosticEntry `json:"backendErrors"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.BackendErrors) == 0 {
		t.Fatalf("expected backend diagnostic log in status: %+v", status)
	}
	if status.BackendErrors[0].Source != "status/socketd-ping" || status.BackendErrors[0].Message == "" || status.BackendErrors[0].Hint == "" {
		t.Fatalf("unexpected backend diagnostics: %+v", status.BackendErrors)
	}

	diagRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(diagRes, httptest.NewRequest(http.MethodGet, "/api/diagnostics/backend?token=secret", nil))
	if diagRes.Code != http.StatusOK {
		t.Fatalf("diagnostics code=%d body=%s", diagRes.Code, diagRes.Body.String())
	}
	var diagnostics struct {
		Errors []backendDiagnosticEntry `json:"errors"`
	}
	if err := json.Unmarshal(diagRes.Body.Bytes(), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics.Errors) == 0 || diagnostics.Errors[0].Message != status.BackendErrors[0].Message {
		t.Fatalf("diagnostics did not expose backend errors: %+v", diagnostics)
	}
}

func TestStatusIncludesVersionMetadata(t *testing.T) {
	t.Setenv("FAKE_VERSION", "v6.4.5")
	srv, workspace, _ := newTestServer(t)
	srv.webVersion = "web-test"
	srv.supportedCoreVersion = "v6.5.0"

	handler := srv.Handler()
	for range 2 {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/status?token=secret", nil))
		if res.Code != http.StatusOK {
			t.Fatalf("status code=%d body=%s", res.Code, res.Body.String())
		}
		var status struct {
			WebVersion           string `json:"webVersion"`
			CoreVersion          string `json:"coreVersion"`
			SupportedCoreVersion string `json:"supportedCoreVersion"`
			IsAndroid            bool   `json:"isAndroid"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.WebVersion != "web-test" || status.CoreVersion != "v6.4.5" || status.SupportedCoreVersion != "v6.5.0" {
			t.Fatalf("unexpected version metadata: %+v", status)
		}
		if status.IsAndroid != config.IsAndroid() {
			t.Fatalf("isAndroid = %t, want %t", status.IsAndroid, config.IsAndroid())
		}
	}

	calls := readOptionalFile(t, filepath.Join(workspace, "droidspaces-calls.log"))
	if strings.Count(calls, "version\n") != 1 {
		t.Fatalf("core version query was not cached: %q", calls)
	}
}

func TestRuntimeCoreVersionSkipsNonVersionWarnings(t *testing.T) {
	t.Setenv("FAKE_VERSION", "[!] String truncation: src='/very/long/path/droidspaces' (len=76) to size=64\nv6.5.0")
	srv, _, _ := newTestServer(t)

	if got := srv.runtimeCoreVersion(context.Background()); got != "v6.5.0" {
		t.Fatalf("core version = %q, want v6.5.0", got)
	}
}

func TestCoreUpdateMetadataForcesCoreVersionRefresh(t *testing.T) {
	t.Setenv("FAKE_VERSION", "v6.4.5")
	srv, workspace, _ := newTestServer(t)
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	assetURL := "https://github.com/ravindu644/Droidspaces-OSS/releases/download/v6.5.0/droidspaces-v6.5.0-test.tar.gz"
	archive := coreUpdateTestArchive(t, map[string]string{
		"droidspaces-v6.5.0/" + architecture + "/droidspaces": "new-core",
	})
	metadata := coreUpdateTestRelease(t, archive, assetURL)
	srv.coreUpdateHTTPClient = coreUpdateTestClient(t, metadata, assetURL, archive)

	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/core/update?token=secret", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("core update metadata status=%d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		CurrentVersion  string `json:"currentVersion"`
		LatestVersion   string `json:"latestVersion"`
		Architecture    string `json:"architecture"`
		AssetName       string `json:"assetName"`
		Source          string `json:"source"`
		UpdateAvailable *bool  `json:"updateAvailable"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.CurrentVersion != "v6.4.5" || body.LatestVersion != "v6.5.0" || body.Architecture != architecture || body.AssetName != "droidspaces-v6.5.0-test.tar.gz" || body.Source != "GitHub 官方 Release" || body.UpdateAvailable == nil || !*body.UpdateAvailable {
		t.Fatalf("unexpected core update metadata: %#v", body)
	}

	t.Setenv("FAKE_VERSION", "v6.4.6")
	response = httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/status?token=secret&refreshCoreVersion=1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("refreshed status=%d body=%s", response.Code, response.Body.String())
	}
	var status struct {
		CoreVersion string `json:"coreVersion"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.CoreVersion != "v6.4.6" {
		t.Fatalf("refreshed core version = %q, want v6.4.6", status.CoreVersion)
	}
	calls := readOptionalFile(t, filepath.Join(workspace, "droidspaces-calls.log"))
	if strings.Count(calls, "version\n") != 2 {
		t.Fatalf("expected forced version refresh, calls=%q", calls)
	}
}

func TestCoreUpdateTaskReplacesVerifiedArchiveAndPreservesBackup(t *testing.T) {
	srv, _, _ := newTestServer(t)
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	assetURL := "https://github.com/ravindu644/Droidspaces-OSS/releases/download/v6.5.0/droidspaces-v6.5.0-test.tar.gz"
	newBinary := "#!/bin/sh\necho v6.5.0\n"
	archive := coreUpdateTestArchive(t, map[string]string{
		"droidspaces-v6.5.0/" + architecture + "/droidspaces": newBinary,
	})
	metadata := coreUpdateTestRelease(t, archive, assetURL)
	srv.coreUpdateHTTPClient = coreUpdateTestClient(t, metadata, assetURL, archive)

	oldBinary, err := os.ReadFile(srv.droidspacesPath)
	if err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(srv.droidspacesPath)
	if err != nil {
		t.Fatal(err)
	}
	srv.coreVersionMu.Lock()
	srv.detectedCoreVersion = "v6.4.5"
	srv.coreVersionCheckedAt = time.Now()
	srv.coreVersionMu.Unlock()

	response := httptest.NewRecorder()
	srv.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/core/update?token=secret", nil))
	if response.Code != http.StatusAccepted {
		t.Fatalf("core update start=%d body=%s", response.Code, response.Body.String())
	}
	var accepted struct {
		TaskID string     `json:"taskId"`
		Task   *taskState `json:"task"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("invalid update task response: task=%#v err=%v body=%s", accepted.Task, err, response.Body.String())
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	if task.Kind != "core-update" || task.Path != srv.droidspacesPath {
		t.Fatalf("unexpected completed update task: %#v", task)
	}

	updated, err := os.ReadFile(srv.droidspacesPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != newBinary {
		t.Fatalf("updated core = %q, want %q", updated, newBinary)
	}
	backup, err := os.ReadFile(srv.droidspacesPath + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backup, oldBinary) {
		t.Fatal("previous core backup does not contain the original binary")
	}
	updatedInfo, err := os.Stat(srv.droidspacesPath)
	if err != nil {
		t.Fatal(err)
	}
	if updatedInfo.Mode().Perm() != oldInfo.Mode().Perm() {
		t.Fatalf("updated core mode=%#o, want %#o", updatedInfo.Mode().Perm(), oldInfo.Mode().Perm())
	}
	srv.coreVersionMu.Lock()
	cacheInvalidated := srv.coreVersionCheckedAt.IsZero() && srv.detectedCoreVersion == ""
	srv.coreVersionMu.Unlock()
	if !cacheInvalidated {
		t.Fatal("core version cache was not invalidated after update")
	}
}

func TestCoreUpdateRejectsUntrustedArchiveURL(t *testing.T) {
	archive := []byte("archive")
	sum := sha256.Sum256(archive)
	_, err := selectCoreUpdateAsset(githubCoreRelease{Assets: []githubCoreReleaseAsset{{
		Name:               "droidspaces-v6.5.0-test.tar.gz",
		BrowserDownloadURL: "https://example.invalid/droidspaces.tar.gz",
		Digest:             "sha256:" + hex.EncodeToString(sum[:]),
		Size:               int64(len(archive)),
	}}}, "aarch64")
	if err == nil || !strings.Contains(err.Error(), "not trusted") {
		t.Fatalf("untrusted archive URL error = %v", err)
	}
}

func TestCoreUpdateDownloadRejectsDigestMismatch(t *testing.T) {
	srv, _, _ := newTestServer(t)
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	assetURL := "https://github.com/ravindu644/Droidspaces-OSS/releases/download/v6.5.0/droidspaces-v6.5.0-test.tar.gz"
	archive := coreUpdateTestArchive(t, map[string]string{
		"droidspaces-v6.5.0/" + architecture + "/droidspaces": "new-core",
	})
	srv.coreUpdateHTTPClient = coreUpdateTestClient(t, nil, assetURL, archive)
	task := srv.newTask("core-update", "digest-check")
	asset := githubCoreReleaseAsset{
		Name:               "droidspaces-v6.5.0-test.tar.gz",
		BrowserDownloadURL: assetURL,
		Digest:             "sha256:" + strings.Repeat("0", sha256.Size*2),
		Size:               int64(len(archive)),
	}
	if _, err := srv.downloadCoreUpdateArchive(context.Background(), filepath.Dir(srv.droidspacesPath), asset, task.ID); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("digest mismatch error = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(srv.droidspacesPath), ".droidspaces-release-*.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("digest mismatch left temporary release files: %#v", matches)
	}
}

func TestExtractCoreBinaryFromArchiveRejectsUnsafePaths(t *testing.T) {
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	archive := coreUpdateTestArchive(t, map[string]string{
		"../outside": "must-not-extract",
		"droidspaces-v6.5.0/" + architecture + "/droidspaces": "new-core",
	})
	archivePath := filepath.Join(t.TempDir(), "unsafe.tar.gz")
	mustWriteFile(t, archivePath, archive, 0600)
	output, err := os.CreateTemp(t.TempDir(), "core-output-*")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := extractCoreBinaryFromArchive(archivePath, architecture, output); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("unsafe archive error = %v", err)
	}
}

func TestContainerCreateDownloadsConfiguredCloudRootfsInBackgroundTask(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	archivePath := filepath.Join(t.TempDir(), "cloud-template.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"etc/issue": "cloud template"})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}

	var assetRequests int64
	var cloud *httptest.Server
	cloud = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rootfs.json":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"name":         "Cloud test template",
				"architecture": rootfs.DeviceArch(),
				"download_url": cloud.URL + "/cloud-template.tar.gz",
				"size_bytes":   len(archive),
			}})
		case "/cloud-template.tar.gz":
			atomic.AddInt64(&assetRequests, 1)
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

	srv.rootfsRepos = []config.RootfsRepository{{Name: "Cloud test", URL: cloud.URL + "/rootfs.json"}}
	if _, err := srv.configuredRootfsAsset(context.Background(), cloud.URL+"/not-published.tar.gz", rootfs.DeviceArch()); err == nil || !strings.Contains(err.Error(), "not present in configured") {
		t.Fatalf("unpublished cloud URL error = %v", err)
	}
	payload, err := json.Marshal(map[string]any{
		"name":           "cloud-install",
		"rootfsSource":   "cloud",
		"cloudRootfsUrl": cloud.URL + "/cloud-template.tar.gz",
		"useSparseImage": false,
		"netMode":        "host",
	})
	if err != nil {
		t.Fatal(err)
	}

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", bytes.NewReader(payload)))
	if res.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad task response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}

	task := waitForTaskDone(t, srv, accepted.TaskID)
	if task.Kind != "container-create" || task.Path != filepath.Join(workspace, "Containers", "cloud-install", "container.config") {
		t.Fatalf("unexpected cloud create task: %#v", task)
	}
	if atomic.LoadInt64(&assetRequests) != 1 {
		t.Fatalf("cloud archive requests=%d, want 1", atomic.LoadInt64(&assetRequests))
	}
	for _, want := range []string{
		"Fetching configured cloud rootfs metadata",
		"Started shared cloud rootfs download task",
		"Cloud rootfs download completed",
		"Container installed successfully",
	} {
		if !strings.Contains(task.Output, want) {
			t.Fatalf("cloud create task log missing %q:\n%s", want, task.Output)
		}
	}
	assertFile(t, filepath.Join(workspace, "Containers", "cloud-install", "rootfs", "etc", "issue"), "cloud template")
	cloudSource := rootfsTemplateStorageSourceForRepository(srv.rootfsRepos[0])
	items, err := srv.localRootfsItems()
	if err != nil {
		t.Fatal(err)
	}
	foundCloudTemplate := false
	for _, item := range items {
		if item.Source == cloudSource.label && filepath.Dir(item.Path) == filepath.Join(srv.templateImageRoot, cloudSource.directory) {
			foundCloudTemplate = true
			break
		}
	}
	if !foundCloudTemplate {
		t.Fatalf("cloud template was not retained under source storage %q: %#v", cloudSource.directory, items)
	}
}

func TestConfiguredRootfsAssetAcceptsLinuxContainersSimpleStreams(t *testing.T) {
	srv, _, _ := newTestServer(t)
	var mirror *httptest.Server
	mirror = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/streams/v1/images.json" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"products": map[string]any{
				"debian:bookworm:arm64:default": map[string]any{
					"arch":    "arm64",
					"os":      "Debian",
					"release": "bookworm",
					"variant": "default",
					"versions": map[string]any{
						"20260810_05:52": map[string]any{
							"items": map[string]any{
								"root.tar.xz": map[string]any{
									"ftype":  "root.tar.xz",
									"path":   "images/debian/bookworm/arm64/default/20260810_05:52/rootfs.tar.xz",
									"size":   12345,
									"sha256": "test-checksum",
								},
							},
						},
					},
				},
			},
		})
	}))
	defer mirror.Close()

	srv.rootfsRepos = []config.RootfsRepository{{Name: "Linux Containers", URL: mirror.URL + "/streams/v1/images.json"}}
	wantURL := mirror.URL + "/images/debian/bookworm/arm64/default/20260810_05:52/rootfs.tar.xz"
	asset, err := srv.configuredRootfsAsset(context.Background(), wantURL, "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if asset.Name != "Debian 12 (bookworm)" || asset.Architecture != "aarch64" || asset.DownloadURL != wantURL {
		t.Fatalf("unexpected SimpleStreams asset: %#v", asset)
	}
	if asset.SourceRepoName != "Linux Containers" || asset.SizeBytes != 12345 || asset.SHA256 != "test-checksum" || asset.Variant != "default" {
		t.Fatalf("missing SimpleStreams metadata: %#v", asset)
	}
}

func TestRootfsDownloadStoresLinuxContainersCloudVariantUnderSourceAndVariant(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
	payload := []byte("linux-containers-cloud-rootfs")
	var mirror *httptest.Server
	mirror = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/streams/v1/images.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"products": map[string]any{
					"debian:trixie:arm64:cloud": map[string]any{
						"arch":    "arm64",
						"os":      "Debian",
						"release": "trixie",
						"variant": "cloud",
						"versions": map[string]any{
							"20260811_00:00": map[string]any{
								"items": map[string]any{
									"root.tar.xz": map[string]any{
										"ftype": "root.tar.xz",
										"path":  "images/debian/trixie/arm64/cloud/20260811_00:00/rootfs.tar.xz",
										"size":  len(payload),
									},
								},
							},
						},
					},
				},
			})
		case "/images/debian/trixie/arm64/cloud/20260811_00:00/rootfs.tar.xz":
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mirror.Close()

	srv.rootfsRepos = []config.RootfsRepository{{Name: config.LinuxContainersRepositoryName, URL: mirror.URL + "/streams/v1/images.json"}}
	downloadURL := mirror.URL + "/images/debian/trixie/arm64/cloud/20260811_00:00/rootfs.tar.xz"
	body := strings.NewReader(`{"architecture":"aarch64","downloadUrl":"` + downloadURL + `"}`)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/rootfs/download?token=secret", body))
	if res.Code != http.StatusAccepted {
		t.Fatalf("download status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("download task response=%s err=%v", res.Body.String(), err)
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	wantDir := filepath.Join(templateRoot, rootfsLinuxContainersDirectory, "cloud")
	if filepath.Dir(task.Path) != wantDir {
		t.Fatalf("download path %q, want directory %q", task.Path, wantDir)
	}
	if string(mustReadFile(t, task.Path)) != string(payload) {
		t.Fatal("stored cloud rootfs payload differs")
	}
}

func TestContainerCreateStartAndDeleteWorkflow(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	templateDir := filepath.Join(templateRoot, "ubuntu-template")
	mustWriteFile(t, filepath.Join(templateDir, "etc", "issue"), []byte("Ubuntu template"), 0644)

	payload := `{
		"name":"Ubuntu 24.04",
		"hostname":"ubuntu-web",
		"rootfsSource":"local",
		"rootfsPath":"` + templateDir + `",
		"netMode":"nat",
		"dnsServers":"1.1.1.1,8.8.8.8",
		"portForwards":"2222:22/tcp",
		"bindMounts":"/sdcard:/mnt/sdcard:ro",
		"customInit":"/sbin/init",
		"env":"FOO=bar\nBAZ=qux",
		"start":true,
		"androidStorage":true,
		"gpuMode":true,
		"termuxX11":true,
		"pulseAudio":true,
		"volatileMode":true,
		"disableIPv6":true
	}`
	req := httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", strings.NewReader(payload))
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad task response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	containerDir := filepath.Join(workspace, "Containers", "Ubuntu-24.04")
	configPath := filepath.Join(containerDir, "container.config")
	if task.Kind != "container-create" || task.Name != "Ubuntu 24.04" || task.Path != configPath {
		t.Fatalf("unexpected create task: %#v", task)
	}
	if !strings.Contains(task.Output, "Starting container installation") || !strings.Contains(task.Output, "Container started successfully") || !strings.Contains(task.Output, "NAT network diagnostics passed") {
		t.Fatalf("create task log missing install/start output:\n%s", task.Output)
	}
	if _, err := os.Stat(filepath.Join(containerDir, "rootfs.img")); err != nil {
		t.Fatalf("rootfs.img was not created: %v", err)
	}
	assertFile(t, filepath.Join(containerDir, ".env"), "FOO=bar\nBAZ=qux\n")

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configData)
	for _, want := range []string{
		"name=Ubuntu 24.04",
		"hostname=ubuntu-web",
		"rootfs_path=" + filepath.Join(containerDir, "rootfs.img"),
		"use_sparse_image=1",
		"sparse_image_size_gb=8",
		"net_mode=nat",
		"disable_ipv6=1",
		"enable_android_storage=1",
		"enable_gpu_mode=1",
		"enable_termux_x11=1",
		"enable_pulseaudio=1",
		"volatile_mode=1",
		"dns_servers=1.1.1.1,8.8.8.8",
		"port_forwards=2222:22/tcp",
		"bind_mounts=/sdcard:/mnt/sdcard:ro",
		"custom_init=/sbin/init",
		"env_file=" + filepath.Join(workspace, "Containers", "Ubuntu-24.04", ".env"),
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("container config missing %q:\n%s", want, configText)
		}
	}
	calls := readOptionalFile(t, filepath.Join(workspace, "droidspaces-calls.log"))
	if !strings.Contains(calls, "--config="+configPath+" start") {
		t.Fatalf("start command not recorded in calls %q", calls)
	}
	containerLogDir := filepath.Join(workspace, "Logs", "Ubuntu-24.04")
	lowercaseLogDir := filepath.Join(workspace, "logs", "Ubuntu-24.04")
	unrelatedLog := filepath.Join(workspace, "Logs", "droidspacesd.log")
	mustWriteFile(t, filepath.Join(containerLogDir, "log"), []byte("container log"), 0644)
	mustWriteFile(t, filepath.Join(lowercaseLogDir, "log"), []byte("legacy lowercase log"), 0644)
	mustWriteFile(t, unrelatedLog, []byte("daemon log"), 0644)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/containers/Ubuntu-24.04?token=secret", nil)
	deleteRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := os.Stat(containerDir); !os.IsNotExist(err) {
		t.Fatalf("container dir still exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(containerLogDir); !os.IsNotExist(err) {
		t.Fatalf("container log dir still exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(lowercaseLogDir); !os.IsNotExist(err) {
		t.Fatalf("lowercase container log dir still exists or unexpected stat error: %v", err)
	}
	if _, err := os.Stat(unrelatedLog); err != nil {
		t.Fatalf("unrelated daemon log was removed: %v", err)
	}
}

func TestAsyncContainerLifecycleTasksArePerContainer(t *testing.T) {
	srv, _, _ := newTestServer(t)

	first, releaseFirst, err := srv.beginContainerTask("container-stop", "first")
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" {
		t.Fatal("first task ID is empty")
	}
	if _, _, err := srv.beginContainerTask("container-delete", "first"); err == nil {
		t.Fatal("same container accepted a conflicting operation")
	}
	_, releaseSecond, err := srv.beginContainerTask("container-stop", "second")
	if err != nil {
		t.Fatalf("different container was blocked: %v", err)
	}
	releaseSecond()
	releaseFirst()
	if _, releaseRetry, err := srv.beginContainerTask("container-restart", "first"); err != nil {
		t.Fatalf("released container remained blocked: %v", err)
	} else {
		releaseRetry()
	}
	held, releaseHeld, err := srv.beginContainerTask("container-stop", "held")
	if err != nil {
		t.Fatal(err)
	}
	defer releaseHeld()
	if held.ID == "" {
		t.Fatal("held task ID is empty")
	}
	conflict := httptest.NewRecorder()
	srv.Handler().ServeHTTP(conflict, httptest.NewRequest(http.MethodPost, "/api/containers/held/restart?async=1&token=secret", nil))
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting lifecycle status=%d body=%s", conflict.Code, conflict.Body.String())
	}

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers/first/start?async=1&token=secret", nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("async lifecycle status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad async lifecycle response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	if task.Kind != "container-start" || task.Name != "first" {
		t.Fatalf("unexpected lifecycle task: %#v", task)
	}
}

func TestAsyncDeleteContainerTaskRemovesContainer(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "async-delete")
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=async-delete\nnet_mode=nat\n"), 0644)
	mustWriteFile(t, filepath.Join(containerDir, "rootfs", "etc", "issue"), []byte("delete me"), 0644)

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodDelete, "/api/containers/async-delete?async=1&token=secret", nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("async delete status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad async delete response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}
	task := waitForTaskDone(t, srv, accepted.TaskID)
	if task.Kind != "container-delete" || task.Name != "async-delete" || task.Path != containerDir {
		t.Fatalf("unexpected delete task: %#v", task)
	}
	if _, err := os.Stat(containerDir); !os.IsNotExist(err) {
		t.Fatalf("container dir still exists or unexpected stat error: %v", err)
	}
}

func TestContainerExportTemplateAndDownloadWorkflow(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "demo")
	rootfsDir := filepath.Join(containerDir, "rootfs")
	mustWriteFile(t, filepath.Join(rootfsDir, "etc", "issue"), []byte("export me"), 0644)
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=demo\nrootfs_path="+rootfsDir+"\nnet_mode=host\n"), 0644)

	handler := srv.Handler()
	backupTask := startContainerTask(t, handler, "/api/containers/demo/export?token=secret")
	backup := waitForTaskDone(t, srv, backupTask)
	if backup.Kind != "container-export" || backup.URL == "" || !strings.Contains(filepath.Base(backup.Path), "demo-backup-") {
		t.Fatalf("unexpected backup task: %#v", backup)
	}
	if !strings.HasPrefix(backup.Path, filepath.Join(templateRoot, "exports")) {
		t.Fatalf("backup path %q not under exports", backup.Path)
	}
	assertTarGzContains(t, backup.Path, "etc/issue", "export me")

	downloadReq := httptest.NewRequest(http.MethodGet, backup.URL+"?token=secret", nil)
	downloadRes := httptest.NewRecorder()
	handler.ServeHTTP(downloadRes, downloadReq)
	if downloadRes.Code != http.StatusOK || downloadRes.Body.Len() == 0 {
		t.Fatalf("backup download status=%d size=%d", downloadRes.Code, downloadRes.Body.Len())
	}

	templateTask := startContainerTask(t, handler, "/api/containers/demo/template?token=secret")
	template := waitForTaskDone(t, srv, templateTask)
	if template.Kind != "container-template" || !strings.Contains(filepath.Base(template.Path), "demo-template-") {
		t.Fatalf("unexpected template task: %#v", template)
	}
	if filepath.Dir(template.Path) != templateRoot {
		t.Fatalf("template path %q not directly under template root %q", template.Path, templateRoot)
	}
	assertTarGzContains(t, template.Path, "etc/issue", "export me")
}

func TestContainerExportStopsAndRestoresRunningContainer(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.socketdEnabled = true
	containerDir := filepath.Join(workspace, "Containers", "demo")
	rootfsDir := filepath.Join(containerDir, "rootfs")
	mustWriteFile(t, filepath.Join(rootfsDir, "etc", "issue"), []byte("running export"), 0644)
	mustWriteFile(t, filepath.Join(rootfsDir, "var", "payload"), []byte(strings.Repeat("payload", 128)), 0644)
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=demo\nrootfs_path="+rootfsDir+"\nnet_mode=host\n"), 0644)
	t.Setenv("FAKE_PID", "1234")

	handler := srv.Handler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "/api/containers/demo/export?token=secret", nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("export status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID             string     `json:"taskId"`
		WillStopContainer  bool       `json:"willStopContainer"`
		RestoreAfterBackup bool       `json:"restoreAfterBackup"`
		Task               *taskState `json:"task"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil {
		t.Fatal(err)
	}
	if accepted.TaskID == "" || !accepted.WillStopContainer || !accepted.RestoreAfterBackup || accepted.Task == nil || !accepted.Task.WillStopContainer || !accepted.Task.RestoreAfterBackup {
		t.Fatalf("missing stop/restore hints: %#v body=%s", accepted, res.Body.String())
	}

	task := waitForTaskDone(t, srv, accepted.TaskID)
	if !task.WillStopContainer || !task.RestoreAfterBackup || task.Total <= 0 || task.Downloaded <= 0 || task.Percent != 100 {
		t.Fatalf("bad completed task progress/hints: %#v", task)
	}
	calls := readOptionalFile(t, filepath.Join(workspace, "droidspaces-calls.log"))
	if !strings.Contains(calls, "--name demo stop") {
		t.Fatalf("stop command not recorded in calls %q", calls)
	}
	if !strings.Contains(calls, "--config "+filepath.Join(containerDir, "container.config")+" start") {
		t.Fatalf("restart command not recorded in calls %q", calls)
	}
}

func startContainerTask(t *testing.T, handler http.Handler, path string) string {
	t.Helper()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodPost, path, nil))
	if res.Code != http.StatusAccepted {
		t.Fatalf("task start %s status=%d body=%s", path, res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad task response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}
	return accepted.TaskID
}

func waitForTaskDone(t *testing.T, srv *Server, taskID string) *taskState {
	t.Helper()
	var task *taskState
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		var ok bool
		task, ok = srv.getTask(taskID)
		if ok && task.Status == "done" {
			return task
		}
		if ok && task.Status == "error" {
			t.Fatalf("task %s failed: %s", taskID, task.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not complete: %#v", taskID, task)
	return task
}

func TestTasksAndHostAuthAndEmptyResponses(t *testing.T) {
	srv, _, _ := newTestServer(t)
	handler := srv.Handler()

	for _, path := range []string{"/api/tasks", "/api/host"} {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token status=%d body=%s", path, res.Code, res.Body.String())
		}
	}

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/tasks?token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("empty tasks status=%d body=%s", res.Code, res.Body.String())
	}
	var body struct {
		Tasks []taskState `json:"tasks"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Tasks == nil || len(body.Tasks) != 0 {
		t.Fatalf("tasks should be empty array, got %#v body=%s", body.Tasks, res.Body.String())
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/api/tasks/missing?token=secret", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing task status=%d body=%s", missing.Code, missing.Body.String())
	}
	badID := httptest.NewRecorder()
	handler.ServeHTTP(badID, httptest.NewRequest(http.MethodGet, "/api/tasks/bad/id?token=secret", nil))
	if badID.Code != http.StatusBadRequest {
		t.Fatalf("bad task id status=%d body=%s", badID.Code, badID.Body.String())
	}

	report := srv.pathReport("empty", "")
	if report.Error != "not configured" || report.Exists {
		t.Fatalf("bad empty path report: %#v", report)
	}
}

func TestTasksListAndHostEndpoint(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	running := srv.newTask("rootfs-download", "alpine")
	srv.updateTask(running.ID, func(task *taskState) {
		task.Status = "running"
		task.Percent = 42
	})
	done := srv.newTask("container-export", "demo")
	srv.completeTask(done.ID, filepath.Join(templateRoot, "demo.tar.gz"), "/api/downloads/"+done.ID)

	handler := srv.Handler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/tasks?token=secret", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("tasks status=%d body=%s", res.Code, res.Body.String())
	}
	var tasksBody struct {
		Tasks   []taskState `json:"tasks"`
		Summary taskSummary `json:"summary"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &tasksBody); err != nil {
		t.Fatal(err)
	}
	if len(tasksBody.Tasks) != 2 {
		t.Fatalf("tasks len=%d body=%s", len(tasksBody.Tasks), res.Body.String())
	}
	if tasksBody.Tasks[0].UpdatedAt < tasksBody.Tasks[1].UpdatedAt {
		t.Fatalf("tasks are not sorted by updatedAt desc: %#v", tasksBody.Tasks)
	}
	if tasksBody.Summary.Total != 2 || tasksBody.Summary.Active != 1 || tasksBody.Summary.Running != 1 || tasksBody.Summary.Done != 1 || tasksBody.Summary.ByKind["rootfs-download"] != 1 || tasksBody.Summary.ByKind["container-export"] != 1 {
		t.Fatalf("unexpected task summary: %#v", tasksBody.Summary)
	}

	hostRes := httptest.NewRecorder()
	handler.ServeHTTP(hostRes, httptest.NewRequest(http.MethodGet, "/api/host?token=secret", nil))
	if hostRes.Code != http.StatusOK {
		t.Fatalf("host status=%d body=%s", hostRes.Code, hostRes.Body.String())
	}
	var hostBody struct {
		GOOS          string `json:"goos"`
		GOARCH        string `json:"goarch"`
		UptimeSeconds uint64 `json:"uptimeSeconds"`
		Paths         []struct {
			Key       string `json:"key"`
			Path      string `json:"path"`
			Exists    bool   `json:"exists"`
			DiskTotal uint64 `json:"diskTotal"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(hostRes.Body.Bytes(), &hostBody); err != nil {
		t.Fatal(err)
	}
	if hostBody.GOOS == "" || hostBody.GOARCH == "" || hostBody.UptimeSeconds == 0 || len(hostBody.Paths) == 0 {
		t.Fatalf("bad host body: %#v", hostBody)
	}
	foundWorkspace := false
	for _, item := range hostBody.Paths {
		if item.Key == "workspace" {
			foundWorkspace = true
			if item.Path != workspace || !item.Exists || item.DiskTotal == 0 {
				t.Fatalf("bad workspace report: %#v", item)
			}
		}
	}
	if !foundWorkspace {
		t.Fatalf("workspace path report missing: %#v", hostBody.Paths)
	}
}

func TestRootfsDownloadTaskRecordsProgressAndDownloadURL(t *testing.T) {
	assetBytes := []byte("rootfs-payload")
	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(assetBytes)
	}))
	defer assetServer.Close()

	repoBody := `[{"name":"Alpine","architecture":"aarch64","download_url":"` + assetServer.URL + `/alpine.tar.gz","size_bytes":14,"build_date":"2026-06-28","author":"Droidspaces"}]`
	repoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(repoBody))
	}))
	defer repoServer.Close()

	srv, _, templateRoot := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "test", URL: repoServer.URL + "/rootfs.json"}}
	handler := srv.Handler()
	body := strings.NewReader(`{"name":"Tampered","architecture":"aarch64","downloadUrl":"` + assetServer.URL + `/alpine.tar.gz","sizeBytes":999999,"buildDate":"2099-01-01","author":"attacker","uniqueFilename":"evil.img"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/rootfs/download", body)
	req.Header.Set("Authorization", "Bearer secret")
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("download status=%d body=%s", res.Code, res.Body.String())
	}
	var accepted struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
		t.Fatalf("bad task response id=%q err=%v body=%s", accepted.TaskID, err, res.Body.String())
	}

	var task *taskState
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		var ok bool
		task, ok = srv.getTask(accepted.TaskID)
		if ok && task.Status == "done" {
			break
		}
		if ok && task.Status == "error" {
			t.Fatalf("task failed: %s", task.Error)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if task == nil || task.Status != "done" || task.Percent != 100 || task.URL == "" {
		t.Fatalf("task not completed: %#v", task)
	}
	for _, want := range []string{
		"Verifying selected cloud rootfs against configured repositories",
		"Selected cloud rootfs: Alpine",
		"Cloud rootfs download completed",
	} {
		if !strings.Contains(task.Output, want) {
			t.Fatalf("download task log missing %q:\n%s", want, task.Output)
		}
	}
	if !strings.HasPrefix(task.Path, templateRoot) {
		t.Fatalf("task path %q outside template root %q", task.Path, templateRoot)
	}
	storageSource := rootfsTemplateStorageSourceForRepository(srv.rootfsRepos[0])
	if filepath.Dir(task.Path) != filepath.Join(templateRoot, storageSource.directory) {
		t.Fatalf("task path %q is not in source storage %q", task.Path, storageSource.directory)
	}
	if filepath.Base(task.Path) == "evil.img" || !strings.Contains(filepath.Base(task.Path), "Alpine") {
		t.Fatalf("download used client-controlled metadata: %s", task.Path)
	}
	data, err := os.ReadFile(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(assetBytes) {
		t.Fatalf("downloaded data = %q", string(data))
	}
}

func TestRootfsDownloadAcceptsTaskBeforeMetadataVerificationCompletes(t *testing.T) {
	assetBytes := []byte("metadata-delayed-rootfs")
	metadataStarted := make(chan struct{}, 1)
	releaseMetadata := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseMetadata) }) }
	defer release()

	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(assetBytes)))
		_, _ = w.Write(assetBytes)
	}))
	defer assetServer.Close()

	repoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case metadataStarted <- struct{}{}:
		default:
		}
		<-releaseMetadata
		_, _ = w.Write([]byte(`[{"name":"Delayed Alpine","architecture":"` + rootfs.DeviceArch() + `","download_url":"` + assetServer.URL + `/delayed.tar.gz","size_bytes":` + strconv.Itoa(len(assetBytes)) + `}]`))
	}))
	defer repoServer.Close()

	srv, _, _ := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "delayed", URL: repoServer.URL + "/rootfs.json"}}
	body := `{"architecture":"` + rootfs.DeviceArch() + `","downloadUrl":"` + assetServer.URL + `/delayed.tar.gz"}`
	type response struct {
		result *httptest.ResponseRecorder
	}
	responses := make(chan response, 1)
	go func() {
		result := httptest.NewRecorder()
		srv.Handler().ServeHTTP(result, httptest.NewRequest(http.MethodPost, "/api/rootfs/download?token=secret", strings.NewReader(body)))
		responses <- response{result: result}
	}()

	var result *httptest.ResponseRecorder
	select {
	case received := <-responses:
		result = received.result
	case <-time.After(500 * time.Millisecond):
		release()
		t.Fatal("rootfs download did not return a task before metadata verification finished")
	}
	if result.Code != http.StatusAccepted {
		t.Fatalf("download status=%d body=%s", result.Code, result.Body.String())
	}
	var accepted struct {
		TaskID string     `json:"taskId"`
		Task   *taskState `json:"task"`
	}
	if err := json.Unmarshal(result.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" || accepted.Task == nil {
		t.Fatalf("bad accepted download response: id=%q task=%#v err=%v body=%s", accepted.TaskID, accepted.Task, err, result.Body.String())
	}
	if accepted.Task.Status != "pending" && accepted.Task.Status != "running" {
		t.Fatalf("accepted task status=%q, want pending or running", accepted.Task.Status)
	}
	select {
	case <-metadataStarted:
	case <-time.After(time.Second):
		t.Fatal("background metadata verification did not start")
	}

	release()
	completed := waitForTaskDone(t, srv, accepted.TaskID)
	if !strings.Contains(completed.Output, "Verifying selected cloud rootfs against configured repositories") {
		t.Fatalf("task log did not record metadata verification:\n%s", completed.Output)
	}
}

func TestRootfsDownloadReusesInFlightTaskForSameAsset(t *testing.T) {
	assetBytes := []byte("shared-rootfs-payload")
	archiveStarted := make(chan struct{}, 1)
	releaseArchive := make(chan struct{})
	var archiveRequests int64
	assetServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&archiveRequests, 1)
		archiveStarted <- struct{}{}
		<-releaseArchive
		w.Header().Set("Content-Length", strconv.Itoa(len(assetBytes)))
		_, _ = w.Write(assetBytes)
	}))
	defer assetServer.Close()

	repoBody := `[{"name":"Shared Alpine","architecture":"` + rootfs.DeviceArch() + `","download_url":"` + assetServer.URL + `/shared.tar.gz","size_bytes":` + strconv.Itoa(len(assetBytes)) + `}]`
	repoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(repoBody))
	}))
	defer repoServer.Close()

	srv, _, _ := newTestServer(t)
	srv.rootfsRepos = []config.RootfsRepository{{Name: "shared", URL: repoServer.URL + "/rootfs.json"}}
	handler := srv.Handler()
	post := func() *httptest.ResponseRecorder {
		body := strings.NewReader(`{"architecture":"` + rootfs.DeviceArch() + `","downloadUrl":"` + assetServer.URL + `/shared.tar.gz"}`)
		request := httptest.NewRequest(http.MethodPost, "/api/rootfs/download?token=secret", body)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first download status=%d body=%s", first.Code, first.Body.String())
	}
	select {
	case <-archiveStarted:
	case <-time.After(time.Second):
		t.Fatal("first archive request did not start")
	}
	second := post()
	if second.Code != http.StatusAccepted {
		t.Fatalf("second download status=%d body=%s", second.Code, second.Body.String())
	}
	var firstAccepted, secondAccepted struct {
		TaskID string `json:"taskId"`
		Shared bool   `json:"shared"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstAccepted); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondAccepted); err != nil {
		t.Fatal(err)
	}
	if firstAccepted.TaskID == "" || firstAccepted.TaskID != secondAccepted.TaskID || !secondAccepted.Shared {
		t.Fatalf("duplicate download was not joined: first=%#v second=%#v", firstAccepted, secondAccepted)
	}
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests before release=%d, want 1", got)
	}
	close(releaseArchive)
	waitForTaskDone(t, srv, firstAccepted.TaskID)
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests after completion=%d, want 1", got)
	}
}

func TestCloudContainerCreatesShareInFlightRootfsDownload(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	archivePath := filepath.Join(t.TempDir(), "shared-cloud-template.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"etc/issue": "shared cloud template"})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveStarted := make(chan struct{}, 1)
	releaseArchive := make(chan struct{})
	var archiveRequests int64
	var cloud *httptest.Server
	cloud = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rootfs.json":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"name":         "Shared cloud template",
				"architecture": rootfs.DeviceArch(),
				"download_url": cloud.URL + "/shared-cloud-template.tar.gz",
				"size_bytes":   len(archive),
			}})
		case "/shared-cloud-template.tar.gz":
			atomic.AddInt64(&archiveRequests, 1)
			archiveStarted <- struct{}{}
			<-releaseArchive
			w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

	srv.rootfsRepos = []config.RootfsRepository{{Name: "Cloud test", URL: cloud.URL + "/rootfs.json"}}
	handler := srv.Handler()
	create := func(name string) string {
		payload, err := json.Marshal(map[string]any{
			"name":           name,
			"rootfsSource":   "cloud",
			"cloudRootfsUrl": cloud.URL + "/shared-cloud-template.tar.gz",
			"netMode":        "host",
		})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", bytes.NewReader(payload)))
		if response.Code != http.StatusAccepted {
			t.Fatalf("create %s status=%d body=%s", name, response.Code, response.Body.String())
		}
		var accepted struct {
			TaskID string `json:"taskId"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil || accepted.TaskID == "" {
			t.Fatalf("create %s task id=%q err=%v", name, accepted.TaskID, err)
		}
		return accepted.TaskID
	}
	firstTask := create("shared-cloud-one")
	select {
	case <-archiveStarted:
	case <-time.After(time.Second):
		t.Fatal("shared cloud archive request did not start")
	}
	secondTask := create("shared-cloud-two")
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests before release=%d, want 1", got)
	}
	close(releaseArchive)
	waitForTaskDone(t, srv, firstTask)
	waitForTaskDone(t, srv, secondTask)
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests after completion=%d, want 1", got)
	}
	for _, name := range []string{"shared-cloud-one", "shared-cloud-two"} {
		if _, err := os.Stat(filepath.Join(workspace, "Containers", name, "container.config")); err != nil {
			t.Fatalf("container %s was not created: %v", name, err)
		}
	}
}

func TestRootfsPreDownloadAndCloudCreateShareInFlightDownload(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	archivePath := filepath.Join(t.TempDir(), "shared-pre-download.tar.gz")
	writeTarGz(t, archivePath, map[string]string{"etc/issue": "shared pre-download template"})
	archive, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	archiveStarted := make(chan struct{}, 1)
	releaseArchive := make(chan struct{})
	var archiveRequests int64
	var cloud *httptest.Server
	cloud = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rootfs.json":
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"name":         "Shared pre-download template",
				"architecture": rootfs.DeviceArch(),
				"download_url": cloud.URL + "/shared-pre-download.tar.gz",
				"size_bytes":   len(archive),
			}})
		case "/shared-pre-download.tar.gz":
			atomic.AddInt64(&archiveRequests, 1)
			select {
			case archiveStarted <- struct{}{}:
			default:
			}
			<-releaseArchive
			w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer cloud.Close()

	srv.rootfsRepos = []config.RootfsRepository{{Name: "Cloud test", URL: cloud.URL + "/rootfs.json"}}
	handler := srv.Handler()
	downloadBody := strings.NewReader(`{"architecture":"` + rootfs.DeviceArch() + `","downloadUrl":"` + cloud.URL + `/shared-pre-download.tar.gz"}`)
	downloadResponse := httptest.NewRecorder()
	handler.ServeHTTP(downloadResponse, httptest.NewRequest(http.MethodPost, "/api/rootfs/download?token=secret", downloadBody))
	if downloadResponse.Code != http.StatusAccepted {
		t.Fatalf("pre-download status=%d body=%s", downloadResponse.Code, downloadResponse.Body.String())
	}
	var preDownload struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(downloadResponse.Body.Bytes(), &preDownload); err != nil || preDownload.TaskID == "" {
		t.Fatalf("pre-download task id=%q err=%v", preDownload.TaskID, err)
	}
	select {
	case <-archiveStarted:
	case <-time.After(time.Second):
		t.Fatal("pre-download archive request did not start")
	}

	payload, err := json.Marshal(map[string]any{
		"name":           "shared-pre-download-container",
		"rootfsSource":   "cloud",
		"cloudRootfsUrl": cloud.URL + "/shared-pre-download.tar.gz",
		"netMode":        "host",
	})
	if err != nil {
		t.Fatal(err)
	}
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", bytes.NewReader(payload)))
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil || created.TaskID == "" {
		t.Fatalf("create task id=%q err=%v", created.TaskID, err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if task, ok := srv.getTask(created.TaskID); ok && strings.Contains(task.Output, "shared cloud rootfs download task") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests before release=%d, want 1", got)
	}
	close(releaseArchive)
	waitForTaskDone(t, srv, preDownload.TaskID)
	waitForTaskDone(t, srv, created.TaskID)
	if got := atomic.LoadInt64(&archiveRequests); got != 1 {
		t.Fatalf("archive requests after completion=%d, want 1", got)
	}
	if _, err := os.Stat(filepath.Join(workspace, "Containers", "shared-pre-download-container", "container.config")); err != nil {
		t.Fatalf("container was not created: %v", err)
	}
}

func TestPrepareRootfsForContainerCopiesAndExtractsTemplates(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	ctx := context.Background()
	containerDir := filepath.Join(workspace, "Containers", "demo")
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		t.Fatal(err)
	}

	dirTemplate := filepath.Join(templateRoot, "dir-template")
	mustWriteFile(t, filepath.Join(dirTemplate, "etc", "issue"), []byte("dir-template"), 0644)
	directDir, err := srv.prepareRootfsForContainer(ctx, createContainerRequest{RootFSStorageMode: "directory"}, "direct", dirTemplate, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	if directDir != dirTemplate {
		t.Fatalf("direct directory path = %s, want %s", directDir, dirTemplate)
	}

	copiedDirImg, err := srv.prepareRootfsForContainer(ctx, createContainerRequest{RootFSImageSizeGB: 8}, "local", dirTemplate, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(copiedDirImg) != "rootfs.img" {
		t.Fatalf("local directory template should create rootfs.img, got %s", copiedDirImg)
	}

	imgTemplate := filepath.Join(templateRoot, "rootfs.img")
	mustWriteFile(t, imgTemplate, []byte("img-template"), 0644)
	copiedImg, err := srv.prepareRootfsForContainer(ctx, createContainerRequest{}, "local", imgTemplate, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, copiedImg, "img-template")
	if filepath.Base(copiedImg) != "rootfs.img" {
		t.Fatalf("copied image path = %s", copiedImg)
	}

	archive := filepath.Join(templateRoot, "rootfs.tar.gz")
	writeTarGz(t, archive, map[string]string{"etc/os-release": "NAME=test\n"})
	archiveImg, err := srv.prepareRootfsForContainer(ctx, createContainerRequest{}, "local", archive, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(archiveImg) != "rootfs.img" {
		t.Fatalf("archive template should create rootfs.img, got %s", archiveImg)
	}
}

func TestWebSocketOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		origin string
		want   bool
	}{
		{name: "no origin", host: "127.0.0.1:9090", want: true},
		{name: "same origin", host: "example.test:9090", origin: "http://example.test:9090", want: true},
		{name: "loopback host mismatch", host: "127.0.0.1:9090", origin: "http://localhost:9090", want: true},
		{name: "cross site", host: "127.0.0.1:9090", origin: "https://evil.example", want: false},
		{name: "bad origin", host: "127.0.0.1:9090", origin: "://bad", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/containers/demo/shell", nil)
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if got := websocketOriginAllowed(req); got != tc.want {
				t.Fatalf("origin allowed = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestShellWebSocketRunsInteractiveEnter(t *testing.T) {
	srv, _, _ := newTestServer(t)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/containers/demo/shell?token=secret&user=root"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	if err := conn.WriteMessage(websocket.TextMessage, []byte("printf READY\\n\n")); err != nil {
		t.Fatal(err)
	}
	output := readWebSocketUntil(t, conn, "READY", 2*time.Second)
	if !strings.Contains(output, "READY") {
		t.Fatalf("shell output missing READY: %q", output)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
}

func readWebSocketUntil(t *testing.T, conn *websocket.Conn, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var out strings.Builder
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if netErr, ok := err.(interface{ Timeout() bool }); ok && netErr.Timeout() {
				continue
			}
			t.Fatal(err)
		}
		out.Write(data)
		if strings.Contains(out.String(), want) {
			return out.String()
		}
	}
	t.Fatalf("timed out waiting for %q in %q", want, out.String())
	return out.String()
}

func TestTerminalBufferHandlesCarriageReturnAndBackspace(t *testing.T) {
	// Keep this behavior mirrored in app.js: carriage return overwrites the line
	// and backspace moves the cursor left before replacement.
	lines, row, col := []string{""}, 0, 0
	appendChar := func(ch rune) {
		for len(lines) <= row {
			lines = append(lines, "")
		}
		switch ch {
		case '\r':
			col = 0
			return
		case '\n':
			row++
			col = 0
			for len(lines) <= row {
				lines = append(lines, "")
			}
			return
		case '\b', 0x7f:
			if col > 0 {
				col--
			}
			return
		}
		line := lines[row]
		if col > len(line) {
			line += strings.Repeat(" ", col-len(line))
		}
		runes := []rune(line)
		if col < len(runes) {
			runes[col] = ch
		} else {
			runes = append(runes, ch)
		}
		lines[row] = string(runes)
		col++
	}
	for _, ch := range "progress 10%\rprogress 100%\nabc\bZ" {
		appendChar(ch)
	}
	if got := strings.Join(lines, "\n"); got != "progress 100%\nabZ" {
		t.Fatalf("terminal buffer = %q", got)
	}
}

func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, string(data), want)
	}
}

func assertTarGzContains(t *testing.T, path string, name string, want string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err != nil {
			break
		}
		if strings.TrimPrefix(header.Name, "./") != name {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("archive %s:%s = %q, want %q", path, name, string(data), want)
		}
		return
	}
	t.Fatalf("archive %s missing %s", path, name)
}

func readOptionalFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return string(data)
}

func writeTarGz(t *testing.T, path string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(out)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		data := []byte(files[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPortForwardConflictsAcrossContainers(t *testing.T) {
	srv, workspace, templateRoot := newTestServer(t)
	srv.socketdEnabled = false
	archive := filepath.Join(templateRoot, "ubuntu.tar.gz")
	writeTarGz(t, archive, map[string]string{"etc/issue": "ubuntu"})
	firstDir := filepath.Join(workspace, "Containers", "first")
	firstRoot := filepath.Join(firstDir, "rootfs")
	mustWriteFile(t, filepath.Join(firstRoot, "etc", "issue"), []byte("first"), 0644)
	mustWriteFile(t, filepath.Join(firstDir, "container.config"), []byte("name=first\nrootfs_path="+firstRoot+"\nnet_mode=nat\nport_forwards=2222:22/tcp,3000-3002:30-32/tcp\n"), 0644)

	handler := srv.Handler()
	createConflict := httptest.NewRecorder()
	createBody := `{"name":"second","rootfsPath":"` + archive + `","rootfsSource":"local","useSparseImage":false,"netMode":"nat","natUpstreamIfnames":"wlan0","portForwards":"2222:22/tcp"}`
	handler.ServeHTTP(createConflict, httptest.NewRequest(http.MethodPost, "/api/containers?token=secret", strings.NewReader(createBody)))
	if createConflict.Code != http.StatusConflict {
		t.Fatalf("create conflict status=%d body=%s", createConflict.Code, createConflict.Body.String())
	}
	if !strings.Contains(createConflict.Body.String(), "first") || !strings.Contains(createConflict.Body.String(), "2222/tcp") {
		t.Fatalf("create conflict message missing owner/port: %s", createConflict.Body.String())
	}

	secondDir := filepath.Join(workspace, "Containers", "second")
	secondRoot := filepath.Join(secondDir, "rootfs")
	mustWriteFile(t, filepath.Join(secondRoot, "etc", "issue"), []byte("second"), 0644)
	secondConfig := filepath.Join(secondDir, "container.config")
	mustWriteFile(t, secondConfig, []byte("name=second\nrootfs_path="+secondRoot+"\nnet_mode=nat\nport_forwards=2223:22/tcp\n"), 0644)

	selfUpdate := httptest.NewRecorder()
	handler.ServeHTTP(selfUpdate, httptest.NewRequest(http.MethodPatch, "/api/containers/second/config?token=secret", strings.NewReader(`{"netMode":"nat","natUpstreamIfnames":"wlan0","portForwards":"2223:22/tcp"}`)))
	if selfUpdate.Code != http.StatusOK {
		t.Fatalf("self update status=%d body=%s", selfUpdate.Code, selfUpdate.Body.String())
	}

	updateConflict := httptest.NewRecorder()
	handler.ServeHTTP(updateConflict, httptest.NewRequest(http.MethodPatch, "/api/containers/second/config?token=secret", strings.NewReader(`{"netMode":"nat","natUpstreamIfnames":"wlan0","portForwards":"3001:22/tcp"}`)))
	if updateConflict.Code != http.StatusConflict {
		t.Fatalf("update conflict status=%d body=%s", updateConflict.Code, updateConflict.Body.String())
	}
	if !strings.Contains(updateConflict.Body.String(), "first") || !strings.Contains(updateConflict.Body.String(), "3001/tcp") {
		t.Fatalf("update conflict message missing owner/port: %s", updateConflict.Body.String())
	}
	if configText := string(mustReadFile(t, secondConfig)); strings.Contains(configText, "3001:22/tcp") {
		t.Fatalf("conflicting update should not be persisted:\n%s", configText)
	}
}

func TestUpdateContainerConfigStopsAndRestoresRunningContainer(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	srv.socketdEnabled = false
	containerDir := filepath.Join(workspace, "Containers", "demo")
	rootfsDir := filepath.Join(containerDir, "rootfs")
	mustWriteFile(t, filepath.Join(rootfsDir, "etc", "issue"), []byte("demo"), 0644)
	configPath := filepath.Join(containerDir, "container.config")
	mustWriteFile(t, configPath, []byte(`name=demo
rootfs_path=`+rootfsDir+`
net_mode=host
unknown_key=keep
# keep comment
`), 0644)
	mustWriteFile(t, filepath.Join(workspace, "Pids", "demo.pid"), []byte("1234\n"), 0644)

	body := strings.NewReader(`{"netMode":"nat","dnsServers":"1.1.1.1","portForwards":"2222:22/tcp","disableIPv6":false,"gpuMode":true}`)
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", body))
	if res.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", res.Code, res.Body.String())
	}
	var resp struct {
		Stopped      bool   `json:"stopped"`
		Restarted    bool   `json:"restarted"`
		RestoreError string `json:"restoreError"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Stopped || !resp.Restarted || resp.RestoreError != "" {
		t.Fatalf("unexpected lifecycle response: %#v body=%s", resp, res.Body.String())
	}

	configText := string(mustReadFile(t, configPath))
	for _, want := range []string{"net_mode=nat", "dns_servers=1.1.1.1", "port_forwards=2222:22/tcp", "disable_ipv6=1", "enable_gpu_mode=1", "unknown_key=keep", "# keep comment"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
	calls := readOptionalFile(t, filepath.Join(workspace, "droidspaces-calls.log"))
	if !strings.Contains(calls, "--name demo stop") || !strings.Contains(calls, "--config "+configPath+" start") {
		t.Fatalf("lifecycle calls not recorded correctly: %q", calls)
	}
}

func TestUpdateContainerGraphicsFlagsClearsDisabledExtras(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "demo")
	configPath := filepath.Join(containerDir, "container.config")
	mustWriteFile(t, configPath, []byte(`name=demo
net_mode=host
enable_termux_x11=1
tx11_extra_flags=--old-x11
enable_virgl=1
virgl_extra_flags=--old-virgl
`), 0644)

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"termuxX11":false,"virgl":false}`)
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", body))
	if res.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", res.Code, res.Body.String())
	}

	configText := string(mustReadFile(t, configPath))
	for _, want := range []string{"enable_termux_x11=0", "enable_virgl=0"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
	for _, removed := range []string{"tx11_extra_flags=", "virgl_extra_flags="} {
		if strings.Contains(configText, removed) {
			t.Fatalf("disabled graphics extra flag %q should be removed:\n%s", removed, configText)
		}
	}
}

func TestUpdateContainerConfigPersistsHumanReadableMemoryLimit(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "demo")
	configPath := filepath.Join(containerDir, "container.config")
	mustWriteFile(t, configPath, []byte("name=demo\nnet_mode=host\n"), 0644)

	res := httptest.NewRecorder()
	body := strings.NewReader(`{"memoryLimit":"4G","cpus":"2","pidsLimit":"256"}`)
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", body))
	if res.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", res.Code, res.Body.String())
	}

	configText := string(mustReadFile(t, configPath))
	for _, want := range []string{"memory_limit=4294967296", "cpu_quota=200000", "cpu_period=100000", "pids_limit=256"} {
		if !strings.Contains(configText, want) {
			t.Fatalf("config missing %q:\n%s", want, configText)
		}
	}
}

func TestUpdateContainerConfigValidation(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	containerDir := filepath.Join(workspace, "Containers", "demo")
	mustWriteFile(t, filepath.Join(containerDir, "container.config"), []byte("name=demo\nnet_mode=host\n"), 0644)
	handler := srv.Handler()

	badNewline := httptest.NewRecorder()
	handler.ServeHTTP(badNewline, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", strings.NewReader("{\"hostname\":\"bad\\nname=oops\"}")))
	if badNewline.Code != http.StatusBadRequest {
		t.Fatalf("newline status=%d body=%s", badNewline.Code, badNewline.Body.String())
	}

	badUnknown := httptest.NewRecorder()
	handler.ServeHTTP(badUnknown, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", strings.NewReader(`{"rootfsPath":"/tmp/rootfs"}`)))
	if badUnknown.Code != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d body=%s", badUnknown.Code, badUnknown.Body.String())
	}

	badMode := httptest.NewRecorder()
	handler.ServeHTTP(badMode, httptest.NewRequest(http.MethodPut, "/api/containers/demo/config?token=secret", strings.NewReader(`{"netMode":"bridge"}`)))
	if badMode.Code != http.StatusBadRequest {
		t.Fatalf("bad mode status=%d body=%s", badMode.Code, badMode.Body.String())
	}
}

func TestParseServiceInfoLineSeparatesRunAndEnableState(t *testing.T) {
	tests := []struct {
		line        string
		state       string
		enableState string
		running     bool
		enabled     bool
	}{
		{"ssh.service|enabled|active|OpenSSH server", "running", "enabled", true, true},
		{"cron.service|disabled|inactive|cron daemon", "stopped", "disabled", false, false},
		{"demo.service|masked|failed|demo", "failed", "masked", false, false},
		{"oneshot.service|static|exited|oneshot", "stopped", "static", false, false},
	}
	for _, tt := range tests {
		item, ok := parseServiceInfoLine(tt.line)
		if !ok {
			t.Fatalf("line did not parse: %q", tt.line)
		}
		if item.State != tt.state || item.EnableState != tt.enableState || item.Running != tt.running || item.Enabled != tt.enabled {
			t.Fatalf("parse %q = state=%q enable=%q running=%v enabled=%v", tt.line, item.State, item.EnableState, item.Running, item.Enabled)
		}
	}
}

func TestContainerConfigContentIncludesUserNamespacesAndBoot(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	content := srv.containerConfigContent("demo", "demo", filepath.Join(workspace, "Containers", "demo", "rootfs"), "host", createContainerRequest{
		AllowUserNS:       true,
		RunAtBoot:         true,
		RunAtBootPriority: 3,
	})
	for _, want := range []string{"allow_userns=1", "run_at_boot=1", "run_at_boot_priority=3"} {
		if !strings.Contains(content, want) {
			t.Fatalf("generated config missing %q:\n%s", want, content)
		}
	}
}

func TestUpdateContainerConfigWritesUserNamespacesAndBoot(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	configPath := filepath.Join(workspace, "Containers", "demo", "container.config")
	mustWriteFile(t, configPath, []byte("name=demo\nnet_mode=host\n"), 0644)

	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPatch, "/api/containers/demo/config?token=secret", strings.NewReader(`{"allowUserns":true,"runAtBoot":true}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	text := string(mustReadFile(t, configPath))
	for _, want := range []string{"allow_userns=1", "run_at_boot=1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("updated config missing %q:\n%s", want, text)
		}
	}
}

func TestBootPriorityEndpointOrdersEveryEnabledContainer(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	alphaPath := filepath.Join(workspace, "Containers", "alpha", "container.config")
	betaPath := filepath.Join(workspace, "Containers", "beta", "container.config")
	stoppedPath := filepath.Join(workspace, "Containers", "stopped", "container.config")
	mustWriteFile(t, alphaPath, []byte("name=alpha\nnet_mode=host\nrun_at_boot=1\nrun_at_boot_priority=2\n"), 0644)
	mustWriteFile(t, betaPath, []byte("name=beta\nnet_mode=host\nrun_at_boot=1\nrun_at_boot_priority=1\n"), 0644)
	mustWriteFile(t, stoppedPath, []byte("name=stopped\nnet_mode=host\nrun_at_boot=0\n"), 0644)

	handler := srv.Handler()
	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/boot-priority?token=secret", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	var listed struct {
		Containers []containerView `json:"containers"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Containers) != 2 || listed.Containers[0].Name != "beta" || listed.Containers[1].Name != "alpha" {
		t.Fatalf("unexpected initial boot order: %#v", listed.Containers)
	}

	update := httptest.NewRecorder()
	handler.ServeHTTP(update, httptest.NewRequest(http.MethodPut, "/api/boot-priority?token=secret", strings.NewReader(`{"names":["alpha","beta"]}`)))
	if update.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", update.Code, update.Body.String())
	}
	if text := string(mustReadFile(t, alphaPath)); !strings.Contains(text, "run_at_boot_priority=1") {
		t.Fatalf("alpha priority not updated:\n%s", text)
	}
	if text := string(mustReadFile(t, betaPath)); !strings.Contains(text, "run_at_boot_priority=2") {
		t.Fatalf("beta priority not updated:\n%s", text)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodPut, "/api/boot-priority?token=secret", strings.NewReader(`{"names":["alpha"]}`)))
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing container status=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestParseSystemdUnitInspection(t *testing.T) {
	inspection := parseSystemdUnitInspection("ssh.service", "Description=OpenSSH server\nActiveState=active\n\n__DS_WEBUI_STATUS__\nactive (running)\n\n__DS_WEBUI_DEPS__\nssh.service\nnetwork.target\n", "__DS_WEBUI_STATUS__", "__DS_WEBUI_DEPS__")
	if inspection.Unit != "ssh.service" || inspection.Properties["Description"] != "OpenSSH server" || inspection.Properties["ActiveState"] != "active" {
		t.Fatalf("unexpected inspection properties: %#v", inspection)
	}
	if inspection.StatusText != "active (running)" || len(inspection.Dependencies) != 2 || inspection.Dependencies[1] != "network.target" {
		t.Fatalf("unexpected inspection output: %#v", inspection)
	}
}

func TestSystemdOverrideUsesEncodedContentAndRejectsUnsafeUnit(t *testing.T) {
	srv, workspace, _ := newTestServer(t)
	content := "[Service]\nEnvironment=\"A=B\"\n"
	res := httptest.NewRecorder()
	srv.Handler().ServeHTTP(res, httptest.NewRequest(http.MethodPut, "/api/containers/demo/services/systemd/ssh.service/override?token=secret", strings.NewReader(`{"content":"[Service]\nEnvironment=\"A=B\"\n"}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("override status=%d body=%s", res.Code, res.Body.String())
	}
	calls := string(mustReadFile(t, filepath.Join(workspace, "droidspaces-calls.log")))
	if strings.Contains(calls, content) || !strings.Contains(calls, base64.StdEncoding.EncodeToString([]byte(content))) {
		t.Fatalf("override content was not encoded in command:\n%s", calls)
	}

	unsafe := httptest.NewRecorder()
	srv.Handler().ServeHTTP(unsafe, httptest.NewRequest(http.MethodPost, "/api/containers/demo/services/systemd/ssh.service%3Becho/stop?token=secret", nil))
	if unsafe.Code != http.StatusBadRequest {
		t.Fatalf("unsafe unit status=%d body=%s", unsafe.Code, unsafe.Body.String())
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
