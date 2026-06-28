package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	ModeLocal  = "local"
	ModePublic = "public"
)

type Config struct {
	Mode                string             `json:"mode"`
	Host                string             `json:"host"`
	Port                int                `json:"port"`
	AuthToken           string             `json:"authToken"`
	DroidspacesPath     string             `json:"droidspacesPath"`
	CorePath            string             `json:"corePath"`
	ImageRoot           string             `json:"imageRoot"`
	TemplateImageRoot   string             `json:"templateImageRoot"`
	Workspace           string             `json:"workspace"`
	RootfsRepositories  []RootfsRepository `json:"rootfsRepositories"`
	RootfsSkipTLSVerify bool               `json:"rootfsSkipTLSVerify"`
}

type RootfsRepository struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type CLIOverrides struct {
	Listen          string
	DroidspacesPath string
	AuthToken       string
	Workspace       string
	Mode            string
	Host            string
	Port            int
	CorePath        string
	ImageRoot       string
	TemplateRoot    string
}

const OfficialRootfsRepositoryURL = "https://github.com/Droidspaces/Droidspaces-rootfs-builder/raw/refs/heads/main/rootfs.json"

func DefaultPath() string {
	if IsAndroid() {
		return "/data/local/Droidspaces/webui.json"
	}
	return "/var/lib/Droidspaces/webui.json"
}

func Default() Config {
	workspace := "/var/lib/Droidspaces"
	if IsAndroid() {
		workspace = "/data/local/Droidspaces"
	}

	path := ""
	if found, err := exec.LookPath("droidspaces"); err == nil {
		path = found
	} else if IsAndroid() {
		path = filepath.Join(workspace, "bin", "droidspaces")
	} else {
		path = "../output/droidspaces"
	}

	corePath := filepath.Dir(path)
	imageRoot := corePath
	templateRoot := filepath.Join(corePath, "templates")
	if IsAndroid() {
		templateRoot = filepath.Join(workspace, "rootfs")
	}

	return Config{
		Mode:                ModeLocal,
		Host:                "127.0.0.1",
		Port:                9090,
		DroidspacesPath:     path,
		CorePath:            corePath,
		ImageRoot:           imageRoot,
		TemplateImageRoot:   templateRoot,
		Workspace:           workspace,
		RootfsSkipTLSVerify: IsAndroid(),
		RootfsRepositories: []RootfsRepository{{
			Name: "Droidspaces Official",
			URL:  OfficialRootfsRepositoryURL,
		}},
	}
}

func Load(path string, overrides CLIOverrides) (Config, string, error) {
	cfg := Default()
	usedPath := path
	if usedPath == "" {
		usedPath = env("DS_WEBUI_CONFIG", DefaultPath())
	}

	if usedPath != "" {
		if err := loadFile(usedPath, &cfg); err != nil {
			return Config{}, usedPath, err
		}
	}

	applyEnv(&cfg)
	applyOverrides(&cfg, overrides)
	if err := normalize(&cfg); err != nil {
		return Config{}, usedPath, err
	}
	return cfg, usedPath, nil
}

func WriteDefault(path string) error {
	if path == "" {
		path = DefaultPath()
	}
	cfg := Default()
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

func (c Config) ListenAddr() string {
	host := c.Host
	if host == "" {
		if c.Mode == ModePublic {
			host = "0.0.0.0"
		} else {
			host = "127.0.0.1"
		}
	}
	return net.JoinHostPort(host, strconv.Itoa(c.Port))
}

func loadFile(path string, cfg *Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}

func applyEnv(cfg *Config) {
	if v := os.Getenv("DS_WEBUI_MODE"); v != "" {
		cfg.Mode = v
	}
	if v := os.Getenv("DS_WEBUI_HOST"); v != "" {
		cfg.Host = v
	}
	if v := os.Getenv("DS_WEBUI_PORT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Port = n
		}
	}
	if v := os.Getenv("DS_WEBUI_LISTEN"); v != "" {
		applyListen(cfg, v)
	}
	if v := os.Getenv("DS_WEBUI_DROIDSPACES"); v != "" {
		cfg.DroidspacesPath = v
	}
	if v := os.Getenv("DS_WEBUI_AUTH_TOKEN"); v != "" {
		cfg.AuthToken = v
	}
	if v := os.Getenv("DS_WEBUI_WORKSPACE"); v != "" {
		cfg.Workspace = v
	}
	if v := os.Getenv("DS_WEBUI_CORE_PATH"); v != "" {
		cfg.CorePath = v
	}
	if v := os.Getenv("DS_WEBUI_IMAGE_ROOT"); v != "" {
		cfg.ImageRoot = v
	}
	if v := os.Getenv("DS_WEBUI_TEMPLATE_IMAGE_ROOT"); v != "" {
		cfg.TemplateImageRoot = v
	}
	if v := os.Getenv("DS_WEBUI_ROOTFS_SKIP_TLS_VERIFY"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.RootfsSkipTLSVerify = b
		}
	}
}

func applyOverrides(cfg *Config, o CLIOverrides) {
	if o.Mode != "" {
		cfg.Mode = o.Mode
	}
	if o.Host != "" {
		cfg.Host = o.Host
	}
	if o.Port > 0 {
		cfg.Port = o.Port
	}
	if o.Listen != "" {
		applyListen(cfg, o.Listen)
	}
	if o.DroidspacesPath != "" {
		cfg.DroidspacesPath = o.DroidspacesPath
	}
	if o.AuthToken != "" {
		cfg.AuthToken = o.AuthToken
	}
	if o.Workspace != "" {
		cfg.Workspace = o.Workspace
	}
	if o.CorePath != "" {
		cfg.CorePath = o.CorePath
	}
	if o.ImageRoot != "" {
		cfg.ImageRoot = o.ImageRoot
	}
	if o.TemplateRoot != "" {
		cfg.TemplateImageRoot = o.TemplateRoot
	}
}

func applyListen(cfg *Config, listen string) {
	host, port, err := net.SplitHostPort(listen)
	if err == nil {
		cfg.Host = host
		if n, convErr := strconv.Atoi(port); convErr == nil {
			cfg.Port = n
		}
		return
	}
	if n, convErr := strconv.Atoi(listen); convErr == nil {
		cfg.Port = n
	}
}

func normalize(cfg *Config) error {
	cfg.Mode = strings.ToLower(strings.TrimSpace(cfg.Mode))
	if cfg.Mode == "" {
		cfg.Mode = ModeLocal
	}
	if cfg.Mode != ModeLocal && cfg.Mode != ModePublic {
		return fmt.Errorf("mode must be %q or %q", ModeLocal, ModePublic)
	}
	if cfg.Host == "" {
		if cfg.Mode == ModePublic {
			cfg.Host = "0.0.0.0"
		} else {
			cfg.Host = "127.0.0.1"
		}
	}
	if cfg.Mode == ModePublic && cfg.AuthToken == "" {
		return fmt.Errorf("authToken is required when mode is %q", ModePublic)
	}
	if cfg.Mode == ModePublic && cfg.Host == "127.0.0.1" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Mode == ModeLocal && (cfg.Host == "" || cfg.Host == "0.0.0.0") {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if cfg.DroidspacesPath == "" {
		return fmt.Errorf("droidspacesPath is required")
	}
	if cfg.CorePath == "" {
		cfg.CorePath = filepath.Dir(cfg.DroidspacesPath)
	}
	if cfg.ImageRoot == "" {
		cfg.ImageRoot = cfg.CorePath
	}
	if cfg.TemplateImageRoot == "" {
		cfg.TemplateImageRoot = filepath.Join(cfg.ImageRoot, "templates")
	}
	if len(cfg.RootfsRepositories) == 0 {
		cfg.RootfsRepositories = []RootfsRepository{{
			Name: "Droidspaces Official",
			URL:  OfficialRootfsRepositoryURL,
		}}
	}
	if cfg.Workspace == "" {
		if IsAndroid() {
			cfg.Workspace = "/data/local/Droidspaces"
		} else {
			cfg.Workspace = "/var/lib/Droidspaces"
		}
	}
	return nil
}

func IsAndroid() bool {
	if _, err := os.Stat("/system/build.prop"); err == nil {
		return true
	}
	if strings.Contains(strings.ToLower(os.Getenv("ANDROID_ROOT")), "system") {
		return true
	}
	return strings.HasPrefix(filepath.Clean(os.Getenv("PREFIX")), "/data/data/com.termux")
}

func parseBoolEnv(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
