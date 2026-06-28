package web

import (
	"bytes"
	"context"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/rootfs"
	"github.com/ravindu644/droidspaces-oss/webui/internal/socketd"
	"github.com/ravindu644/droidspaces-oss/webui/internal/workspace"
)

//go:embed static/*
var staticFiles embed.FS

//go:embed scripts/post_extract_fixes.sh
var postExtractFixesScript string

type Server struct {
	socketd             *socketd.Client
	socketdEnabled      bool
	droidspacesPath     string
	authToken           string
	workspace           string
	configPath          string
	mode                string
	corePath            string
	imageRoot           string
	templateImageRoot   string
	rootfsRepos         []config.RootfsRepository
	rootfsClient        *rootfs.Client
	rootfsSkipTLSVerify bool
	tasksMu             sync.RWMutex
	tasks               map[string]*taskState
}

type Options struct {
	DroidspacesPath     string
	AuthToken           string
	Workspace           string
	ConfigPath          string
	Mode                string
	CorePath            string
	ImageRoot           string
	TemplateImageRoot   string
	RootfsRepos         []config.RootfsRepository
	RootfsSkipTLSVerify bool
	SocketdEnabled      bool
}

type apiError struct {
	Error string `json:"error"`
}

type inspectResponse struct {
	socketd.Inspect
	Source       string            `json:"source,omitempty"`
	BackendError string            `json:"backendError,omitempty"`
	CLIInfo      map[string]string `json:"cliInfo,omitempty"`
	RawOutput    string            `json:"rawOutput,omitempty"`
}

type createContainerRequest struct {
	Name              string `json:"name"`
	Hostname          string `json:"hostname"`
	RootFSPath        string `json:"rootfsPath"`
	RootFSSource      string `json:"rootfsSource"`
	RootFSTaskID      string `json:"rootfsTaskId"`
	NetMode           string `json:"netMode"`
	DNSServers        string `json:"dnsServers"`
	PortForwards      string `json:"portForwards"`
	BindMounts        string `json:"bindMounts"`
	Env               string `json:"env"`
	CustomInit        string `json:"customInit"`
	DisableIPv6       bool   `json:"disableIPv6"`
	AndroidStorage    bool   `json:"androidStorage"`
	HWAccess          bool   `json:"hwAccess"`
	GPUMode           bool   `json:"gpuMode"`
	TermuxX11         bool   `json:"termuxX11"`
	VirGL             bool   `json:"virgl"`
	PulseAudio        bool   `json:"pulseAudio"`
	SELinuxPermissive bool   `json:"selinuxPermissive"`
	VolatileMode      bool   `json:"volatileMode"`
	ForceCgroupV1     bool   `json:"forceCgroupV1"`
	BlockNestedNS     bool   `json:"blockNestedNamespaces"`
	Start             bool   `json:"start"`
}

type execContainerRequest struct {
	Command string `json:"command"`
	User    string `json:"user"`
}

type cliCommandResult struct {
	Args     []string `json:"args"`
	ExitCode int      `json:"exitCode"`
	Output   string   `json:"output"`
}

type taskState struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
	Percent    int    `json:"percent"`
	Path       string `json:"path,omitempty"`
	URL        string `json:"url,omitempty"`
	Error      string `json:"error,omitempty"`
	StartedAt  int64  `json:"startedAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type localRootfsItem struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

func NewServer(opts Options) (*Server, error) {
	path := opts.DroidspacesPath
	if path == "" {
		path = "../output/droidspaces"
	}

	workspace := opts.Workspace
	if workspace == "" {
		workspace = "/var/lib/Droidspaces"
	}

	return &Server{
		socketd:             socketd.NewClient(6 * time.Second),
		socketdEnabled:      opts.SocketdEnabled,
		droidspacesPath:     path,
		authToken:           opts.AuthToken,
		workspace:           workspace,
		configPath:          opts.ConfigPath,
		mode:                opts.Mode,
		corePath:            opts.CorePath,
		imageRoot:           opts.ImageRoot,
		templateImageRoot:   opts.TemplateImageRoot,
		rootfsRepos:         opts.RootfsRepos,
		rootfsClient:        rootfs.NewClient(opts.RootfsSkipTLSVerify),
		rootfsSkipTLSVerify: opts.RootfsSkipTLSVerify,
		tasks:               map[string]*taskState{},
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("/api/containers", s.withAuth(s.handleContainers))
	mux.HandleFunc("/api/containers/", s.withAuth(s.handleContainer))
	mux.HandleFunc("/api/events", s.withAuth(s.handleEvents))
	mux.HandleFunc("/api/rootfs", s.withAuth(s.handleRootfsList))
	mux.HandleFunc("/api/rootfs/local", s.withAuth(s.handleLocalRootfsList))
	mux.HandleFunc("/api/rootfs/local/download", s.withAuth(s.handleLocalRootfsDownload))
	mux.HandleFunc("/api/rootfs/download", s.withAuth(s.handleRootfsDownload))
	mux.HandleFunc("/api/tasks/", s.withAuth(s.handleTask))
	mux.HandleFunc("/api/downloads/", s.withAuth(s.handleDownload))
	mux.HandleFunc("/api/cli", s.withAuth(s.handleCLI))

	sub, _ := fs.Sub(staticFiles, "static")
	fileServer := http.FileServer(http.FS(sub))
	mux.Handle("/", s.injectIndexConfig(fileServer))

	return securityHeaders(mux)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	if s.authToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authEnabled": false})
		return
	}
	got := s.requestToken(r)
	if got != s.authToken {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing or invalid auth token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authEnabled": true})
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.authToken != "" {
			got := s.requestToken(r)
			if got != s.authToken {
				writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing or invalid auth token"})
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) requestToken(r *http.Request) string {
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != "" {
		return got
	}
	return r.URL.Query().Get("token")
}

func (s *Server) injectIndexConfig(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			next.ServeHTTP(w, r)
			return
		}

		sub, _ := fs.Sub(staticFiles, "static")
		index, err := fs.ReadFile(sub, "index.html")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		configScript := fmt.Sprintf(`<script>window.DS_AUTH_REQUIRED = %t;</script>`, s.authToken != "")
		index = bytes.Replace(index, []byte("</head>"), []byte(configScript+"</head>"), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(index)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	status := map[string]any{
		"backend":             "unreachable",
		"mode":                s.mode,
		"configPath":          s.configPath,
		"droidspacesPath":     s.droidspacesPath,
		"corePath":            s.corePath,
		"imageRoot":           s.imageRoot,
		"templateImageRoot":   s.templateImageRoot,
		"rootfsRepoCount":     len(s.rootfsRepos),
		"rootfsSkipTLSVerify": s.rootfsSkipTLSVerify,
		"socketdEnabled":      s.socketdEnabled,
		"workspace":           s.workspace,
		"authEnabled":         s.authToken != "",
		"listenHint":          "local 模式仅监听本机；public 模式请配置 authToken",
	}

	if !s.socketdEnabled {
		status["backend"] = "socketd-disabled"
		if snap, snapErr := workspace.ReadSnapshot(s.workspace, true); snapErr == nil {
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else {
			status["fallbackError"] = snapErr.Error()
		}
		writeJSON(w, http.StatusOK, status)
		return
	}

	if err := s.socketd.Ping(ctx); err != nil {
		status["backendError"] = err.Error()
		if snap, cliErr := s.cliSnapshot(ctx, true); cliErr == nil {
			status["backend"] = "cli-fallback"
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else if snap, snapErr := workspace.ReadSnapshot(s.workspace, true); snapErr == nil {
			status["backend"] = "workspace-fallback"
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else {
			status["fallbackError"] = snapErr.Error()
		}
		writeJSON(w, http.StatusOK, status)
		return
	}
	status["backend"] = "ready"

	if caps, err := s.socketd.Capabilities(ctx); err == nil {
		status["capabilities"] = caps
	}
	if info, err := s.socketd.Info(ctx); err == nil {
		status["info"] = info
	} else if snap, snapErr := workspace.ReadSnapshot(s.workspace, true); snapErr == nil {
		status["info"] = snap.Info
		status["fallbackSource"] = snap.Source
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleContainers(w http.ResponseWriter, r *http.Request) {
	caseMethod := r.Method
	switch caseMethod {
	case http.MethodGet:
		s.listContainers(w, r)
	case http.MethodPost:
		s.createContainer(w, r)
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	includeAll := r.URL.Query().Get("all") != "0"
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if !s.socketdEnabled {
		snap, snapErr := workspace.ReadSnapshot(s.workspace, includeAll)
		if snapErr != nil {
			writeBackendError(w, snapErr)
			return
		}
		writeJSON(w, http.StatusOK, containerListResponse(snap, nil))
		return
	}

	var socketdErr error
	containers, err := s.socketd.ListContainers(ctx, includeAll)
	if err == nil {
		containers = s.mergeWorkspaceContainers(containers, includeAll)
		writeJSON(w, http.StatusOK, map[string]any{"containers": containers, "source": "socketd"})
		return
	}
	socketdErr = err

	if snap, cliErr := s.cliSnapshot(ctx, includeAll); cliErr == nil {
		writeJSON(w, http.StatusOK, containerListResponse(snap, socketdErr))
		return
	}

	snap, snapErr := workspace.ReadSnapshot(s.workspace, includeAll)
	if snapErr != nil {
		writeBackendError(w, fallbackError(socketdErr, snapErr))
		return
	}
	writeJSON(w, http.StatusOK, containerListResponse(snap, socketdErr))
}

func containerListResponse(snap workspace.Snapshot, backendErr error) map[string]any {
	resp := map[string]any{"containers": snap.Containers, "source": snap.Source, "info": snap.Info}
	if backendErr != nil {
		resp["backendError"] = backendErr.Error()
	}
	return resp
}

func fallbackError(backendErr error, fallbackErr error) error {
	if backendErr == nil {
		return fallbackErr
	}
	return fmt.Errorf("socketd: %v; workspace fallback: %w", backendErr, fallbackErr)
}

func (s *Server) handleContainer(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/containers/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusNotFound, apiError{Error: "container not found"})
		return
	}

	target, err := cleanTarget(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			s.inspectContainer(w, r, target)
		case http.MethodDelete:
			s.deleteContainer(w, r, target)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		}
		return
	}

	if parts[1] == "shell" {
		s.shellContainer(w, r, target)
		return
	}

	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	switch parts[1] {
	case "start":
		s.lifecycle(w, r, target, "start")
	case "stop":
		s.lifecycle(w, r, target, "stop")
	case "restart":
		s.lifecycle(w, r, target, "restart")
	case "exec":
		s.execInContainer(w, r, target)
	case "export":
		s.exportContainer(w, r, target, false)
	case "template":
		s.exportContainer(w, r, target, true)
	default:
		writeJSON(w, http.StatusNotFound, apiError{Error: "unknown container action"})
	}
}

func (s *Server) inspectContainer(w http.ResponseWriter, r *http.Request, target string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	if !s.socketdEnabled {
		fallback, fallbackErr := workspace.Inspect(s.workspace, target)
		if fallbackErr != nil {
			writeBackendError(w, fallbackErr)
			return
		}
		writeJSON(w, http.StatusOK, inspectResponse{Inspect: fallback, Source: "workspace"})
		return
	}

	var socketdErr error
	inspect, err := s.socketd.InspectContainer(ctx, target)
	if err == nil {
		writeJSON(w, http.StatusOK, inspectResponse{Inspect: inspect, Source: "socketd"})
		return
	}
	socketdErr = err
	if fallback, cliErr := s.inspectViaCLI(ctx, target); cliErr == nil {
		if socketdErr != nil {
			fallback.BackendError = socketdErr.Error()
		}
		writeJSON(w, http.StatusOK, fallback)
		return
	}
	fallback, fallbackErr := workspace.Inspect(s.workspace, target)
	if fallbackErr != nil {
		writeBackendError(w, fallbackError(socketdErr, fallbackErr))
		return
	}
	resp := inspectResponse{Inspect: fallback, Source: "workspace"}
	if socketdErr != nil {
		resp.BackendError = socketdErr.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) lifecycle(w http.ResponseWriter, r *http.Request, target string, action string) {
	timeoutSeconds := 15
	if raw := r.URL.Query().Get("timeout"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 300 {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "timeout must be between 0 and 300 seconds"})
			return
		}
		timeoutSeconds = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds+40)*time.Second)
	defer cancel()

	var err error
	if s.socketdEnabled {
		switch action {
		case "start":
			err = s.socketd.StartContainer(ctx, target)
		case "stop":
			err = s.socketd.StopContainer(ctx, target, timeoutSeconds)
		case "restart":
			err = s.socketd.RestartContainer(ctx, target, timeoutSeconds)
		}
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "target": target, "source": "socketd"})
			return
		}
	}

	result, cliErr := s.lifecycleViaCLI(ctx, target, action)
	if cliErr != nil {
		message := fmt.Sprintf("cli: %v\n%s", cliErr, result.Output)
		if err != nil {
			message = fmt.Sprintf("socketd: %v; %s", err, message)
		}
		writeJSON(w, http.StatusBadGateway, apiError{Error: message})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "target": target, "source": "cli", "output": result.Output})
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	var since int64
	if raw := r.URL.Query().Get("since"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "since must be a unix timestamp"})
			return
		}
		since = n
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()

	if !s.socketdEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"events": []socketd.Event{}, "source": "workspace", "backendError": "socketd disabled"})
		return
	}

	events, err := s.socketd.PollEvents(ctx, since)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"events": []socketd.Event{}, "backendError": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

type cliRequest struct {
	Command string `json:"command"`
}

type cliResponse struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exitCode"`
	Output   string `json:"output"`
}

var allowedCLICommands = map[string][]string{
	"check":       {"check"},
	"mode":        {"mode"},
	"scan":        {"scan"},
	"show":        {"show"},
	"show-format": {"--format", "show"},
	"version":     {"version"},
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

func (s *Server) handleRootfsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	arch := r.URL.Query().Get("arch")
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	assets, errors := s.rootfsClient.FetchAll(ctx, s.rootfsRepos, arch)
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":            assets,
		"errors":            errors,
		"templateImageRoot": s.templateImageRoot,
		"repositories":      s.rootfsRepos,
	})
}

func (s *Server) handleLocalRootfsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	items, err := s.localRootfsItems()
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "templateImageRoot": s.templateImageRoot})
}

func (s *Server) handleLocalRootfsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if path == "" || hasConfigUnsafeChars(path) || !filepath.IsAbs(path) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid path"})
		return
	}
	if !s.pathWithinManagedRoots(path) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "path is outside managed roots"})
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		writeJSON(w, http.StatusNotFound, apiError{Error: err.Error()})
		return
	}
	if info.IsDir() || (!isRootfsArchive(path) && !strings.HasSuffix(strings.ToLower(path), ".img")) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "only rootfs image or archive files can be downloaded directly"})
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filepath.Base(path)))
	http.ServeFile(w, r, path)
}

type rootfsDownloadRequest struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Architecture   string `json:"architecture"`
	DownloadURL    string `json:"downloadUrl"`
	SizeBytes      int64  `json:"sizeBytes"`
	BuildDate      string `json:"buildDate"`
	Author         string `json:"author"`
	SourceRepoName string `json:"sourceRepoName"`
	UniqueFilename string `json:"uniqueFilename"`
}

func (s *Server) handleRootfsDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	var req rootfsDownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()

	allowedAssets, fetchErrors := s.rootfsClient.FetchAll(ctx, s.rootfsRepos, req.Architecture)
	allowed := false
	for _, candidate := range allowedAssets {
		if candidate.DownloadURL == req.DownloadURL {
			allowed = true
			break
		}
	}
	if !allowed {
		message := "download URL is not present in configured rootfs repositories"
		if len(fetchErrors) > 0 {
			message += ": " + strings.Join(fetchErrors, "; ")
		}
		writeJSON(w, http.StatusBadRequest, apiError{Error: message})
		return
	}
	asset := rootfs.Asset{
		Name:           req.Name,
		Description:    req.Description,
		Architecture:   req.Architecture,
		DownloadURL:    req.DownloadURL,
		SizeBytes:      req.SizeBytes,
		BuildDate:      req.BuildDate,
		Author:         req.Author,
		SourceRepoName: req.SourceRepoName,
		UniqueFilename: req.UniqueFilename,
	}
	if asset.UniqueFilename == "" {
		asset.UniqueFilename = rootfs.UniqueFilename(asset)
	}

	task := s.newTask("rootfs-download", asset.Name)
	task.Total = asset.SizeBytes
	s.updateTask(task.ID, func(t *taskState) {
		t.Status = "running"
	})
	go func() {
		path, err := s.rootfsClient.DownloadWithProgress(context.Background(), asset, s.templateImageRoot, func(downloaded int64, total int64) {
			s.updateTask(task.ID, func(t *taskState) {
				t.Downloaded = downloaded
				t.Total = total
				if total > 0 {
					t.Percent = int(downloaded * 100 / total)
				}
			})
		})
		if err != nil {
			s.failTask(task.ID, err)
			return
		}
		s.completeTask(task.ID, path, "/api/rootfs/local/download?path="+url.QueryEscape(path))
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/tasks/")
	if id == "" || strings.ContainsAny(id, `/\\`) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid task id"})
		return
	}
	task, ok := s.getTask(id)
	if !ok {
		writeJSON(w, http.StatusNotFound, apiError{Error: "task not found"})
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/downloads/")
	if id == "" || strings.ContainsAny(id, `/\\`) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid download id"})
		return
	}
	task, ok := s.getTask(id)
	if !ok || task.Path == "" || task.Status != "done" {
		writeJSON(w, http.StatusNotFound, apiError{Error: "download not ready"})
		return
	}
	if !s.pathWithinManagedRoots(task.Path) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "download path is outside managed roots"})
		return
	}
	name := filepath.Base(task.Path)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	http.ServeFile(w, r, task.Path)
}

func (s *Server) exportContainer(w http.ResponseWriter, r *http.Request, target string, asTemplate bool) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	kind := "container-export"
	if asTemplate {
		kind = "container-template"
	}
	task := s.newTask(kind, target)
	s.updateTask(task.ID, func(t *taskState) { t.Status = "running" })
	go s.runExportTask(task.ID, target, asTemplate)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
}

func (s *Server) runExportTask(taskID string, target string, asTemplate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	if running, _ := s.containerRunning(ctx, target); running {
		result, err := s.lifecycleViaCLI(ctx, target, "stop")
		if err != nil {
			s.failTask(taskID, fmt.Errorf("failed to stop container before export: %v\n%s", err, result.Output))
			return
		}
	}
	inspect, err := s.inspectForExport(ctx, target)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	rootfsPath := strings.TrimSpace(inspect.RootFSPath)
	if rootfsPath == "" {
		s.failTask(taskID, fmt.Errorf("container rootfs path is empty"))
		return
	}
	if _, err := os.Stat(rootfsPath); err != nil {
		s.failTask(taskID, fmt.Errorf("rootfs is not accessible: %w", err))
		return
	}
	outDir := filepath.Join(s.templateImageRoot, "exports")
	if asTemplate {
		outDir = s.templateImageRoot
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		s.failTask(taskID, err)
		return
	}
	filename := sanitizeDownloadName(target)
	if asTemplate {
		filename += "-template"
	} else {
		filename += "-backup"
	}
	filename += "-" + time.Now().Format("20060102-150405") + ".tar.gz"
	dest := filepath.Join(outDir, filename)
	if err := s.createRootfsArchive(ctx, rootfsPath, dest, taskID); err != nil {
		_ = os.Remove(dest)
		s.failTask(taskID, err)
		return
	}
	url := "/api/downloads/" + taskID
	s.completeTask(taskID, dest, url)
}

func (s *Server) inspectForExport(ctx context.Context, target string) (socketd.Inspect, error) {
	if s.socketdEnabled {
		if item, err := s.socketd.InspectContainer(ctx, target); err == nil {
			return item, nil
		}
	}
	if item, err := workspace.Inspect(s.workspace, target); err == nil {
		return item, nil
	}
	item, err := s.inspectViaCLI(ctx, target)
	return item.Inspect, err
}

func (s *Server) handleCLI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	var req cliRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	args, ok := allowedCLICommands[req.Command]
	if !ok {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "unsupported command"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	result, _ := s.runDroidspaces(ctx, args...)
	writeJSON(w, http.StatusOK, cliResponse{
		Command:  req.Command,
		ExitCode: result.ExitCode,
		Output:   result.Output,
	})
}

func (s *Server) createContainer(w http.ResponseWriter, r *http.Request) {
	var req createContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}

	name, err := cleanTarget(strings.TrimSpace(req.Name))
	if err != nil || hasConfigUnsafeChars(name) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid container name"})
		return
	}
	netMode := strings.ToLower(strings.TrimSpace(req.NetMode))
	if netMode == "" {
		netMode = "host"
	}
	if netMode != "host" && netMode != "nat" && netMode != "none" && netMode != "gateway" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "netMode must be host, nat, none, or gateway"})
		return
	}
	for _, value := range []string{req.Hostname, req.DNSServers, req.PortForwards, req.BindMounts, req.CustomInit} {
		if hasConfigUnsafeChars(value) {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "config values must not contain newlines"})
			return
		}
	}

	containerDir, err := s.containerDir(name)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if _, err := os.Stat(containerDir); err == nil {
		writeJSON(w, http.StatusConflict, apiError{Error: "container already exists"})
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	cleanupContainerDir := true
	defer func() {
		if cleanupContainerDir {
			_ = os.RemoveAll(containerDir)
		}
	}()

	rootfsPath, err := s.resolveCreateRootfs(r.Context(), req, containerDir)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}

	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = sanitizeContainerName(name)
	}
	configPath := filepath.Join(containerDir, "container.config")
	content := s.containerConfigContent(name, hostname, rootfsPath, netMode, req)
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	if strings.TrimSpace(req.Env) != "" {
		if err := os.WriteFile(filepath.Join(containerDir, ".env"), []byte(strings.TrimRight(req.Env, "\r\n")+"\n"), 0644); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
	}

	cleanupContainerDir = false
	resp := map[string]any{"ok": true, "name": name, "configPath": configPath, "containerDir": containerDir}
	if req.Start {
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		result, startErr := s.runDroidspaces(ctx, "--config", configPath, "start")
		resp["startExitCode"] = result.ExitCode
		resp["startOutput"] = result.Output
		if startErr != nil {
			resp["startError"] = startErr.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) resolveCreateRootfs(ctx context.Context, req createContainerRequest, containerDir string) (string, error) {
	source := strings.TrimSpace(req.RootFSSource)
	if source == "" {
		source = "direct"
	}
	switch source {
	case "direct", "local":
		rootfsPath := strings.TrimSpace(req.RootFSPath)
		if rootfsPath == "" || hasConfigUnsafeChars(rootfsPath) || !filepath.IsAbs(rootfsPath) {
			return "", fmt.Errorf("rootfsPath must be an absolute path")
		}
		if source == "local" && !s.pathWithinManagedRoots(rootfsPath) {
			return "", fmt.Errorf("local template path is outside managed roots")
		}
		return s.prepareRootfsForContainer(ctx, source, rootfsPath, containerDir)
	case "cloud":
		taskID := strings.TrimSpace(req.RootFSTaskID)
		if taskID == "" {
			return "", fmt.Errorf("rootfsTaskId is required for cloud source")
		}
		task, ok := s.getTask(taskID)
		if !ok || task.Status != "done" || task.Path == "" {
			return "", fmt.Errorf("cloud rootfs download is not ready")
		}
		if !s.pathWithinManagedRoots(task.Path) {
			return "", fmt.Errorf("downloaded rootfs is outside managed roots")
		}
		return s.prepareRootfsForContainer(ctx, source, task.Path, containerDir)
	default:
		return "", fmt.Errorf("rootfsSource must be direct, local, or cloud")
	}
}

func (s *Server) deleteContainer(w http.ResponseWriter, r *http.Request, target string) {
	containerDir, err := s.containerDir(target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if _, err := os.Stat(containerDir); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, apiError{Error: "container not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if running, _ := s.containerRunning(ctx, target); running {
		if result, err := s.lifecycleViaCLI(ctx, target, "stop"); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("failed to stop container before delete: %v\n%s", err, result.Output)})
			return
		}
	}
	if err := os.RemoveAll(containerDir); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	s.removePidSidecars(target)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": target, "deleted": containerDir})
}

func (s *Server) execInContainer(w http.ResponseWriter, r *http.Request, target string) {
	var req execContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	command := strings.TrimSpace(req.Command)
	if command == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "command is required"})
		return
	}
	if len(command) > 4096 {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "command is too long"})
		return
	}
	user := strings.TrimSpace(req.User)
	args := []string{"--name", target}
	if user != "" {
		if hasConfigUnsafeChars(user) || strings.ContainsAny(user, " /\\\x00") {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid user"})
			return
		}
		args = append(args, "--user", user)
	}
	args = append(args, "run", "/bin/sh", "-lc", command)

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	result, _ := s.runDroidspaces(ctx, args...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": result.ExitCode == 0, "exitCode": result.ExitCode, "output": result.Output, "args": result.Args})
}

var shellUpgrader = websocket.Upgrader{
	ReadBufferSize:  8192,
	WriteBufferSize: 8192,
	CheckOrigin:     websocketOriginAllowed,
}

func websocketOriginAllowed(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}
	host := r.Host
	if host == "" {
		return false
	}
	if strings.EqualFold(parsed.Host, host) {
		return true
	}
	originHost, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		originHost = parsed.Host
	}
	requestHost, _, err := net.SplitHostPort(host)
	if err != nil {
		requestHost = host
	}
	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(host), "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Server) shellContainer(w http.ResponseWriter, r *http.Request, target string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if user == "" {
		user = "root"
	}
	if hasConfigUnsafeChars(user) || strings.ContainsAny(user, " /\\\x00") {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid user"})
		return
	}

	conn, err := shellUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	args := []string{"--name", target, "enter"}
	if user != "root" {
		args = append(args, user)
	}
	cmd := exec.CommandContext(r.Context(), s.droidspacesPath, args...)
	cmd.Env = s.terminalEnv()
	if s.workspace != "" {
		cmd.Dir = s.workspace
	}

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 32, Cols: 120})
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("failed to start droidspaces enter: %v\n", err)))
		return
	}

	var writeMu sync.Mutex
	writeWS := func(messageType int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteMessage(messageType, data)
	}

	var closeOnce sync.Once
	closeSession := func() {
		closeOnce.Do(func() {
			_ = ptyFile.Close()
			_ = conn.Close()
		})
	}
	defer closeSession()

	ptyDone := make(chan struct{})
	go func() {
		defer close(ptyDone)
		buf := make([]byte, 8192)
		for {
			n, readErr := ptyFile.Read(buf)
			if n > 0 {
				if err := writeWS(websocket.BinaryMessage, append([]byte(nil), buf[:n]...)); err != nil {
					return
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	wsDone := make(chan struct{})
	go func() {
		defer close(wsDone)
		for {
			messageType, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
				continue
			}
			if len(message) == 0 {
				continue
			}
			if _, err := ptyFile.Write(message); err != nil {
				return
			}
		}
	}()

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	waited := false
	select {
	case err := <-waitDone:
		waited = true
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			_ = writeWS(websocket.TextMessage, []byte(fmt.Sprintf("\r\n[droidspaces enter exited: %v]\r\n", err)))
		}
	case <-wsDone:
	case <-ptyDone:
	case <-r.Context().Done():
	}

	closeSession()
	if !waited {
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
			<-waitDone
		}
	}
	<-ptyDone
}

func (s *Server) terminalEnv() []string {
	env := append([]string{}, os.Environ()...)
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	if s.corePath != "" {
		pathValue := os.Getenv("PATH")
		if pathValue == "" {
			pathValue = s.corePath
		} else {
			pathValue = s.corePath + string(os.PathListSeparator) + pathValue
		}
		env = append(env, "PATH="+pathValue)
	}
	return env
}

func (s *Server) lifecycleViaCLI(ctx context.Context, target string, action string) (cliCommandResult, error) {
	var args []string
	switch action {
	case "start", "restart":
		if configPath, ok := s.containerConfigPath(target); ok {
			args = []string{"--config", configPath, action}
		} else {
			args = []string{"--name", target, action}
		}
	case "stop":
		args = []string{"--name", target, "stop"}
	default:
		return cliCommandResult{}, fmt.Errorf("unsupported lifecycle action %q", action)
	}
	return s.runDroidspaces(ctx, args...)
}

func (s *Server) cliSnapshot(ctx context.Context, includeAll bool) (workspace.Snapshot, error) {
	result, err := s.runDroidspaces(ctx, "--format", "show")
	if err != nil && result.Output == "" {
		return workspace.Snapshot{}, err
	}
	kv := parseKeyValueOutput(result.Output)
	running := map[string]int32{}
	for key, value := range kv {
		if !strings.HasPrefix(key, "CONT_") {
			continue
		}
		pid64, convErr := strconv.ParseInt(strings.TrimSpace(value), 10, 32)
		if convErr == nil && pid64 > 0 {
			running[strings.TrimPrefix(key, "CONT_")] = int32(pid64)
		}
	}

	snap, _ := workspace.ReadSnapshot(s.workspace, true)
	byName := map[string]int{}
	for i := range snap.Containers {
		snap.Containers[i].Running = false
		snap.Containers[i].PID = 0
		byName[snap.Containers[i].Name] = i
	}
	for name, pid := range running {
		if idx, ok := byName[name]; ok {
			snap.Containers[idx].Running = true
			snap.Containers[idx].PID = pid
			if snap.Containers[idx].NetMode == "" {
				snap.Containers[idx].NetMode = "unknown"
			}
		} else {
			snap.Containers = append(snap.Containers, socketd.Container{Name: name, PID: pid, Running: true, NetMode: "unknown"})
		}
	}
	if !includeAll {
		filtered := snap.Containers[:0]
		for _, container := range snap.Containers {
			if container.Running {
				filtered = append(filtered, container)
			}
		}
		snap.Containers = filtered
	}
	total := uint32(len(snap.Containers))
	if includeAll {
		if raw := kv["TOTAL_CONTAINERS"]; raw != "" {
			if n, convErr := strconv.ParseUint(raw, 10, 32); convErr == nil && uint32(n) > total {
				total = uint32(n)
			}
		}
	}
	runningCount := uint32(len(running))
	stopped := uint32(0)
	if total > runningCount {
		stopped = total - runningCount
	}
	snap.Info = socketd.Info{ContainersTotal: total, ContainersRunning: runningCount, ContainersStopped: stopped}
	snap.Source = "cli"
	return snap, nil
}

func (s *Server) mergeWorkspaceContainers(containers []socketd.Container, includeAll bool) []socketd.Container {
	snap, err := workspace.ReadSnapshot(s.workspace, includeAll)
	if err != nil {
		return containers
	}
	seen := map[string]bool{}
	for _, container := range containers {
		seen[container.Name] = true
	}
	for _, container := range snap.Containers {
		if seen[container.Name] {
			continue
		}
		containers = append(containers, container)
	}
	return containers
}

func (s *Server) inspectViaCLI(ctx context.Context, target string) (inspectResponse, error) {
	result, err := s.runDroidspaces(ctx, "--name", target, "--format", "info")
	if err != nil {
		return inspectResponse{}, err
	}
	kv := parseKeyValueOutput(result.Output)
	base := socketd.Inspect{}
	if fallback, fallbackErr := workspace.Inspect(s.workspace, target); fallbackErr == nil {
		base = fallback
	}
	if base.Name == "" {
		base.Name = valueOr(kv["CONTAINER_NAME"], target)
	}
	if pid, convErr := strconv.ParseInt(kv["CONTAINER_PID"], 10, 32); convErr == nil && pid > 0 {
		base.PID = int32(pid)
		base.Running = true
	}
	if v := kv["CONTAINER_HOSTNAME"]; v != "" {
		base.Hostname = v
	}
	if v := kv["NETWORKING_MODE"]; v != "" {
		base.NetMode = strings.ToLower(v)
	}
	if v := kv["NAT_IP"]; v != "" {
		base.NATIP = v
	}
	if v := kv["DNS_SERVERS"]; v != "" {
		base.DNSServers = v
	}
	base.DisableIPv6 = kvBool(kv["DISABLE_IPV6"])
	base.AndroidStorage = kvBool(kv["ANDROID_STORAGE"])
	base.VolatileMode = kvBool(kv["VOLATILE_MODE"])
	base.ForceCgroupV1 = kvBool(kv["FORCE_CGROUP_V1"])
	base.BlockNestedNS = kvBool(kv["DEADLOCK_SHIELD"])
	base.Foreground = kvBool(kv["FOREGROUND_MODE"])
	base.TermuxX11 = kvBool(kv["TERMUX_X11"])
	base.GPUMode = strings.EqualFold(kv["HW_ACCESS"], "GPU") || strings.EqualFold(kv["HW_ACCESS"], "full")
	base.HWAccess = strings.EqualFold(kv["HW_ACCESS"], "full")
	if ports := parsePortList(kv["PORT_FORWARDS"]); len(ports) > 0 {
		base.Ports = ports
		base.PortTotal = len(ports)
	}
	return inspectResponse{Inspect: base, Source: "cli", CLIInfo: kv, RawOutput: result.Output}, nil
}

func (s *Server) containerRunning(ctx context.Context, target string) (bool, int32) {
	if !s.socketdEnabled {
		if inspect, err := workspace.Inspect(s.workspace, target); err == nil && inspect.Running && inspect.PID > 0 {
			return true, inspect.PID
		}
		return false, 0
	}
	result, err := s.runDroidspaces(ctx, "--name", target, "pid")
	if err != nil {
		return false, 0
	}
	pid, convErr := strconv.ParseInt(strings.TrimSpace(result.Output), 10, 32)
	if convErr != nil || pid <= 0 {
		return false, 0
	}
	return true, int32(pid)
}

func (s *Server) newTask(kind string, name string) *taskState {
	id := newUUID()
	now := time.Now().Unix()
	task := &taskState{ID: id, Kind: kind, Name: name, Status: "pending", StartedAt: now, UpdatedAt: now}
	s.tasksMu.Lock()
	s.tasks[id] = task
	s.tasksMu.Unlock()
	return cloneTask(task)
}

func (s *Server) getTask(id string) (*taskState, bool) {
	s.tasksMu.RLock()
	defer s.tasksMu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, false
	}
	return cloneTask(task), true
}

func (s *Server) updateTask(id string, fn func(*taskState)) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	if task, ok := s.tasks[id]; ok {
		fn(task)
		task.UpdatedAt = time.Now().Unix()
	}
}

func (s *Server) failTask(id string, err error) {
	s.updateTask(id, func(task *taskState) {
		task.Status = "error"
		task.Error = err.Error()
	})
}

func (s *Server) completeTask(id string, path string, url string) {
	s.updateTask(id, func(task *taskState) {
		task.Status = "done"
		task.Path = path
		task.URL = url
		task.Percent = 100
		if path != "" {
			if info, err := os.Stat(path); err == nil {
				task.Downloaded = info.Size()
				if task.Total <= 0 {
					task.Total = info.Size()
				}
			}
		}
	})
}

func cloneTask(task *taskState) *taskState {
	if task == nil {
		return nil
	}
	copy := *task
	return &copy
}

func (s *Server) prepareRootfsForContainer(ctx context.Context, source string, rootfsPath string, containerDir string) (string, error) {
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return "", fmt.Errorf("rootfsPath is not accessible: %v", err)
	}
	lower := strings.ToLower(rootfsPath)
	if isRootfsArchive(lower) {
		dest := filepath.Join(containerDir, "rootfs")
		if err := s.extractRootfsArchive(ctx, rootfsPath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	if info.IsDir() {
		if source == "direct" {
			return rootfsPath, nil
		}
		dest := filepath.Join(containerDir, "rootfs")
		if err := s.copyRootfsDirectory(ctx, rootfsPath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	if strings.HasSuffix(lower, ".img") {
		if source == "direct" {
			return rootfsPath, nil
		}
		dest := filepath.Join(containerDir, "rootfs.img")
		if err := s.copyRootfsFile(ctx, rootfsPath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("rootfs template must be a directory, .img, .tar.gz, .tgz, or .tar.xz")
}

func (s *Server) localRootfsItems() ([]localRootfsItem, error) {
	roots := []string{s.templateImageRoot}
	if s.imageRoot != "" && s.imageRoot != s.templateImageRoot {
		roots = append(roots, s.imageRoot)
	}
	seen := map[string]bool{}
	items := make([]localRootfsItem, 0)
	for _, root := range roots {
		if root == "" {
			continue
		}
		rootItems, err := s.scanLocalRootfsDir(root, "", seen)
		if err != nil {
			return nil, err
		}
		items = append(items, rootItems...)
		exportItems, err := s.scanLocalRootfsDir(filepath.Join(root, "exports"), "backup", seen)
		if err != nil {
			return nil, err
		}
		items = append(items, exportItems...)
	}
	return items, nil
}

func (s *Server) scanLocalRootfsDir(root string, kindOverride string, seen map[string]bool) ([]localRootfsItem, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var items []localRootfsItem
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".part") || entry.Name() == "exports" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := "directory"
		if !entry.IsDir() {
			lower := strings.ToLower(entry.Name())
			switch {
			case strings.HasSuffix(lower, ".img"):
				kind = "image"
			case isRootfsArchive(lower):
				kind = "archive"
			default:
				continue
			}
		}
		if kindOverride != "" && !entry.IsDir() {
			kind = kindOverride
		}
		if seen[path] {
			continue
		}
		seen[path] = true
		items = append(items, localRootfsItem{Name: entry.Name(), Path: path, Kind: kind, Size: info.Size(), Modified: info.ModTime().Unix()})
	}
	return items, nil
}

func (s *Server) createRootfsArchive(ctx context.Context, rootfsPath string, dest string, taskID string) error {
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if info.IsDir() {
		return s.archiveDirectory(ctx, rootfsPath, dest, taskID)
	}
	if strings.HasSuffix(strings.ToLower(rootfsPath), ".img") {
		return s.archiveImage(ctx, rootfsPath, dest, taskID)
	}
	return fmt.Errorf("unsupported rootfs type: %s", rootfsPath)
}

func (s *Server) copyRootfsDirectory(ctx context.Context, source string, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	busybox := shellQuote(s.busyboxPath())
	cmdText := fmt.Sprintf("cd %s && %s tar -cpf - . | (cd %s && %s tar -xpf -)", shellQuote(source), busybox, shellQuote(dest), busybox)
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy rootfs template failed: %v\n%s", err, string(out))
	}
	return nil
}

func (s *Server) copyRootfsFile(ctx context.Context, source string, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	_ = os.Remove(dest)
	cmd := exec.CommandContext(ctx, s.busyboxPath(), "cp", "-f", source, dest)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("copy rootfs image failed: %v\n%s", err, string(out))
	}
	return nil
}

func (s *Server) extractRootfsArchive(ctx context.Context, archive string, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	busybox := shellQuote(s.busyboxPath())
	archiveArg := shellQuote(archive)
	destArg := shellQuote(dest)
	lower := strings.ToLower(archive)
	var cmdText string
	switch {
	case strings.HasSuffix(lower, ".tar.xz"):
		cmdText = fmt.Sprintf("cd %s && %s xzcat %s | %s tar -xpf -", destArg, busybox, archiveArg, busybox)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		cmdText = fmt.Sprintf("cd %s && %s tar -xzpf %s", destArg, busybox, archiveArg)
	default:
		return fmt.Errorf("unsupported rootfs archive: %s", archive)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("extract rootfs archive failed: %v\n%s", err, string(out))
	}
	_ = s.applyPostExtractionFixes(ctx, dest)
	return nil
}

func (s *Server) applyPostExtractionFixes(ctx context.Context, rootfsDir string) error {
	if strings.TrimSpace(postExtractFixesScript) == "" {
		return nil
	}
	tmpRoot := filepath.Join(s.workspace, ".webui-tmp")
	if s.workspace == "" {
		tmpRoot = os.TempDir()
	}
	if err := os.MkdirAll(tmpRoot, 0700); err != nil {
		return err
	}
	scriptPath := filepath.Join(tmpRoot, "post_extract_fixes-"+newUUID()+".sh")
	if err := os.WriteFile(scriptPath, []byte(postExtractFixesScript), 0755); err != nil {
		return err
	}
	defer os.Remove(scriptPath)
	cmd := exec.CommandContext(ctx, "sh", scriptPath, rootfsDir)
	cmd.Env = append(s.terminalEnv(), "BUSYBOX_PATH="+s.busyboxPath())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("post extraction fixes failed: %v\n%s", err, string(out))
	}
	return nil
}

func isRootfsArchive(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".tar.xz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (s *Server) archiveDirectory(ctx context.Context, rootfsDir string, dest string, taskID string) error {
	busybox := s.busyboxPath()
	cmd := exec.CommandContext(ctx, busybox, "tar", "-czf", dest, "-C", rootfsDir, ".")
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("archive failed: %v\n%s", err, string(out))
	}
	return s.verifyArchive(dest, taskID)
}

func (s *Server) archiveImage(ctx context.Context, imagePath string, dest string, taskID string) error {
	mountDir := filepath.Join(filepath.Dir(dest), ".mount-"+taskID)
	if err := os.MkdirAll(mountDir, 0755); err != nil {
		return err
	}
	defer os.Remove(mountDir)
	_ = exec.CommandContext(ctx, "chcon", "u:object_r:vold_data_file:s0", imagePath).Run()
	mountOut, err := exec.CommandContext(ctx, "mount", "-t", "ext4", "-o", "loop,ro", imagePath, mountDir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount image failed: %v\n%s", err, string(mountOut))
	}
	defer exec.Command("umount", "-f", mountDir).Run()
	return s.archiveDirectory(ctx, mountDir, dest, taskID)
}

func (s *Server) verifyArchive(path string, taskID string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Size() <= 0 {
		return fmt.Errorf("archive is empty")
	}
	s.updateTask(taskID, func(task *taskState) {
		task.Downloaded = info.Size()
		task.Total = info.Size()
		task.Percent = 100
	})
	return nil
}

func (s *Server) busyboxPath() string {
	if s.corePath != "" {
		candidate := filepath.Join(s.corePath, "busybox")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	return "busybox"
}

func (s *Server) pathWithinManagedRoots(path string) bool {
	clean := filepath.Clean(path)
	for _, root := range []string{s.templateImageRoot, s.imageRoot, s.workspace} {
		if root == "" {
			continue
		}
		base := filepath.Clean(root)
		if clean == base || strings.HasPrefix(clean, base+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func sanitizeDownloadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, " ", "-")
	name = regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "container"
	}
	return name
}

func (s *Server) runDroidspaces(ctx context.Context, args ...string) (cliCommandResult, error) {
	cmd := exec.CommandContext(ctx, s.droidspacesPath, args...)
	cmd.Env = append(os.Environ(), "TERM=dumb")
	if s.workspace != "" {
		cmd.Dir = s.workspace
	}
	out, err := cmd.CombinedOutput()
	text := ansiPattern.ReplaceAllString(string(out), "")
	result := cliCommandResult{Args: append([]string{}, args...), ExitCode: 0, Output: text}
	if err != nil {
		result.ExitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		if ctx.Err() == context.DeadlineExceeded {
			result.ExitCode = 124
		}
		message := fmt.Sprintf("droidspaces %s failed with exit %d", strings.Join(args, " "), result.ExitCode)
		if strings.TrimSpace(result.Output) == "" {
			result.Output = message
		}
		return result, fmt.Errorf("%s", message)
	}
	return result, nil
}

func (s *Server) containerConfigContent(name, hostname, rootfsPath, netMode string, req createContainerRequest) string {
	var b strings.Builder
	b.WriteString("# Droidspaces Container Configuration\n")
	b.WriteString("# Generated by Droidspaces WebUI\n\n")
	writeConfigLine(&b, "name", name)
	writeConfigLine(&b, "hostname", hostname)
	writeConfigLine(&b, "rootfs_path", rootfsPath)
	writeConfigLine(&b, "net_mode", netMode)
	writeConfigLine(&b, "disable_ipv6", boolFlag(req.DisableIPv6))
	writeConfigLine(&b, "enable_android_storage", boolFlag(req.AndroidStorage))
	writeConfigLine(&b, "enable_hw_access", boolFlag(req.HWAccess))
	writeConfigLine(&b, "enable_gpu_mode", boolFlag(req.GPUMode))
	writeConfigLine(&b, "enable_termux_x11", boolFlag(req.TermuxX11))
	writeConfigLine(&b, "enable_virgl", boolFlag(req.VirGL))
	writeConfigLine(&b, "enable_pulseaudio", boolFlag(req.PulseAudio))
	writeConfigLine(&b, "selinux_permissive", boolFlag(req.SELinuxPermissive))
	writeConfigLine(&b, "volatile_mode", boolFlag(req.VolatileMode))
	writeConfigLine(&b, "run_at_boot", "0")
	writeConfigLine(&b, "force_cgroupv1", boolFlag(req.ForceCgroupV1))
	writeConfigLine(&b, "block_nested_ns", boolFlag(req.BlockNestedNS))
	if strings.TrimSpace(req.DNSServers) != "" {
		writeConfigLine(&b, "dns_servers", strings.TrimSpace(req.DNSServers))
	}
	if strings.TrimSpace(req.PortForwards) != "" {
		writeConfigLine(&b, "port_forwards", strings.TrimSpace(req.PortForwards))
	}
	if strings.TrimSpace(req.BindMounts) != "" {
		writeConfigLine(&b, "bind_mounts", strings.TrimSpace(req.BindMounts))
	}
	if strings.TrimSpace(req.CustomInit) != "" {
		writeConfigLine(&b, "custom_init", strings.TrimSpace(req.CustomInit))
	}
	if strings.TrimSpace(req.Env) != "" {
		writeConfigLine(&b, "env_file", filepath.Join(s.workspace, "Containers", sanitizeContainerName(name), ".env"))
	}
	writeConfigLine(&b, "uuid", newUUID())
	return b.String()
}

func (s *Server) containerConfigPath(name string) (string, bool) {
	dir, err := s.containerDir(name)
	if err != nil {
		return "", false
	}
	path := filepath.Join(dir, "container.config")
	if _, err := os.Stat(path); err == nil {
		return path, true
	}
	return path, false
}

func (s *Server) containerDir(name string) (string, error) {
	clean, err := cleanTarget(name)
	if err != nil {
		return "", err
	}
	base := filepath.Clean(filepath.Join(s.workspace, "Containers"))
	dir := filepath.Clean(filepath.Join(base, sanitizeContainerName(clean)))
	if dir != base && !strings.HasPrefix(dir, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("invalid container path")
	}
	return dir, nil
}

func (s *Server) removePidSidecars(name string) {
	for _, candidate := range []string{name, sanitizeContainerName(name)} {
		if candidate == "" || strings.ContainsAny(candidate, `/\\`) {
			continue
		}
		for _, ext := range []string{".pid", ".mount", ".init"} {
			_ = os.Remove(filepath.Join(s.workspace, "Pids", candidate+ext))
		}
	}
}

func sanitizeContainerName(name string) string {
	return strings.ReplaceAll(name, " ", "-")
}

func hasConfigUnsafeChars(value string) bool {
	return strings.ContainsAny(value, "\r\n\x00")
}

func boolFlag(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

func writeConfigLine(b *strings.Builder, key, value string) {
	b.WriteString(key)
	b.WriteByte('=')
	b.WriteString(value)
	b.WriteByte('\n')
}

func newUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func parseKeyValueOutput(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func valueOr(value string, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func kvBool(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func parsePortList(value string) []socketd.Port {
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
				proto = strings.ToLower(right)
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

func writeBackendError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	var statusErr socketd.StatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Status {
		case socketd.StatusBadRequest:
			code = http.StatusBadRequest
		case socketd.StatusNotFound:
			code = http.StatusNotFound
		case socketd.StatusForbidden:
			code = http.StatusForbidden
		case socketd.StatusAlreadyRunning, socketd.StatusAlreadyStopped:
			code = http.StatusConflict
		default:
			code = http.StatusBadGateway
		}
	}
	writeJSON(w, code, apiError{Error: err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func cleanTarget(raw string) (string, error) {
	if raw == "" || len(raw) > 255 {
		return "", fmt.Errorf("invalid container name")
	}
	if strings.ContainsAny(raw, "/\x00") {
		return "", fmt.Errorf("invalid container name")
	}
	return raw, nil
}
