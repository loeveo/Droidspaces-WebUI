package web

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

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
	installFakeDroidspaces(t, corePath)
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
if [ "$1" = "pid" ]; then
  exit 1
fi
printf 'fake droidspaces %s\n' "$*"
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
	if res.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", res.Code, res.Body.String())
	}
	var created struct {
		Name         string `json:"name"`
		ConfigPath   string `json:"configPath"`
		ContainerDir string `json:"containerDir"`
		StartOutput  string `json:"startOutput"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "Ubuntu 24.04" {
		t.Fatalf("created name = %q", created.Name)
	}
	if filepath.Base(created.ContainerDir) != "Ubuntu-24.04" {
		t.Fatalf("container dir = %q", created.ContainerDir)
	}
	assertFile(t, filepath.Join(created.ContainerDir, "rootfs", "etc", "issue"), "Ubuntu template")
	assertFile(t, filepath.Join(created.ContainerDir, ".env"), "FOO=bar\nBAZ=qux\n")

	configData, err := os.ReadFile(created.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	configText := string(configData)
	for _, want := range []string{
		"name=Ubuntu 24.04",
		"hostname=ubuntu-web",
		"rootfs_path=" + filepath.Join(created.ContainerDir, "rootfs"),
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
	if !strings.Contains(calls, "--config "+created.ConfigPath+" start") {
		t.Fatalf("start command not recorded in calls %q", calls)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/containers/Ubuntu-24.04?token=secret", nil)
	deleteRes := httptest.NewRecorder()
	srv.Handler().ServeHTTP(deleteRes, deleteReq)
	if deleteRes.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteRes.Code, deleteRes.Body.String())
	}
	if _, err := os.Stat(created.ContainerDir); !os.IsNotExist(err) {
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
