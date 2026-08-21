package config

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
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
	Mode                     string             `json:"mode"`
	Host                     string             `json:"host"`
	Port                     int                `json:"port"`
	AuthToken                string             `json:"authToken"`
	DroidspacesPath          string             `json:"droidspacesPath"`
	CorePath                 string             `json:"corePath"`
	ImageRoot                string             `json:"imageRoot"`
	TemplateImageRoot        string             `json:"templateImageRoot"`
	Workspace                string             `json:"workspace"`
	SocketdEnabled           *bool              `json:"socketdEnabled"`
	RootfsRepositories       []RootfsRepository `json:"rootfsRepositories"`
	RootfsSkipTLSVerify      bool               `json:"rootfsSkipTLSVerify"`
	DefaultNATCIDR           string             `json:"defaultNatCIDR"`
	DefaultNATThirdOctet     int                `json:"defaultNatThirdOctet"`
	NestedAndroidNATCompat   bool               `json:"nestedAndroidNatCompat"`
	BatteryDirectPower       bool               `json:"batteryDirectPowerSupported"`
	BatterySeriesCells       int                `json:"batterySeriesCells"`
	OverviewPowerEnabled     bool               `json:"overviewPowerEnabled"`
	BatteryMonitoringEnabled bool               `json:"batteryMonitoringEnabled"`
	BatteryDetailEnabled     bool               `json:"batteryDetailEnabled"`
	BatteryStatsSampleSecs   int                `json:"batteryStatsSampleSeconds"`
	BatteryStatsWriteMins    int                `json:"batteryStatsWriteMinutes"`
	OverviewRefreshSecs      int                `json:"overviewRefreshSeconds"`
	GeneratedAuthToken       bool               `json:"-"`
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
	SocketdEnabled  *bool
}

const OfficialRootfsRepositoryURL = "https://github.com/Droidspaces/Droidspaces-rootfs-builder/raw/refs/heads/main/rootfs.json"
const LinuxContainersRepositoryURL = "https://images.linuxcontainers.org/"
const LinuxContainersRepositoryName = "Linux Containers"
const LinuxContainersNJURepositoryURL = "https://mirror.nju.edu.cn/lxc-images/"
const LinuxContainersNJURepositoryName = "Linux Containers CN（南京大学镜像）"
const DefaultNATCIDR = "172.28.0.0/16"
const DefaultNATThirdOctet = 1

// DefaultRootfsRepositories returns the built-in template sources. The Linux
// Containers endpoint publishes a SimpleStreams catalog rather than rootfs.json;
// the WebUI rootfs client recognizes that endpoint automatically.
func DefaultRootfsRepositories() []RootfsRepository {
	return []RootfsRepository{
		{Name: "Droidspaces Official", URL: OfficialRootfsRepositoryURL},
		{Name: LinuxContainersRepositoryName, URL: LinuxContainersRepositoryURL},
	}
}

// EnsureDefaultRootfsRepositories keeps the Linux Containers catalog available
// for old webui.json files that were created before it became a built-in source.
// It also consolidates the legacy official-plus-CN pair into one selected source.
func EnsureDefaultRootfsRepositories(repositories []RootfsRepository) []RootfsRepository {
	if len(repositories) == 0 {
		return DefaultRootfsRepositories()
	}
	repositories = NormalizeLinuxContainersRepositories(repositories)

	for _, repository := range repositories {
		if IsLinuxContainersRepositoryURL(repository.URL) {
			return repositories
		}
	}

	result := append([]RootfsRepository(nil), repositories...)
	return append(result, RootfsRepository{Name: LinuxContainersRepositoryName, URL: LinuxContainersRepositoryURL})
}

// IsLinuxContainersRepositoryURL identifies the managed Linux Containers
// repository roots accepted by the WebUI. It deliberately does not match an
// arbitrary images.linuxcontainers.org URL so user-defined repositories retain
// their original behavior.
func IsLinuxContainersRepositoryURL(value string) bool {
	value = strings.ToLower(strings.TrimRight(strings.TrimSpace(value), "/"))
	canonical := strings.ToLower(strings.TrimRight(LinuxContainersRepositoryURL, "/"))
	return value == canonical || value == canonical+"/streams/v1/images.json" || IsLinuxContainersNJURepositoryURL(value)
}

// NormalizeLinuxContainersRepositories keeps the selected Linux Containers
// origin as one repository. It migrates the old add-only CN setting by folding
// an official-plus-NJU pair into the first Linux Containers position while
// preserving all unrelated repositories and their order.
func NormalizeLinuxContainersRepositories(repositories []RootfsRepository) []RootfsRepository {
	selectedURL := ""
	for _, repository := range repositories {
		if !IsLinuxContainersRepositoryURL(repository.URL) {
			continue
		}
		if selectedURL == "" {
			selectedURL = LinuxContainersRepositoryURL
		}
		if IsLinuxContainersNJURepositoryURL(repository.URL) {
			selectedURL = LinuxContainersNJURepositoryURL
		}
	}
	if selectedURL == "" {
		return append([]RootfsRepository(nil), repositories...)
	}

	selected := RootfsRepository{Name: LinuxContainersRepositoryName, URL: selectedURL}
	result := make([]RootfsRepository, 0, len(repositories))
	inserted := false
	for _, repository := range repositories {
		if !IsLinuxContainersRepositoryURL(repository.URL) {
			result = append(result, repository)
			continue
		}
		if !inserted {
			result = append(result, selected)
			inserted = true
		}
	}
	return result
}

// IsLinuxContainersNJURepositoryURL recognizes only the supported Nanjing
// University Linux Containers mirror root. Keeping the match exact prevents a
// user-provided catalog path or lookalike host from rewriting downloads to an
// unintended origin.
func IsLinuxContainersNJURepositoryURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || strings.ToLower(parsed.Hostname()) != "mirror.nju.edu.cn" {
		return false
	}
	if parsed.Port() != "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return false
	}
	return strings.TrimRight(parsed.EscapedPath(), "/") == "/lxc-images"
}

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
	socketdEnabled := IsAndroid()
	if IsAndroid() {
		templateRoot = filepath.Join(workspace, "rootfs")
	}

	return Config{
		Mode:                     ModeLocal,
		Host:                     "127.0.0.1",
		Port:                     9090,
		DroidspacesPath:          path,
		CorePath:                 corePath,
		ImageRoot:                imageRoot,
		TemplateImageRoot:        templateRoot,
		Workspace:                workspace,
		SocketdEnabled:           &socketdEnabled,
		RootfsSkipTLSVerify:      IsAndroid(),
		DefaultNATCIDR:           DefaultNATCIDR,
		DefaultNATThirdOctet:     DefaultNATThirdOctet,
		OverviewPowerEnabled:     true,
		BatteryMonitoringEnabled: true,
		BatteryDetailEnabled:     true,
		BatteryStatsSampleSecs:   3,
		BatteryStatsWriteMins:    5,
		OverviewRefreshSecs:      3,
		RootfsRepositories:       DefaultRootfsRepositories(),
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

func NormalizeInterfaceList(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return strings.Join(out, ",")
}

func ValidateInterfaceList(value string) error {
	value = NormalizeInterfaceList(value)
	if value == "" {
		return nil
	}
	if len(value) >= 512 {
		return fmt.Errorf("interface candidate list is too long")
	}
	for _, token := range strings.Split(value, ",") {
		if token == "" {
			continue
		}
		if len(token) >= 16 {
			return fmt.Errorf("interface candidate %q is too long", token)
		}
		for _, r := range token {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' || r == '.' || r == ':' || r == '*' || r == '?' || r == '[' || r == ']' {
				continue
			}
			return fmt.Errorf("interface candidate %q contains invalid character %q", token, r)
		}
	}
	return nil
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
	if v := os.Getenv("DS_WEBUI_SOCKETD_ENABLED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.SocketdEnabled = &b
		}
	}
	if v := os.Getenv("DS_WEBUI_ROOTFS_SKIP_TLS_VERIFY"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.RootfsSkipTLSVerify = b
		}
	}
	if v := os.Getenv("DS_WEBUI_DEFAULT_NAT_CIDR"); v != "" {
		cfg.DefaultNATCIDR = v
	}
	if v := os.Getenv("DS_WEBUI_DEFAULT_NAT_THIRD_OCTET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.DefaultNATThirdOctet = n
		}
	}
	if v := os.Getenv("DS_WEBUI_NESTED_ANDROID_NAT_COMPAT"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.NestedAndroidNATCompat = b
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_DIRECT_POWER_SUPPORTED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.BatteryDirectPower = b
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_SERIES_CELLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.BatterySeriesCells = n
		}
	}
	if v := os.Getenv("DS_WEBUI_OVERVIEW_POWER_ENABLED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.OverviewPowerEnabled = b
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_MONITORING_ENABLED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.BatteryMonitoringEnabled = b
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_DETAIL_ENABLED"); v != "" {
		if b, ok := parseBoolEnv(v); ok {
			cfg.BatteryDetailEnabled = b
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_STATS_SAMPLE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.BatteryStatsSampleSecs = n
		}
	}
	if v := os.Getenv("DS_WEBUI_BATTERY_STATS_WRITE_MINUTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.BatteryStatsWriteMins = n
		}
	}
	if v := os.Getenv("DS_WEBUI_OVERVIEW_REFRESH_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.OverviewRefreshSecs = n
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
	if o.SocketdEnabled != nil {
		cfg.SocketdEnabled = o.SocketdEnabled
	}
}

func applyListen(cfg *Config, listen string) {
	host, port, err := net.SplitHostPort(listen)
	if err == nil {
		cfg.Host = host
		if host != "" && host != "127.0.0.1" && host != "localhost" && host != "::1" {
			cfg.Mode = ModePublic
		}
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
	cfg.AuthToken = strings.TrimSpace(cfg.AuthToken)
	cfg.GeneratedAuthToken = false
	if cfg.Host == "" {
		if cfg.Mode == ModePublic {
			cfg.Host = "0.0.0.0"
		} else {
			cfg.Host = "127.0.0.1"
		}
	}
	if cfg.Mode == ModePublic && cfg.AuthToken == "" {
		token, err := GenerateAuthToken(8)
		if err != nil {
			return fmt.Errorf("generate authToken: %w", err)
		}
		cfg.AuthToken = token
		cfg.GeneratedAuthToken = true
	}
	if cfg.Mode == ModePublic && cfg.Host == "127.0.0.1" {
		cfg.Host = "0.0.0.0"
	}
	if cfg.Mode == ModeLocal {
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
	cfg.RootfsRepositories = EnsureDefaultRootfsRepositories(cfg.RootfsRepositories)
	cfg.DefaultNATCIDR = strings.TrimSpace(cfg.DefaultNATCIDR)
	if cfg.DefaultNATCIDR == "" {
		cfg.DefaultNATCIDR = DefaultNATCIDR
	}
	if cfg.DefaultNATCIDR != DefaultNATCIDR {
		return fmt.Errorf("defaultNatCIDR currently must be %q", DefaultNATCIDR)
	}
	if cfg.DefaultNATThirdOctet <= 0 {
		cfg.DefaultNATThirdOctet = DefaultNATThirdOctet
	}
	if cfg.DefaultNATThirdOctet < 1 || cfg.DefaultNATThirdOctet > 254 {
		return fmt.Errorf("defaultNatThirdOctet must be between 1 and 254")
	}
	if cfg.OverviewRefreshSecs <= 0 {
		cfg.OverviewRefreshSecs = 3
	}
	if cfg.OverviewRefreshSecs < 1 || cfg.OverviewRefreshSecs > 60 {
		return fmt.Errorf("overviewRefreshSeconds must be between 1 and 60")
	}
	if cfg.BatterySeriesCells < 0 || cfg.BatterySeriesCells > 6 {
		return fmt.Errorf("batterySeriesCells must be 0(auto) or between 1 and 6")
	}
	if cfg.BatteryStatsSampleSecs <= 0 {
		cfg.BatteryStatsSampleSecs = 3
	}
	if cfg.BatteryStatsSampleSecs < 1 || cfg.BatteryStatsSampleSecs > 60 {
		return fmt.Errorf("batteryStatsSampleSeconds must be between 1 and 60")
	}
	if cfg.BatteryStatsWriteMins <= 0 {
		cfg.BatteryStatsWriteMins = 5
	}
	if cfg.BatteryStatsWriteMins < 5 || cfg.BatteryStatsWriteMins > 1440 {
		return fmt.Errorf("batteryStatsWriteMinutes must be between 5 and 1440")
	}
	if cfg.Workspace == "" {
		if IsAndroid() {
			cfg.Workspace = "/data/local/Droidspaces"
		} else {
			cfg.Workspace = "/var/lib/Droidspaces"
		}
	}
	if cfg.SocketdEnabled == nil {
		socketdEnabled := IsAndroid()
		cfg.SocketdEnabled = &socketdEnabled
	}
	return nil
}

func GenerateAuthToken(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("token length must be positive")
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	max := big.NewInt(int64(len(alphabet)))
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = alphabet[n.Int64()]
	}
	return string(out), nil
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
