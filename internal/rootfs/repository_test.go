package rootfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestParseFiltersArchAndBuildsFilename(t *testing.T) {
	data := []byte(`[
	  {"name":"Ubuntu XFCE","description":"desktop","architecture":"aarch64","download_url":"https://example.test/rootfs.tar.xz","size_bytes":42,"build_date":"2026-06-01","author":"Droidspaces"},
	  {"name":"Alpine","architecture":"x86_64","download_url":"https://example.test/alpine.tar.gz"}
	]`)
	assets, err := Parse(data, "official", "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("len = %d", len(assets))
	}
	asset := assets[0]
	if asset.Name != "Ubuntu XFCE" || asset.Architecture != "aarch64" {
		t.Fatalf("unexpected asset: %#v", asset)
	}
	if asset.Description != "desktop" {
		t.Fatalf("description = %q", asset.Description)
	}
	if asset.UniqueFilename == "" || filepath.Ext(asset.UniqueFilename) != ".xz" {
		t.Fatalf("unexpected filename: %s", asset.UniqueFilename)
	}
}

func TestParseLinuxContainersSelectsLatestRootfsAndMapsArchitecture(t *testing.T) {
	imageBase, err := url.Parse("https://images.linuxcontainers.org/")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
  "products": {
    "ubuntu:noble:arm64:default": {
      "aliases": "ubuntu/noble/default,ubuntu/24.04/default",
      "arch": "arm64",
      "os": "Ubuntu",
      "release": "noble",
      "release_title": "24.04 LTS",
      "variant": "default",
      "versions": {
        "20260101_01:01": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"images/ubuntu/noble/arm64/default/20260101_01:01/rootfs.tar.xz","size":10,"sha256":"old"}}},
        "20260202_02:02": {"items": {"incus.tar.xz": {"ftype":"incus.tar.xz","path":"images/ubuntu/noble/arm64/default/20260202_02:02/incus.tar.xz","size":1},"root.tar.xz": {"ftype":"root.tar.xz","path":"images/ubuntu/noble/arm64/default/20260202_02:02/rootfs.tar.xz","size":20,"sha256":"latest"}}}
      }
    },
    "alpine:edge:amd64:default": {
      "arch": "amd64",
      "os": "Alpine",
      "release": "edge",
      "versions": {"20260202_02:02": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"images/alpine/edge/amd64/default/20260202_02:02/rootfs.tar.xz","size":30}}}}
    },
    "invalid:arm64:default": {
      "arch": "arm64",
      "os": "Invalid",
      "versions": {"20260202_02:02": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"../outside.tar.xz","size":30}}}}
    }
  }
}`)

	assets, err := ParseLinuxContainers(data, "Linux Containers", "aarch64", imageBase)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 1 {
		t.Fatalf("assets = %#v, want one arm64 rootfs", assets)
	}
	asset := assets[0]
	if asset.Name != "Ubuntu 24.04 LTS (noble)" || asset.Architecture != "aarch64" || asset.BuildDate != "20260202_02:02" {
		t.Fatalf("unexpected asset metadata: %#v", asset)
	}
	if asset.DownloadURL != "https://images.linuxcontainers.org/images/ubuntu/noble/arm64/default/20260202_02:02/rootfs.tar.xz" {
		t.Fatalf("download URL = %q", asset.DownloadURL)
	}
	if asset.SizeBytes != 20 || asset.SHA256 != "latest" || asset.Variant != "default" || asset.Author != "Linux Containers" || asset.SourceRepoName != "Linux Containers" {
		t.Fatalf("unexpected asset source metadata: %#v", asset)
	}
	if !strings.Contains(asset.Description, "default") || !strings.Contains(asset.Description, "ubuntu/noble/default") {
		t.Fatalf("description = %q", asset.Description)
	}
}

func TestLinuxContainersReleaseTitleFormatsKnownCodenames(t *testing.T) {
	tests := []struct {
		name    string
		product linuxContainersProduct
		want    string
	}{
		{
			name:    "trixie",
			product: linuxContainersProduct{OS: "Debian", Release: "trixie", ReleaseTitle: "Trixie"},
			want:    "13 (trixie)",
		},
		{
			name:    "bookworm",
			product: linuxContainersProduct{OS: "Debian", Release: "bookworm"},
			want:    "12 (bookworm)",
		},
		{
			name:    "bullseye",
			product: linuxContainersProduct{OS: "Debian", Release: "bullseye"},
			want:    "11 (bullseye)",
		},
		{
			name:    "buster",
			product: linuxContainersProduct{OS: "Debian", Release: "buster"},
			want:    "10 (buster)",
		},
		{
			name:    "ubuntu noble",
			product: linuxContainersProduct{OS: "Ubuntu", Release: "noble"},
			want:    "24.04 (noble)",
		},
		{
			name:    "devuan excalibur",
			product: linuxContainersProduct{OS: "Devuan", Release: "excalibur"},
			want:    "6 (excalibur)",
		},
		{
			name:    "debian forky",
			product: linuxContainersProduct{OS: "Debian", Release: "forky", ReleaseTitle: "14"},
			want:    "14 (forky)",
		},
		{
			name:    "linux mint zena",
			product: linuxContainersProduct{OS: "Mint", Release: "zena"},
			want:    "22.3 (zena)",
		},
		{
			name:    "centos stream",
			product: linuxContainersProduct{OS: "Centos", Release: "10-Stream"},
			want:    "Stream 10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := linuxContainersReleaseTitle(tt.product); got != tt.want {
				t.Fatalf("linuxContainersReleaseTitle(%#v) = %q, want %q", tt.product, got, tt.want)
			}
		})
	}
}

func TestLinuxContainersDisplayNameNormalizesDistributionNames(t *testing.T) {
	tests := []struct {
		product linuxContainersProduct
		want    string
	}{
		{linuxContainersProduct{OS: "Almalinux", Release: "10"}, "AlmaLinux 10"},
		{linuxContainersProduct{OS: "Amazonlinux", Release: "2"}, "Amazon Linux 2"},
		{linuxContainersProduct{OS: "Archlinux", Release: "current"}, "Arch Linux Current"},
		{linuxContainersProduct{OS: "Centos", Release: "9-Stream"}, "CentOS Stream 9"},
		{linuxContainersProduct{OS: "Kali", Release: "current"}, "Kali Linux Current"},
		{linuxContainersProduct{OS: "Mint", Release: "zena"}, "Linux Mint 22.3 (zena)"},
		{linuxContainersProduct{OS: "Nixos", Release: "26.05"}, "NixOS 26.05"},
		{linuxContainersProduct{OS: "Openeuler", Release: "24.03"}, "openEuler 24.03"},
		{linuxContainersProduct{OS: "Opensuse", Release: "tumbleweed"}, "openSUSE Tumbleweed"},
		{linuxContainersProduct{OS: "Openwrt", Release: "24.10"}, "OpenWrt 24.10"},
		{linuxContainersProduct{OS: "Oracle", Release: "10"}, "Oracle Linux 10"},
		{linuxContainersProduct{OS: "Plamo", Release: "8.x"}, "Plamo 8.x"},
		{linuxContainersProduct{OS: "Rockylinux", Release: "9"}, "Rocky Linux 9"},
		{linuxContainersProduct{OS: "Voidlinux", Release: "current"}, "Void Linux Current"},
	}
	for _, tt := range tests {
		if got := linuxContainersDisplayName(tt.product); got != tt.want {
			t.Fatalf("linuxContainersDisplayName(%#v) = %q, want %q", tt.product, got, tt.want)
		}
	}
}

func TestFetchLinuxContainersCatalogEndpoint(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		_, _ = w.Write([]byte(`{
  "products": {
    "debian:bookworm:arm64:default": {
      "arch": "arm64", "os": "Debian", "release": "bookworm", "variant": "default",
      "versions": {"20260202_02:02": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"images/debian/bookworm/arm64/default/20260202_02:02/rootfs.tar.xz","size":20}}}}
    }
  }
}`))
	}))
	defer server.Close()

	assets, err := NewClient().Fetch(context.Background(), config.RootfsRepository{Name: "Linux Containers mirror", URL: server.URL + "/streams/v1/images.json"}, "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if requestedPath != "/streams/v1/images.json" {
		t.Fatalf("catalog path = %q", requestedPath)
	}
	if len(assets) != 1 || assets[0].DownloadURL != server.URL+"/images/debian/bookworm/arm64/default/20260202_02:02/rootfs.tar.xz" {
		t.Fatalf("unexpected mirror assets: %#v", assets)
	}
	if assets[0].Name != "Debian 12 (bookworm)" || assets[0].Variant != "default" {
		t.Fatalf("unexpected Debian display metadata: %#v", assets[0])
	}
}

func TestFetchLinuxContainersNJUMirrorUsesOfficialCatalogAndMirrorDownloads(t *testing.T) {
	var requested []string
	client := NewClient()
	client.HTTPClient.Transport = roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		requested = append(requested, request.URL.String())
		switch request.URL.String() {
		case config.LinuxContainersRepositoryURL + "streams/v1/images.json":
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{
  "products": {
    "debian:bookworm:arm64:default": {
      "arch": "arm64", "os": "Debian", "release": "bookworm", "variant": "default",
      "versions": {
        "20260201_02:02": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"images/debian/bookworm/arm64/default/20260201_02:02/rootfs.tar.xz","size":19}}},
        "20260202_02:02": {"items": {"root.tar.xz": {"ftype":"root.tar.xz","path":"images/debian/bookworm/arm64/default/20260202_02:02/rootfs.tar.xz","size":20}}}
      }
    }
  }
}`)),
				Request: request,
			}, nil
		case config.LinuxContainersNJURepositoryURL:
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`<!doctype html><table><tr><td>debian</td><td>bookworm</td><td>arm64</td><td>default</td><td><a href="/images/debian/bookworm/arm64/default/20260201_02:02/">20260201_02:02</a></td></tr></table>`)),
				Request:    request,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
	})

	assets, err := client.Fetch(context.Background(), config.RootfsRepository{Name: config.LinuxContainersNJURepositoryName, URL: config.LinuxContainersNJURepositoryURL}, "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if len(requested) != 2 || requested[0] != config.LinuxContainersRepositoryURL+"streams/v1/images.json" || requested[1] != config.LinuxContainersNJURepositoryURL {
		t.Fatalf("catalog requests = %#v", requested)
	}
	if len(assets) != 1 || assets[0].DownloadURL != config.LinuxContainersNJURepositoryURL+"images/debian/bookworm/arm64/default/20260201_02:02/rootfs.tar.xz" {
		t.Fatalf("unexpected NJU mirror assets: %#v", assets)
	}
}

func TestParseLinuxContainersImageIndex(t *testing.T) {
	paths := parseLinuxContainersImageIndex([]byte(`
<tr><td>debian</td><td>bookworm</td><td>arm64</td><td>cloud</td><td><a href="/images/debian/bookworm/arm64/cloud/20260202_02%3A02/">build</a></td></tr>
<tr><td>ignore</td></tr>`))
	if !paths["images/debian/bookworm/arm64/cloud/20260202_02:02/rootfs.tar.xz"] {
		t.Fatalf("parsed mirror paths = %#v", paths)
	}
}

func TestIsLinuxContainersNJUMirrorRequiresExactOriginAndPath(t *testing.T) {
	for rawURL, want := range map[string]bool{
		"https://mirror.nju.edu.cn/lxc-images/":               true,
		"https://mirror.nju.edu.cn/lxc-images":                true,
		"https://mirror.nju.edu.cn/lxc-images/?catalog=other": false,
		"https://mirror.nju.edu.cn/lxc-images-extra/":         false,
		"https://mirror.nju.edu.cn/other/lxc-images/":         false,
		"https://mirror.nju.edu.cn.evil.example/lxc-images/":  false,
	} {
		if got := isLinuxContainersNJUMirror(rawURL); got != want {
			t.Errorf("isLinuxContainersNJUMirror(%q) = %v, want %v", rawURL, got, want)
		}
	}
}

func TestDownloadWritesTemplateFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("rootfs-data"))
	}))
	defer server.Close()

	dir := t.TempDir()
	client := NewClient()
	asset := Asset{Name: "Test", Architecture: "aarch64", DownloadURL: server.URL + "/rootfs.tar.xz", Author: "tester", BuildDate: "2026-06-01"}
	asset.UniqueFilename = UniqueFilename(asset)

	path, err := client.Download(context.Background(), asset, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "rootfs-data" {
		t.Fatalf("unexpected data: %q", string(data))
	}
}

func TestDownloadResumesInterruptedResponse(t *testing.T) {
	payload := bytes.Repeat([]byte("rootfs-data-"), 16*1024)
	cut := len(payload) / 3

	var mu sync.Mutex
	var ranges []string
	var encodings []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		encodings = append(encodings, r.Header.Get("Accept-Encoding"))
		mu.Unlock()

		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(payload[:cut])
			if flush, ok := w.(http.Flusher); ok {
				flush.Flush()
			}
			return
		}

		wantRange := fmt.Sprintf("bytes=%d-", cut)
		if got := r.Header.Get("Range"); got != wantRange {
			http.Error(w, "unexpected range "+got, http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(payload)-cut))
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[cut:])
	}))
	defer server.Close()

	dir := t.TempDir()
	client := NewClient()
	if client.HTTPClient.Timeout != 0 {
		t.Fatalf("unexpected global HTTP timeout: %s", client.HTTPClient.Timeout)
	}
	asset := Asset{
		Name:         "Interrupted",
		Architecture: "aarch64",
		DownloadURL:  server.URL + "/rootfs.tar.xz",
		SizeBytes:    int64(len(payload)),
		Author:       "tester",
		BuildDate:    "2026-06-01",
	}
	asset.UniqueFilename = UniqueFilename(asset)

	var logs []string
	path, err := client.DownloadWithProgressAndLog(context.Background(), asset, dir, nil, func(message string) {
		logs = append(logs, message)
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, payload) {
		t.Fatalf("download contents do not match: got %d bytes, want %d", len(data), len(payload))
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial file remains after successful resume: %v", err)
	}

	mu.Lock()
	gotRanges := append([]string(nil), ranges...)
	gotEncodings := append([]string(nil), encodings...)
	mu.Unlock()
	if len(gotRanges) < 2 {
		t.Fatalf("requests = %d, want retry with a range request", len(gotRanges))
	}
	if gotRanges[0] != "" || gotRanges[1] != fmt.Sprintf("bytes=%d-", cut) {
		t.Fatalf("unexpected ranges: %#v", gotRanges)
	}
	for _, encoding := range gotEncodings {
		if encoding != "identity" {
			t.Fatalf("Accept-Encoding = %q, want identity", encoding)
		}
	}
	joinedLogs := strings.Join(logs, "\n")
	if !strings.Contains(joinedLogs, "retrying") || !strings.Contains(joinedLogs, "Resuming download") {
		t.Fatalf("missing retry/resume logs: %s", joinedLogs)
	}
}
