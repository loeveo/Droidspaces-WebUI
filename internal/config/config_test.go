package config

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPublicModeGeneratesAuthToken(t *testing.T) {
	cfg := Default()
	cfg.Mode = ModePublic
	cfg.AuthToken = ""

	if err := normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.AuthToken == "" {
		t.Fatal("expected generated authToken")
	}
	if len(cfg.AuthToken) != 8 {
		t.Fatalf("generated authToken length = %d, want 8", len(cfg.AuthToken))
	}
	if !cfg.GeneratedAuthToken {
		t.Fatal("expected GeneratedAuthToken flag")
	}
}

func TestGenerateAuthToken(t *testing.T) {
	token, err := GenerateAuthToken(8)
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 8 {
		t.Fatalf("token length = %d, want 8", len(token))
	}
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		t.Fatalf("token contains invalid character %q", r)
	}
}

func TestDefaultPathLayout(t *testing.T) {
	tests := []struct {
		name              string
		android           bool
		workspace         string
		corePath          string
		droidspacesPath   string
		templateImageRoot string
	}{
		{
			name:              "Android",
			android:           true,
			workspace:         "/data/local/Droidspaces",
			corePath:          "/data/local/Droidspaces/bin",
			droidspacesPath:   "/data/local/Droidspaces/bin/droidspaces",
			templateImageRoot: "/data/local/Droidspaces/rootfs",
		},
		{
			name:              "Linux",
			android:           false,
			workspace:         "/var/lib/Droidspaces",
			corePath:          "/var/lib/Droidspaces/bin",
			droidspacesPath:   "/var/lib/Droidspaces/bin/droidspaces",
			templateImageRoot: "/var/lib/Droidspaces/rootfs",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspace, corePath, droidspacesPath, templateImageRoot := defaultPathLayout(test.android)
			if workspace != test.workspace || corePath != test.corePath || droidspacesPath != test.droidspacesPath || templateImageRoot != test.templateImageRoot {
				t.Fatalf("defaultPathLayout(%t) = (%q, %q, %q, %q)", test.android, workspace, corePath, droidspacesPath, templateImageRoot)
			}
		})
	}
}

func TestDefaultUsesCurrentPlatformWorkspaceLayout(t *testing.T) {
	wantWorkspace, wantCorePath, wantDroidspacesPath, wantTemplateImageRoot := defaultPathLayout(IsAndroid())
	cfg := Default()
	if cfg.Workspace != wantWorkspace {
		t.Fatalf("Workspace = %q, want %q", cfg.Workspace, wantWorkspace)
	}
	if cfg.CorePath != wantCorePath {
		t.Fatalf("CorePath = %q, want %q", cfg.CorePath, wantCorePath)
	}
	if cfg.DroidspacesPath != wantDroidspacesPath {
		t.Fatalf("DroidspacesPath = %q, want %q", cfg.DroidspacesPath, wantDroidspacesPath)
	}
	if cfg.TemplateImageRoot != wantTemplateImageRoot || cfg.ImageRoot != wantTemplateImageRoot {
		t.Fatalf("template roots = (%q, %q), want %q", cfg.TemplateImageRoot, cfg.ImageRoot, wantTemplateImageRoot)
	}
	if got, want := DefaultPath(), filepath.Join(wantWorkspace, "webui.json"); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestPlatformExampleTemplatesUseCanonicalPaths(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to locate config test source file")
	}
	templateDir := filepath.Join(filepath.Dir(sourceFile), "..", "..", "config")
	tests := []struct {
		name       string
		file       string
		workspace  string
		corePath   string
		binaryPath string
		rootfsPath string
		socketd    bool
		skipTLS    bool
	}{
		{
			name:       "Linux",
			file:       "webui.linux.example.json",
			workspace:  "/var/lib/Droidspaces",
			corePath:   "/var/lib/Droidspaces/bin",
			binaryPath: "/var/lib/Droidspaces/bin/droidspaces",
			rootfsPath: "/var/lib/Droidspaces/rootfs",
			socketd:    false,
			skipTLS:    false,
		},
		{
			name:       "Android",
			file:       "webui.android.example.json",
			workspace:  "/data/local/Droidspaces",
			corePath:   "/data/local/Droidspaces/bin",
			binaryPath: "/data/local/Droidspaces/bin/droidspaces",
			rootfsPath: "/data/local/Droidspaces/rootfs",
			socketd:    true,
			skipTLS:    true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, _, err := Load(filepath.Join(templateDir, test.file), CLIOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Workspace != test.workspace || cfg.CorePath != test.corePath || cfg.DroidspacesPath != test.binaryPath {
				t.Fatalf("paths = workspace:%q core:%q binary:%q", cfg.Workspace, cfg.CorePath, cfg.DroidspacesPath)
			}
			if cfg.ImageRoot != test.rootfsPath || cfg.TemplateImageRoot != test.rootfsPath {
				t.Fatalf("template roots = image:%q template:%q, want %q", cfg.ImageRoot, cfg.TemplateImageRoot, test.rootfsPath)
			}
			if cfg.SocketdEnabled == nil || *cfg.SocketdEnabled != test.socketd {
				t.Fatalf("SocketdEnabled = %#v, want %t", cfg.SocketdEnabled, test.socketd)
			}
			if cfg.RootfsSkipTLSVerify != test.skipTLS {
				t.Fatalf("RootfsSkipTLSVerify = %t, want %t", cfg.RootfsSkipTLSVerify, test.skipTLS)
			}
			if len(cfg.RootfsRepositories) != 2 || cfg.RootfsRepositories[1].Name != LinuxContainersRepositoryName {
				t.Fatalf("rootfs repositories = %#v", cfg.RootfsRepositories)
			}
		})
	}
}

func TestTemplateImageRootDefaultsFromWorkspace(t *testing.T) {
	cfg := Config{
		Mode:            ModeLocal,
		Host:            "127.0.0.1",
		Port:            9090,
		Workspace:       "/data/local/Droidspaces",
		DroidspacesPath: "/data/local/Droidspaces/bin/droidspaces",
	}

	if err := normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.CorePath != "/data/local/Droidspaces/bin" {
		t.Fatalf("CorePath = %q", cfg.CorePath)
	}
	if cfg.ImageRoot != "/data/local/Droidspaces/rootfs" {
		t.Fatalf("ImageRoot = %q", cfg.ImageRoot)
	}
	if cfg.TemplateImageRoot != "/data/local/Droidspaces/rootfs" {
		t.Fatalf("TemplateImageRoot = %q", cfg.TemplateImageRoot)
	}
}

func TestDefaultRootfsRepositoriesIncludeLinuxContainers(t *testing.T) {
	if Default().NestedAndroidNATCompat {
		t.Fatal("nested Android NAT compatibility must default to disabled")
	}
	if !Default().OverviewPowerEnabled || !Default().BatteryMonitoringEnabled {
		t.Fatal("battery features must default to enabled")
	}
	repositories := DefaultRootfsRepositories()
	if len(repositories) != 2 {
		t.Fatalf("repository count = %d, want 2", len(repositories))
	}
	if repositories[1].Name != LinuxContainersRepositoryName || repositories[1].URL != LinuxContainersRepositoryURL {
		t.Fatalf("Linux Containers repository = %#v", repositories[1])
	}
}

func TestLoadKeepsBatteryFeatureDefaultsForExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(path, []byte(`{"mode":"local"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path, CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OverviewPowerEnabled || !cfg.BatteryMonitoringEnabled {
		t.Fatalf("existing config must retain enabled battery defaults: %#v", cfg)
	}
}

func TestLoadHonorsDisabledBatteryFeatures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webui.json")
	if err := os.WriteFile(path, []byte(`{"overviewPowerEnabled":false,"batteryMonitoringEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := Load(path, CLIOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OverviewPowerEnabled || cfg.BatteryMonitoringEnabled {
		t.Fatalf("explicit disabled battery features were not loaded: %#v", cfg)
	}
}

func TestEnsureDefaultRootfsRepositoriesKeepsExistingAndAddsLinuxContainers(t *testing.T) {
	existing := []RootfsRepository{{Name: "Custom", URL: "https://example.test/rootfs.json"}}
	repositories := EnsureDefaultRootfsRepositories(existing)
	if len(repositories) != 2 || repositories[0] != existing[0] {
		t.Fatalf("repositories = %#v", repositories)
	}
	if repositories[1].URL != LinuxContainersRepositoryURL {
		t.Fatalf("Linux Containers repository missing: %#v", repositories)
	}

	repositories = EnsureDefaultRootfsRepositories(repositories)
	if len(repositories) != 2 {
		t.Fatalf("Linux Containers repository duplicated: %#v", repositories)
	}

	repositories = EnsureDefaultRootfsRepositories([]RootfsRepository{{Name: "Linux Containers", URL: LinuxContainersRepositoryURL + "streams/v1/images.json"}})
	if len(repositories) != 1 {
		t.Fatalf("SimpleStreams endpoint should not add a duplicate: %#v", repositories)
	}

	repositories = EnsureDefaultRootfsRepositories([]RootfsRepository{{Name: LinuxContainersNJURepositoryName, URL: LinuxContainersNJURepositoryURL}})
	if len(repositories) != 1 {
		t.Fatalf("NJU mirror should not add the official catalog alongside its explicit source: %#v", repositories)
	}
}

func TestNormalizeLinuxContainersRepositoriesKeepsOneSelectedOrigin(t *testing.T) {
	customFirst := RootfsRepository{Name: "First", URL: "https://first.example.test/rootfs.json"}
	customLast := RootfsRepository{Name: "Last", URL: "https://last.example.test/rootfs.json"}

	tests := []struct {
		name string
		in   []RootfsRepository
		want []RootfsRepository
	}{
		{
			name: "official remains official",
			in:   []RootfsRepository{customFirst, {Name: "Old name", URL: LinuxContainersRepositoryURL}, customLast},
			want: []RootfsRepository{customFirst, {Name: LinuxContainersRepositoryName, URL: LinuxContainersRepositoryURL}, customLast},
		},
		{
			name: "CN remains CN",
			in:   []RootfsRepository{customFirst, {Name: "Old name", URL: LinuxContainersNJURepositoryURL}, customLast},
			want: []RootfsRepository{customFirst, {Name: LinuxContainersRepositoryName, URL: LinuxContainersNJURepositoryURL}, customLast},
		},
		{
			name: "legacy pair selects CN and preserves first position",
			in: []RootfsRepository{
				customFirst,
				{Name: LinuxContainersRepositoryName, URL: LinuxContainersRepositoryURL},
				{Name: LinuxContainersNJURepositoryName, URL: LinuxContainersNJURepositoryURL},
				customLast,
			},
			want: []RootfsRepository{customFirst, {Name: LinuxContainersRepositoryName, URL: LinuxContainersNJURepositoryURL}, customLast},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NormalizeLinuxContainersRepositories(test.in); !equalRootfsRepositories(got, test.want) {
				t.Fatalf("NormalizeLinuxContainersRepositories() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestIsLinuxContainersRepositoryNameRecognizesCurrentAndLegacyNames(t *testing.T) {
	for rawName, want := range map[string]bool{
		LinuxContainersRepositoryName:    true,
		LinuxContainersNJURepositoryName: true,
		"Linux Containers":               true,
		"Linux Containers CN（南京大学镜像）":    true,
		"Other repository":               false,
	} {
		if got := IsLinuxContainersRepositoryName(rawName); got != want {
			t.Errorf("IsLinuxContainersRepositoryName(%q) = %v, want %v", rawName, got, want)
		}
	}
}

func equalRootfsRepositories(got, want []RootfsRepository) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func TestIsLinuxContainersNJURepositoryURLRequiresSupportedRoot(t *testing.T) {
	for rawURL, want := range map[string]bool{
		"https://mirror.nju.edu.cn/lxc-images/":               true,
		"https://MIRROR.NJU.EDU.CN/lxc-images":                true,
		"http://mirror.nju.edu.cn/lxc-images/":                false,
		"https://mirror.nju.edu.cn:443/lxc-images/":           false,
		"https://mirror.nju.edu.cn/lxc-images/?catalog=other": false,
		"https://mirror.nju.edu.cn/lxc-images-extra/":         false,
		"https://mirror.nju.edu.cn.evil.example/lxc-images/":  false,
	} {
		if got := IsLinuxContainersNJURepositoryURL(rawURL); got != want {
			t.Errorf("IsLinuxContainersNJURepositoryURL(%q) = %v, want %v", rawURL, got, want)
		}
	}
}

func TestLocalModeForcesLoopbackHost(t *testing.T) {
	cfg := Default()
	cfg.Mode = ModeLocal
	cfg.Host = "192.168.1.10"

	if err := normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" {
		t.Fatalf("local mode host = %q, want 127.0.0.1", cfg.Host)
	}
}

func TestListenPublicAddressSetsPublicMode(t *testing.T) {
	cfg := Default()
	applyOverrides(&cfg, CLIOverrides{Listen: "0.0.0.0:9090", AuthToken: "secret"})
	if err := normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != ModePublic || cfg.Host != "0.0.0.0" || cfg.Port != 9090 {
		t.Fatalf("listen override = mode:%s host:%s port:%d", cfg.Mode, cfg.Host, cfg.Port)
	}
}

func TestSocketdEnabledCanBeOverridden(t *testing.T) {
	cfg := Default()
	cfg.SocketdEnabled = nil
	t.Setenv("DS_WEBUI_SOCKETD_ENABLED", "false")
	t.Setenv("DS_WEBUI_BATTERY_DIRECT_POWER_SUPPORTED", "true")
	t.Setenv("DS_WEBUI_BATTERY_SERIES_CELLS", "2")
	t.Setenv("DS_WEBUI_OVERVIEW_POWER_ENABLED", "false")
	t.Setenv("DS_WEBUI_BATTERY_MONITORING_ENABLED", "false")
	t.Setenv("DS_WEBUI_BATTERY_DETAIL_ENABLED", "false")
	t.Setenv("DS_WEBUI_BATTERY_STATS_SAMPLE_SECONDS", "5")
	t.Setenv("DS_WEBUI_BATTERY_STATS_WRITE_MINUTES", "6")
	t.Setenv("DS_WEBUI_OVERVIEW_REFRESH_SECONDS", "7")
	t.Setenv("DS_WEBUI_NESTED_ANDROID_NAT_COMPAT", "true")
	applyEnv(&cfg)
	if cfg.SocketdEnabled == nil || *cfg.SocketdEnabled {
		t.Fatalf("env socketd override not applied: %#v", cfg.SocketdEnabled)
	}
	if !cfg.BatteryDirectPower {
		t.Fatalf("battery direct power env override not applied")
	}
	if cfg.BatterySeriesCells != 2 {
		t.Fatalf("battery series cells env override not applied: %d", cfg.BatterySeriesCells)
	}
	if cfg.OverviewPowerEnabled {
		t.Fatalf("overview power env override not applied")
	}
	if cfg.BatteryMonitoringEnabled {
		t.Fatalf("battery monitoring env override not applied")
	}
	if cfg.BatteryDetailEnabled {
		t.Fatalf("battery detail env override not applied")
	}
	if cfg.BatteryStatsSampleSecs != 5 {
		t.Fatalf("battery stats sample env override not applied: %d", cfg.BatteryStatsSampleSecs)
	}
	if cfg.BatteryStatsWriteMins != 6 {
		t.Fatalf("battery stats write env override not applied: %d", cfg.BatteryStatsWriteMins)
	}
	if cfg.OverviewRefreshSecs != 7 {
		t.Fatalf("overview refresh env override not applied: %d", cfg.OverviewRefreshSecs)
	}
	if !cfg.NestedAndroidNATCompat {
		t.Fatal("nested Android NAT compatibility env override not applied")
	}

	enabled := true
	applyOverrides(&cfg, CLIOverrides{SocketdEnabled: &enabled})
	if cfg.SocketdEnabled == nil || !*cfg.SocketdEnabled {
		t.Fatalf("cli socketd override not applied: %#v", cfg.SocketdEnabled)
	}
}
