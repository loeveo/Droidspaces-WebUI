package rootfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

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
	if asset.UniqueFilename == "" || filepath.Ext(asset.UniqueFilename) != ".xz" {
		t.Fatalf("unexpected filename: %s", asset.UniqueFilename)
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
