package web

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
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
	srv, err := NewServer(Options{
		DroidspacesPath:   filepath.Join(corePath, "droidspaces"),
		Workspace:         workspace,
		CorePath:          corePath,
		ImageRoot:         filepath.Join(workspace, "images"),
		TemplateImageRoot: templateRoot,
		AuthToken:         "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	return srv, workspace, templateRoot
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

func TestLocalRootfsListAndDownload(t *testing.T) {
	srv, _, templateRoot := newTestServer(t)
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
	body := strings.NewReader(`{"name":"Alpine","architecture":"aarch64","downloadUrl":"` + assetServer.URL + `/alpine.tar.gz","sizeBytes":14,"buildDate":"2026-06-28","author":"Droidspaces"}`)
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
	if !strings.HasPrefix(task.Path, templateRoot) {
		t.Fatalf("task path %q outside template root %q", task.Path, templateRoot)
	}
	data, err := os.ReadFile(task.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(assetBytes) {
		t.Fatalf("downloaded data = %q", string(data))
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
	copiedDir, err := srv.prepareRootfsForContainer(ctx, "local", dirTemplate, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(copiedDir, "etc", "issue"), "dir-template")
	if copiedDir == dirTemplate {
		t.Fatal("local directory template was not copied into container")
	}

	imgTemplate := filepath.Join(templateRoot, "rootfs.img")
	mustWriteFile(t, imgTemplate, []byte("img-template"), 0644)
	copiedImg, err := srv.prepareRootfsForContainer(ctx, "local", imgTemplate, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, copiedImg, "img-template")
	if filepath.Base(copiedImg) != "rootfs.img" {
		t.Fatalf("copied image path = %s", copiedImg)
	}

	archive := filepath.Join(templateRoot, "rootfs.tar.gz")
	writeTarGz(t, archive, map[string]string{"etc/os-release": "NAME=test\n"})
	extracted, err := srv.prepareRootfsForContainer(ctx, "local", archive, containerDir)
	if err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(extracted, "etc", "os-release"), "NAME=test\n")
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
