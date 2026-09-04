package web

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/platformhttp"
	"github.com/ravindu644/droidspaces-oss/webui/internal/rootfs"
	"github.com/ravindu644/droidspaces-oss/webui/internal/socketd"
	"github.com/ravindu644/droidspaces-oss/webui/internal/workspace"
)

//go:embed static/*
var staticFiles embed.FS

//go:embed scripts/post_extract_fixes.sh
var postExtractFixesScript string

type Server struct {
	socketd                             *socketd.Client
	socketdEnabled                      bool
	droidspacesPath                     string
	webVersion                          string
	supportedCoreVersion                string
	authToken                           string
	uiLanguage                          string
	uiLanguageConfigured                bool
	workspace                           string
	configPath                          string
	mode                                string
	host                                string
	port                                int
	corePath                            string
	imageRoot                           string
	templateImageRoot                   string
	rootfsRepos                         []config.RootfsRepository
	rootfsReposConfigured               bool
	rootfsClient                        *rootfs.Client
	rootfsSkipTLSVerify                 bool
	rootfsCacheMu                       sync.RWMutex
	rootfsCatalogRefreshMu              sync.Mutex
	rootfsCatalogRefreshCancel          context.CancelFunc
	rootfsCatalogRefreshDone            chan struct{}
	disableRootfsCatalogRefresh         bool
	rootfsDownloadMu                    sync.Mutex
	rootfsDownloads                     map[string]*sharedRootfsDownload
	rootfsDownloadRequests              map[string]*rootfsDownloadRequestFlight
	cgroupRoot                          string
	defaultNATCIDR                      string
	defaultNATThirdOctet                int
	nestedAndroidNATCompat              bool
	nestedAndroidNATScope               nestedAndroidNATScope
	nestedAndroidNATCompatMu            sync.RWMutex
	nestedAndroidNATCompatExecMu        sync.Mutex
	nestedAndroidNATCompatMonitorMu     sync.Mutex
	nestedAndroidNATCompatMonitorCancel context.CancelFunc
	disableNATCompatRuntime             bool
	// nestedAndroidNATCommand is injected by tests. Production always uses
	// fixed executable names and arguments from nat_compat.go.
	nestedAndroidNATCommand   natCompatCommandRunner
	batteryDirectPower        bool
	batterySeriesCells        int
	overviewPowerEnabled      bool
	batteryMonitoringEnabled  bool
	batteryFeatureMu          sync.RWMutex
	batterySamplerMu          sync.Mutex
	batterySamplerCtx         context.Context
	batterySamplerCancel      context.CancelFunc
	batterySamplerDone        chan struct{}
	disableBatterySampler     bool
	batteryDetailEnabled      bool
	batteryStatsSampleSecs    int64
	batteryStatsWriteMins     int64
	batteryStatsRetentionDays int64
	overviewRefreshSecs       int
	systemSettingsMu          sync.RWMutex
	configMu                  sync.Mutex
	containerConfigMu         sync.Mutex
	containerDistroMu         sync.Mutex
	natIPMu                   sync.Mutex
	natIPReservations         map[string]string
	portForwardMu             sync.Mutex
	portForwardReservations   map[string][]socketd.Port
	containerDistroCache      map[string]containerDistroCacheEntry
	backendDiagnosticsMu      sync.Mutex
	backendDiagnostics        []backendDiagnosticEntry
	tasksMu                   sync.RWMutex
	tasks                     map[string]*taskState
	containerTaskMu           sync.Mutex
	containerTasks            map[string]string
	hostStatsMu               sync.Mutex
	lastCPUSample             cpuSample
	batteryStatsMu            sync.Mutex
	batteryStats              batteryStatsState
	coreVersionMu             sync.Mutex
	detectedCoreVersion       string
	coreVersionCheckedAt      time.Time
	coreUpdateMu              sync.Mutex
	coreUpdateRunning         bool
	coreUpdateHTTPClient      *http.Client
}

type Options struct {
	DroidspacesPath              string
	WebVersion                   string
	SupportedCoreVersion         string
	AuthToken                    string
	UILanguage                   string
	UILanguageConfigured         bool
	Workspace                    string
	ConfigPath                   string
	Mode                         string
	Host                         string
	Port                         int
	CorePath                     string
	ImageRoot                    string
	TemplateImageRoot            string
	RootfsRepos                  []config.RootfsRepository
	RootfsRepositoriesConfigured bool
	RootfsSkipTLSVerify          bool
	DefaultNATCIDR               string
	DefaultNATThirdOctet         int
	NestedAndroidNATCompat       bool
	BatteryDirectPower           bool
	BatterySeriesCells           int
	OverviewPowerEnabled         *bool
	BatteryMonitoringEnabled     *bool
	BatteryDetailEnabled         *bool
	BatteryStatsSampleSecs       int
	BatteryStatsWriteMins        int
	BatteryStatsRetentionDays    int
	OverviewRefreshSecs          int
	SocketdEnabled               bool
	DisableBatterySampler        bool
	DisableRootfsCatalogRefresh  bool
}

type apiError struct {
	Error string `json:"error"`
}

type inspectResponse struct {
	containerView
	ImageRef           string              `json:"imageRef,omitempty"`
	DNSServers         string              `json:"dnsServers,omitempty"`
	MemoryLimit        int64               `json:"memoryLimit"`
	CPUQuota           int64               `json:"cpuQuota"`
	CPUPeriod          int64               `json:"cpuPeriod"`
	PidsLimit          int64               `json:"pidsLimit"`
	MemoryLimitText    string              `json:"memoryLimitText,omitempty"`
	CPUsText           string              `json:"cpusText,omitempty"`
	PrivilegedMask     int32               `json:"privilegedMask"`
	Foreground         bool                `json:"foreground"`
	VolatileMode       bool                `json:"volatileMode"`
	ForceCgroupV1      bool                `json:"forceCgroupV1"`
	DisableIPv6        bool                `json:"disableIPv6"`
	AndroidStorage     bool                `json:"androidStorage"`
	SELinuxPermissive  bool                `json:"selinuxPermissive"`
	HWAccess           bool                `json:"hwAccess"`
	GPUMode            bool                `json:"gpuMode"`
	TermuxX11          bool                `json:"termuxX11"`
	Tx11ExtraFlags     string              `json:"tx11ExtraFlags,omitempty"`
	VirGL              bool                `json:"virgl"`
	VirGLExtraFlags    string              `json:"virglExtraFlags,omitempty"`
	PulseAudio         bool                `json:"pulseAudio"`
	BlockNestedNS      bool                `json:"blockNestedNamespaces"`
	IsImageMount       bool                `json:"isImageMount"`
	StaticNATIP        string              `json:"staticNatIp,omitempty"`
	NATUpstreamIfnames string              `json:"natUpstreamIfnames,omitempty"`
	GatewayContainer   string              `json:"gatewayContainer,omitempty"`
	GatewayNet         string              `json:"gatewayNet,omitempty"`
	GatewayLanIfname   string              `json:"gatewayLanIfname,omitempty"`
	GatewayBridge      string              `json:"gatewayBridge,omitempty"`
	PrivilegedMode     string              `json:"privilegedMode,omitempty"`
	ConfigValues       map[string]string   `json:"configValues,omitempty"`
	Env                []socketd.EnvVar    `json:"env,omitempty"`
	EnvTotal           int                 `json:"envTotal"`
	Binds              []socketd.BindMount `json:"binds,omitempty"`
	BindTotal          int                 `json:"bindTotal"`
	PortTotal          int                 `json:"portTotal"`
	Source             string              `json:"source,omitempty"`
	BackendError       string              `json:"backendError,omitempty"`
	CLIInfo            map[string]string   `json:"cliInfo,omitempty"`
	RawOutput          string              `json:"rawOutput,omitempty"`
}

type createContainerRequest struct {
	Name               string `json:"name"`
	Hostname           string `json:"hostname"`
	RootFSPath         string `json:"rootfsPath"`
	RootFSSource       string `json:"rootfsSource"`
	RootFSTaskID       string `json:"rootfsTaskId"`
	CloudRootFSURL     string `json:"cloudRootfsUrl"`
	CloudInitEnabled   *bool  `json:"cloudInitEnabled"`
	CloudInitUserData  string `json:"cloudInitUserData"`
	CloudInitNetwork   string `json:"cloudInitNetworkConfig"`
	CloudInitNATStatic bool   `json:"-"`
	RootFSStorageMode  string `json:"rootfsStorageMode"`
	StorageMode        string `json:"storageMode"`
	UseSparseImage     *bool  `json:"useSparseImage"`
	RootFSImageSizeGB  int    `json:"rootfsImageSizeGB"`
	ImageSizeGB        int    `json:"imageSizeGB"`
	NetMode            string `json:"netMode"`
	DNSServers         string `json:"dnsServers"`
	PortForwards       string `json:"portForwards"`
	StaticNATIP        string `json:"staticNatIp"`
	NATUpstreamIfnames string `json:"natUpstreamIfnames"`
	NATUpstreamIfname  string `json:"natUpstreamIfname"`
	GatewayContainer   string `json:"gatewayContainer"`
	GatewayNet         string `json:"gatewayNet"`
	GatewayLanIfname   string `json:"gatewayLanIfname"`
	GatewayBridge      string `json:"gatewayBridge"`
	PrivilegedMode     string `json:"privilegedMode"`
	BindMounts         string `json:"bindMounts"`
	Env                string `json:"env"`
	CustomInit         string `json:"customInit"`
	DisableIPv6        bool   `json:"disableIPv6"`
	AndroidStorage     bool   `json:"androidStorage"`
	HWAccess           bool   `json:"hwAccess"`
	GPUMode            bool   `json:"gpuMode"`
	TermuxX11          bool   `json:"termuxX11"`
	Tx11ExtraFlags     string `json:"tx11ExtraFlags"`
	VirGL              bool   `json:"virgl"`
	VirGLExtraFlags    string `json:"virglExtraFlags"`
	PulseAudio         bool   `json:"pulseAudio"`
	SELinuxPermissive  bool   `json:"selinuxPermissive"`
	AllowUserNS        bool   `json:"allowUserns"`
	VolatileMode       bool   `json:"volatileMode"`
	RunAtBoot          bool   `json:"runAtBoot"`
	RunAtBootPriority  int    `json:"runAtBootPriority"`
	ForceCgroupV1      bool   `json:"forceCgroupV1"`
	BlockNestedNS      bool   `json:"blockNestedNamespaces"`
	MemoryLimit        string `json:"memoryLimit"`
	CPUs               string `json:"cpus"`
	PidsLimit          string `json:"pidsLimit"`
	Start              bool   `json:"start"`
}

type updateContainerConfigRequest struct {
	Hostname           *string `json:"hostname"`
	NetMode            *string `json:"netMode"`
	DNSServers         *string `json:"dnsServers"`
	PortForwards       *string `json:"portForwards"`
	StaticNATIP        *string `json:"staticNatIp"`
	NATUpstreamIfnames *string `json:"natUpstreamIfnames"`
	NATUpstreamIfname  *string `json:"natUpstreamIfname"`
	GatewayContainer   *string `json:"gatewayContainer"`
	GatewayNet         *string `json:"gatewayNet"`
	GatewayLanIfname   *string `json:"gatewayLanIfname"`
	GatewayBridge      *string `json:"gatewayBridge"`
	PrivilegedMode     *string `json:"privilegedMode"`
	BindMounts         *string `json:"bindMounts"`
	Env                *string `json:"env"`
	CustomInit         *string `json:"customInit"`
	Restore            *bool   `json:"restore"`
	RestoreAfterUpdate *bool   `json:"restoreAfterUpdate"`
	DisableIPv6        *bool   `json:"disableIPv6"`
	AndroidStorage     *bool   `json:"androidStorage"`
	HWAccess           *bool   `json:"hwAccess"`
	GPUMode            *bool   `json:"gpuMode"`
	TermuxX11          *bool   `json:"termuxX11"`
	Tx11ExtraFlags     *string `json:"tx11ExtraFlags"`
	VirGL              *bool   `json:"virgl"`
	VirGLExtraFlags    *string `json:"virglExtraFlags"`
	PulseAudio         *bool   `json:"pulseAudio"`
	SELinuxPermissive  *bool   `json:"selinuxPermissive"`
	AllowUserNS        *bool   `json:"allowUserns"`
	VolatileMode       *bool   `json:"volatileMode"`
	RunAtBoot          *bool   `json:"runAtBoot"`
	RunAtBootPriority  *int    `json:"runAtBootPriority"`
	ForceCgroupV1      *bool   `json:"forceCgroupV1"`
	BlockNestedNS      *bool   `json:"blockNestedNamespaces"`
	MemoryLimit        *string `json:"memoryLimit"`
	CPUs               *string `json:"cpus"`
	PidsLimit          *string `json:"pidsLimit"`
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
	ID                 string   `json:"id"`
	Kind               string   `json:"kind"`
	Name               string   `json:"name"`
	Status             string   `json:"status"`
	Downloaded         int64    `json:"downloaded,omitempty"`
	Total              int64    `json:"total,omitempty"`
	Percent            int      `json:"percent"`
	Path               string   `json:"path,omitempty"`
	URL                string   `json:"url,omitempty"`
	Error              string   `json:"error,omitempty"`
	WillStopContainer  bool     `json:"willStopContainer,omitempty"`
	RestoreAfterBackup bool     `json:"restoreAfterBackup,omitempty"`
	StoppedContainer   bool     `json:"stoppedContainer,omitempty"`
	RestoredContainer  bool     `json:"restoredContainer,omitempty"`
	RestoreError       string   `json:"restoreError,omitempty"`
	Log                []string `json:"log,omitempty"`
	Output             string   `json:"output,omitempty"`
	ExitCode           int      `json:"exitCode,omitempty"`
	StartedAt          int64    `json:"startedAt"`
	UpdatedAt          int64    `json:"updatedAt"`
}

// sharedRootfsDownload represents the single in-flight writer for one rootfs
// archive. All callers that need the same managed destination wait on done;
// only the owner touches the archive's .part file.
type sharedRootfsDownload struct {
	taskID       string
	done         chan struct{}
	asset        rootfs.Asset
	downloadRoot string
	storage      localRootfsStorageSource
	path         string
	err          error
}

// rootfsDownloadRequestFlight coalesces requests while their selected URL is
// being verified against configured repositories. The archive-level flight
// above takes over once the verified asset and its managed destination are
// known.
type rootfsDownloadRequestFlight struct {
	taskID string
}

type taskSummary struct {
	Total     int            `json:"total"`
	Active    int            `json:"active"`
	Pending   int            `json:"pending"`
	Running   int            `json:"running"`
	Done      int            `json:"done"`
	Error     int            `json:"error"`
	Cancelled int            `json:"cancelled"`
	ByKind    map[string]int `json:"byKind"`
}

type localRootfsItem struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
	Source   string `json:"source"`
	Variant  string `json:"variant,omitempty"`
}

type localRootfsStorageSource struct {
	directory          string
	label              string
	kindOverride       string
	variantDirectories bool
}

const (
	rootfsDroidspacesOfficialDirectory     = "droidspaces-official"
	rootfsLinuxContainersDirectory         = "lxc-image"
	rootfsLinuxContainersPreviousDirectory = "images.linuxcontainers.org"
	rootfsLinuxContainersLegacyDir         = "linux-containers"
	rootfsUploadsDirectory                 = "uploads"
	rootfsExportsDirectory                 = "exports"
	rootfsRepositoryDirectoryPrefix        = "repository-"
	maxCloudInitDocumentBytes              = 64 << 10
	cloudInitNATPrefix                     = 16
	cloudInitNATGateway                    = "172.28.0.1"
)

var rootfsStorageComponentUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

type rootfsRepositoriesRequest struct {
	Repositories []config.RootfsRepository `json:"repositories"`
}

type networkSettingsRequest struct {
	DefaultNATCIDR       string `json:"defaultNatCIDR"`
	DefaultNATThirdOctet int    `json:"defaultNatThirdOctet"`
}

type uiLanguageRequest struct {
	UILanguage string `json:"uiLanguage"`
}

type systemSettingsRequest struct {
	Mode                      string                    `json:"mode"`
	Host                      string                    `json:"host"`
	Port                      int                       `json:"port"`
	AuthToken                 string                    `json:"authToken"`
	UILanguage                string                    `json:"uiLanguage"`
	DroidspacesPath           string                    `json:"droidspacesPath"`
	CorePath                  string                    `json:"corePath"`
	ImageRoot                 string                    `json:"imageRoot"`
	TemplateImageRoot         string                    `json:"templateImageRoot"`
	Workspace                 string                    `json:"workspace"`
	SocketdEnabled            *bool                     `json:"socketdEnabled"`
	RootfsSkipTLSVerify       *bool                     `json:"rootfsSkipTLSVerify"`
	DefaultNATCIDR            string                    `json:"defaultNatCIDR"`
	DefaultNATThirdOctet      int                       `json:"defaultNatThirdOctet"`
	NestedAndroidNATCompat    *bool                     `json:"nestedAndroidNatCompat"`
	BatteryDirectPower        *bool                     `json:"batteryDirectPowerSupported"`
	BatterySeriesCells        *int                      `json:"batterySeriesCells"`
	OverviewPowerEnabled      *bool                     `json:"overviewPowerEnabled"`
	BatteryMonitoringEnabled  *bool                     `json:"batteryMonitoringEnabled"`
	BatteryDetailEnabled      *bool                     `json:"batteryDetailEnabled"`
	BatteryStatsSampleSecs    int                       `json:"batteryStatsSampleSeconds"`
	BatteryStatsWriteMins     int                       `json:"batteryStatsWriteMinutes"`
	BatteryStatsRetentionDays int                       `json:"batteryStatsRetentionDays"`
	OverviewRefreshSecs       int                       `json:"overviewRefreshSeconds"`
	RootfsRepositories        []config.RootfsRepository `json:"rootfsRepositories"`
}

type normalizedSystemSettings struct {
	Mode                      string
	Host                      string
	Port                      int
	AuthToken                 string
	UILanguage                string
	DroidspacesPath           string
	CorePath                  string
	ImageRoot                 string
	TemplateImageRoot         string
	Workspace                 string
	SocketdEnabled            bool
	RootfsSkipTLSVerify       bool
	DefaultNATCIDR            string
	DefaultNATThirdOctet      int
	NestedAndroidNATCompat    bool
	BatteryDirectPower        bool
	BatterySeriesCells        int
	OverviewPowerEnabled      bool
	BatteryMonitoringEnabled  bool
	BatteryDetailEnabled      bool
	BatteryStatsSampleSecs    int
	BatteryStatsWriteMins     int
	BatteryStatsRetentionDays int
	OverviewRefreshSecs       int
	RootfsRepositories        []config.RootfsRepository
}

type sparseRequest struct {
	SizeGB int `json:"sizeGb"`
}

type containerUser struct {
	Name  string `json:"name"`
	UID   string `json:"uid,omitempty"`
	GID   string `json:"gid,omitempty"`
	Home  string `json:"home,omitempty"`
	Shell string `json:"shell,omitempty"`
}

type serviceInfo struct {
	Name        string `json:"name"`
	Manager     string `json:"manager,omitempty"`
	State       string `json:"state,omitempty"`
	EnableState string `json:"enableState,omitempty"`
	Enabled     bool   `json:"enabled"`
	Running     bool   `json:"running"`
	Description string `json:"description,omitempty"`
}

type bootPriorityUpdateRequest struct {
	Names []string `json:"names"`
}

type systemdOverrideRequest struct {
	Content string `json:"content"`
}

type systemdUnitInspection struct {
	Unit         string            `json:"unit"`
	Properties   map[string]string `json:"properties"`
	StatusText   string            `json:"statusText"`
	Dependencies []string          `json:"dependencies"`
}

type diagnosticsSettingsRequest struct {
	DaemonMode     *bool `json:"daemonMode"`
	SymlinkEnabled *bool `json:"symlinkEnabled"`
}

type backendDiagnosticEntry struct {
	Time    int64  `json:"time"`
	Source  string `json:"source"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// webUILogResponse is deliberately limited to the WebUI process log. The API
// never accepts a filename so it cannot be used to browse workspace files.
type webUILogResponse struct {
	Path          string   `json:"path"`
	Exists        bool     `json:"exists"`
	SizeBytes     int64    `json:"sizeBytes"`
	ModifiedAt    int64    `json:"modifiedAt,omitempty"`
	Tail          int      `json:"tail"`
	ReturnedLines int      `json:"returnedLines"`
	Truncated     bool     `json:"truncated"`
	Lines         []string `json:"lines"`
}

type cpuSample struct {
	Idle  uint64
	Total uint64
}

type memoryReport struct {
	Used    uint64  `json:"used"`
	Total   uint64  `json:"total"`
	Percent float64 `json:"percent"`
}

type networkReport struct {
	RxBytes uint64 `json:"rxBytes"`
	TxBytes uint64 `json:"txBytes"`
	IO      string `json:"io"`
}

type batteryReport struct {
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Status    string `json:"status"`
	// PowerMode is a normalized, mutually exclusive view of the device's
	// current power path: charging, discharging, direct, external, idle, or
	// unknown. The raw kernel status remains available in Status.
	PowerMode           string  `json:"powerMode"`
	BatteryDirection    string  `json:"batteryDirection"`
	PowerModeSource     string  `json:"powerModeSource,omitempty"`
	ExternalPowerActive bool    `json:"externalPowerActive"`
	DirectPowerActive   bool    `json:"directPowerActive"`
	SignedCurrentMA     float64 `json:"signedCurrentMa"`
	SignedPowerW        float64 `json:"signedPowerW"`
	HasSignedCurrent    bool    `json:"hasSignedCurrent"`
	HasSignedPower      bool    `json:"hasSignedPower"`
	CapacityPercent     float64 `json:"capacityPercent"`
	CurrentMA           float64 `json:"currentMa"`
	AbsCurrentMA        float64 `json:"absCurrentMa"`
	VoltageV            float64 `json:"voltageV"`
	PowerW              float64 `json:"powerW"`
	AbsPowerW           float64 `json:"absPowerW"`
	ChargeMah           float64 `json:"chargeMah"`
	FullChargeMah       float64 `json:"fullChargeMah"`
	DesignChargeMah     float64 `json:"designChargeMah"`
	EnergyWh            float64 `json:"energyWh"`
	FullEnergyWh        float64 `json:"fullEnergyWh"`
	DesignEnergyWh      float64 `json:"designEnergyWh"`
	HealthPercent       float64 `json:"healthPercent"`
	InputCurrentMA      float64 `json:"inputCurrentMa"`
	InputVoltageV       float64 `json:"inputVoltageV"`
	InputPowerW         float64 `json:"inputPowerW"`
	// ChargingPowerW is the positive battery-side power while the pack is
	// charging. BoardPowerEstimateW is the remaining external input after that
	// battery-side charge power; it necessarily includes conversion losses.
	ChargingPowerW      float64 `json:"chargingPowerW"`
	BoardPowerEstimateW float64 `json:"boardPowerEstimateW"`
	// InputPowerKind describes how InputPowerW was obtained. A PD contract is
	// useful diagnostic information, but is not a measurement of device load.
	InputPowerKind        string             `json:"inputPowerKind,omitempty"`
	InputOnline           bool               `json:"inputOnline"`
	TemperatureC          float64            `json:"temperatureC"`
	HasCapacity           bool               `json:"hasCapacity"`
	HasCurrent            bool               `json:"hasCurrent"`
	HasVoltage            bool               `json:"hasVoltage"`
	HasPower              bool               `json:"hasPower"`
	HasCharge             bool               `json:"hasCharge"`
	HasFullCharge         bool               `json:"hasFullCharge"`
	HasDesignCharge       bool               `json:"hasDesignCharge"`
	HasEnergy             bool               `json:"hasEnergy"`
	HasFullEnergy         bool               `json:"hasFullEnergy"`
	HasDesignEnergy       bool               `json:"hasDesignEnergy"`
	HasHealth             bool               `json:"hasHealth"`
	HasInputCurrent       bool               `json:"hasInputCurrent"`
	HasInputVoltage       bool               `json:"hasInputVoltage"`
	HasInputPower         bool               `json:"hasInputPower"`
	HasChargingPower      bool               `json:"hasChargingPower"`
	HasBoardPowerEstimate bool               `json:"hasBoardPowerEstimate"`
	HasTemperature        bool               `json:"hasTemperature"`
	CurrentSource         string             `json:"currentSource,omitempty"`
	VoltageSource         string             `json:"voltageSource,omitempty"`
	PowerSource           string             `json:"powerSource,omitempty"`
	InputSource           string             `json:"inputSource,omitempty"`
	Summary               string             `json:"summary"`
	Stats                 batteryStatsReport `json:"stats"`
}

type batteryStatsReport struct {
	SampleCount               int     `json:"sampleCount"`
	LastSampleTime            int64   `json:"lastSampleTime,omitempty"`
	LastWriteTime             int64   `json:"lastWriteTime,omitempty"`
	MinSampleIntervalSeconds  int     `json:"minSampleIntervalSeconds"`
	SamplerIntervalSeconds    int     `json:"samplerIntervalSeconds"`
	WriteIntervalSeconds      int     `json:"writeIntervalSeconds"`
	PendingSampleCount        int     `json:"pendingSampleCount"`
	ChargeWh                  float64 `json:"chargeWh"`
	DischargeWh               float64 `json:"dischargeWh"`
	InputWh                   float64 `json:"inputWh"`
	ChargeMah                 float64 `json:"chargeMah"`
	DischargeMah              float64 `json:"dischargeMah"`
	EstimatedRemainingWh      float64 `json:"estimatedRemainingWh"`
	EstimatedUsableWh         float64 `json:"estimatedUsableWh"`
	EstimatedHealthPercent    float64 `json:"estimatedHealthPercent"`
	RuntimeHours              float64 `json:"runtimeHours"`
	RemainingSource           string  `json:"remainingSource,omitempty"`
	HasEstimatedRemainingWh   bool    `json:"hasEstimatedRemainingWh"`
	HasEstimatedUsableWh      bool    `json:"hasEstimatedUsableWh"`
	HasEstimatedHealthPercent bool    `json:"hasEstimatedHealthPercent"`
	HasRuntime                bool    `json:"hasRuntime"`
	CurrentPowerW             float64 `json:"currentPowerW,omitempty"`
	CurrentInputPowerW        float64 `json:"currentInputPowerW,omitempty"`
	DatabaseMode              string  `json:"databaseMode,omitempty"`
	StorageError              string  `json:"storageError,omitempty"`
	Message                   string  `json:"message,omitempty"`
}

type batteryPowerRangeReport struct {
	Enabled              bool                      `json:"enabled"`
	From                 int64                     `json:"from"`
	To                   int64                     `json:"to"`
	Hours                int                       `json:"hours"`
	SampleCount          int                       `json:"sampleCount"`
	BatterySampleCount   int                       `json:"batterySampleCount"`
	DischargeSampleCount int                       `json:"dischargeSampleCount"`
	ChargeSampleCount    int                       `json:"chargeSampleCount"`
	InputSampleCount     int                       `json:"inputSampleCount"`
	AvgDischargeW        float64                   `json:"avgDischargeW,omitempty"`
	MaxDischargeW        float64                   `json:"maxDischargeW,omitempty"`
	AvgChargeW           float64                   `json:"avgChargeW,omitempty"`
	MaxChargeW           float64                   `json:"maxChargeW,omitempty"`
	AvgInputW            float64                   `json:"avgInputW,omitempty"`
	MaxInputW            float64                   `json:"maxInputW,omitempty"`
	BatteryBins          []batteryPowerRangeBin    `json:"batteryBins"`
	InputBins            []batteryPowerRangeBin    `json:"inputBins"`
	ChartSamples         []batteryPowerRangeSample `json:"chartSamples"`
	RecentSamples        []batteryPowerRangeSample `json:"recentSamples"`
	Message              string                    `json:"message,omitempty"`
}

type batteryPowerRangeBin struct {
	Label   string  `json:"label"`
	MinW    float64 `json:"minW"`
	MaxW    float64 `json:"maxW"`
	Count   int     `json:"count"`
	Percent float64 `json:"percent"`
}

type batteryPowerRangeSample struct {
	Time             int64   `json:"time"`
	Status           string  `json:"status,omitempty"`
	PowerMode        string  `json:"powerMode,omitempty"`
	BatteryDirection string  `json:"batteryDirection,omitempty"`
	BatteryW         float64 `json:"batteryW,omitempty"`
	HasBattery       bool    `json:"hasBattery"`
	InputW           float64 `json:"inputW,omitempty"`
	HasInput         bool    `json:"hasInput"`
	Capacity         float64 `json:"capacityPercent,omitempty"`
	HasCapacity      bool    `json:"hasCapacity"`
}

type batteryStatsState struct {
	path                string
	loaded              bool
	sampleCount         int
	lastSample          batteryStatsSample
	hasLastSample       bool
	pendingSamples      []batteryStatsSample
	pendingSince        int64
	lastFlushTime       int64
	chargeWh            float64
	dischargeWh         float64
	inputWh             float64
	chargeMah           float64
	dischargeMah        float64
	usableWhWeightedSum float64
	usableWhWeight      float64
	trackedRemainingWh  float64
	hasTrackedRemaining bool
	trackedSource       string
	storageSignature    string
	storageError        string
}

type batteryStatsSample struct {
	Time             int64   `json:"time"`
	Status           string  `json:"status,omitempty"`
	PowerMode        string  `json:"powerMode,omitempty"`
	BatteryDirection string  `json:"batteryDirection,omitempty"`
	CapacityPercent  float64 `json:"capacityPercent,omitempty"`
	HasCapacity      bool    `json:"hasCapacity,omitempty"`
	CurrentMA        float64 `json:"currentMa,omitempty"`
	HasCurrent       bool    `json:"hasCurrent,omitempty"`
	VoltageV         float64 `json:"voltageV,omitempty"`
	HasVoltage       bool    `json:"hasVoltage,omitempty"`
	PowerW           float64 `json:"powerW,omitempty"`
	HasPower         bool    `json:"hasPower,omitempty"`
	InputPowerW      float64 `json:"inputPowerW,omitempty"`
	HasInputPower    bool    `json:"hasInputPower,omitempty"`
	ChargeMah        float64 `json:"chargeMah,omitempty"`
	HasCharge        bool    `json:"hasCharge,omitempty"`
	EnergyWh         float64 `json:"energyWh,omitempty"`
	HasEnergy        bool    `json:"hasEnergy,omitempty"`
	FullChargeMah    float64 `json:"fullChargeMah,omitempty"`
	HasFullCharge    bool    `json:"hasFullCharge,omitempty"`
	DesignChargeMah  float64 `json:"designChargeMah,omitempty"`
	HasDesignCharge  bool    `json:"hasDesignCharge,omitempty"`
	FullEnergyWh     float64 `json:"fullEnergyWh,omitempty"`
	HasFullEnergy    bool    `json:"hasFullEnergy,omitempty"`
	DesignEnergyWh   float64 `json:"designEnergyWh,omitempty"`
	HasDesignEnergy  bool    `json:"hasDesignEnergy,omitempty"`
	HealthPercent    float64 `json:"healthPercent,omitempty"`
	HasHealth        bool    `json:"hasHealth,omitempty"`
}

const defaultContainerCgroupRoot = "/sys/fs/cgroup/droidspaces"

var powerSupplyRoot = "/sys/class/power_supply"
var configuredBatterySeriesCells int

const (
	minRootfsImageSizeGB      = 4
	defaultRootfsImageSizeGB  = 8
	maxRootfsImageSizeGB      = 512
	batteryStatsFileName      = "battery_stats.jsonl"      // Legacy single-file store; never read by the daily store.
	batteryStatsDBFileName    = "battery_stats_state.json" // Legacy checkpoint; never read by the daily store.
	batteryStatsDirectoryName = "battery-stats"
)

const (
	batteryStatsDefaultSampleSeconds = 3
	batteryStatsMinSampleSeconds     = 1
	batteryStatsMaxSampleSeconds     = 60
	batteryStatsDefaultWriteMinutes  = 5
	batteryStatsMinWriteMinutes      = 5
	batteryStatsMaxWriteMinutes      = 1440
	batteryStatsDefaultRetentionDays = 7
	batteryStatsMinRetentionDays     = 1
	batteryStatsMaxRetentionDays     = 365
	batteryStatsMaxPowerRangeHours   = batteryStatsMaxRetentionDays * 24
	batteryStatsMaxSampleGap         = 15 * time.Minute
	batteryStatsMinPowerW            = 0.01
	batteryStatsMinCurrentMA         = 1
	// Input and battery telemetry are sampled independently. Permit a small
	// negative difference before treating the derived board power as invalid.
	batteryBoardPowerToleranceW         = 0.15
	batteryStatsMinCapacityDeltaPercent = 0.5
	batteryStatsMaxEstimatedUsableWh    = 500
)

const (
	defaultWebUILogTailLines = 200
	maxWebUILogTailLines     = 1000
	maxWebUILogReadBytes     = 512 << 10
)

var maxRootfsUploadBytes int64 = 16 << 30

type containerMemoryUsage struct {
	UsedKB      int64    `json:"usedKb"`
	TotalKB     int64    `json:"totalKb,omitempty"`
	UsedBytes   int64    `json:"usedBytes"`
	TotalBytes  int64    `json:"totalBytes,omitempty"`
	Percent     *float64 `json:"percent,omitempty"`
	AnonBytes   *int64   `json:"anonBytes,omitempty"`
	FileBytes   *int64   `json:"fileBytes,omitempty"`
	KernelBytes *int64   `json:"kernelBytes,omitempty"`
}

// containerDiskUsage matches the Android app's sparse-image measurement:
// UsedBytes is the host storage allocated to rootfs.img, while TotalBytes is
// the image's apparent capacity.
type containerDiskUsage struct {
	UsedBytes  int64    `json:"usedBytes"`
	TotalBytes int64    `json:"totalBytes,omitempty"`
	Percent    *float64 `json:"percent,omitempty"`
}

// containerDistroCacheEntry keeps the lightweight /etc/os-release lookup out
// of the regular container list refresh path. It is intentionally in-memory:
// a running container is rechecked after the WebUI restarts, just as the
// Android app refreshes its icon cache when a container starts.
type containerDistroCacheEntry struct {
	RootFSPath    string
	DistroName    string
	RootFSChecked bool
	LookupStarted bool
}

type containerView struct {
	socketd.Container
	DistroName        string                `json:"distroName,omitempty"`
	IPAddress         string                `json:"ipAddress,omitempty"`
	AllowUserNS       bool                  `json:"allowUserns"`
	RunAtBoot         bool                  `json:"runAtBoot"`
	RunAtBootPriority int                   `json:"runAtBootPriority,omitempty"`
	CPUUsage          *float64              `json:"cpuUsage,omitempty"`
	CPUPercent        *float64              `json:"cpuPercent,omitempty"`
	RAMUsedKB         *int64                `json:"ramUsedKb,omitempty"`
	RAMTotalKB        *int64                `json:"ramTotalKb,omitempty"`
	RAMUsageMB        *float64              `json:"ramUsageMb,omitempty"`
	RAMPercent        *float64              `json:"ramPercent,omitempty"`
	MemoryPercent     *float64              `json:"memoryPercent,omitempty"`
	Uptime            string                `json:"uptime,omitempty"`
	MemoryUsage       *containerMemoryUsage `json:"memoryUsage,omitempty"`
	MemoryUsageSource string                `json:"memoryUsageSource,omitempty"`
	CgroupMemoryUsage *containerMemoryUsage `json:"cgroupMemoryUsage,omitempty"`
	DiskUsage         *containerDiskUsage   `json:"diskUsage,omitempty"`
	UseSparseImage    bool                  `json:"useSparseImage"`
}

type containerUsageSnapshot struct {
	CPUUsage          *float64
	RAMUsedKB         *int64
	RAMTotalKB        *int64
	RAMPercent        *float64
	MemoryUsage       *containerMemoryUsage
	MemoryUsageSource string
	CgroupMemoryUsage *containerMemoryUsage
	Uptime            string
}

func newInspectResponse(inspect socketd.Inspect, source string) inspectResponse {
	resp := inspectResponse{
		containerView:     newContainerView(inspect.Container),
		ImageRef:          inspect.ImageRef,
		DNSServers:        inspect.DNSServers,
		MemoryLimit:       inspect.MemoryLimit,
		CPUQuota:          inspect.CPUQuota,
		CPUPeriod:         inspect.CPUPeriod,
		PidsLimit:         inspect.PidsLimit,
		PrivilegedMask:    inspect.PrivilegedMask,
		Foreground:        inspect.Foreground,
		VolatileMode:      inspect.VolatileMode,
		ForceCgroupV1:     inspect.ForceCgroupV1,
		DisableIPv6:       inspect.DisableIPv6,
		AndroidStorage:    inspect.AndroidStorage,
		SELinuxPermissive: inspect.SELinuxPermissive,
		HWAccess:          inspect.HWAccess,
		GPUMode:           inspect.GPUMode,
		TermuxX11:         inspect.TermuxX11,
		BlockNestedNS:     inspect.BlockNestedNS,
		IsImageMount:      inspect.IsImageMount,
		Env:               inspect.Env,
		EnvTotal:          inspect.EnvTotal,
		Binds:             inspect.Binds,
		BindTotal:         inspect.BindTotal,
		PortTotal:         inspect.PortTotal,
		Source:            source,
	}
	if resp.MemoryLimit > 0 {
		resp.MemoryLimitText = formatBytes(uint64(resp.MemoryLimit))
	}
	if resp.CPUQuota > 0 {
		resp.CPUsText = formatCPUs(resp.CPUQuota, resp.CPUPeriod)
	}
	return resp
}

func (resp inspectResponse) toSocketdInspect() socketd.Inspect {
	return socketd.Inspect{
		Container:         resp.Container,
		ImageRef:          resp.ImageRef,
		DNSServers:        resp.DNSServers,
		MemoryLimit:       resp.MemoryLimit,
		CPUQuota:          resp.CPUQuota,
		CPUPeriod:         resp.CPUPeriod,
		PidsLimit:         resp.PidsLimit,
		PrivilegedMask:    resp.PrivilegedMask,
		Foreground:        resp.Foreground,
		VolatileMode:      resp.VolatileMode,
		ForceCgroupV1:     resp.ForceCgroupV1,
		DisableIPv6:       resp.DisableIPv6,
		AndroidStorage:    resp.AndroidStorage,
		SELinuxPermissive: resp.SELinuxPermissive,
		HWAccess:          resp.HWAccess,
		GPUMode:           resp.GPUMode,
		TermuxX11:         resp.TermuxX11,
		BlockNestedNS:     resp.BlockNestedNS,
		IsImageMount:      resp.IsImageMount,
		Env:               resp.Env,
		EnvTotal:          resp.EnvTotal,
		Binds:             resp.Binds,
		BindTotal:         resp.BindTotal,
		PortTotal:         resp.PortTotal,
	}
}

func NewServer(opts Options) (*Server, error) {
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		workspace = config.Default().Workspace
	}
	path := strings.TrimSpace(opts.DroidspacesPath)
	if path == "" {
		path = filepath.Join(workspace, "bin", "droidspaces")
	}
	corePath := strings.TrimSpace(opts.CorePath)
	if corePath == "" {
		corePath = filepath.Dir(path)
	}
	templateImageRoot := strings.TrimSpace(opts.TemplateImageRoot)
	if templateImageRoot == "" {
		templateImageRoot = filepath.Join(workspace, "rootfs")
	}
	imageRoot := strings.TrimSpace(opts.ImageRoot)
	if imageRoot == "" {
		imageRoot = templateImageRoot
	}
	webVersion := strings.TrimSpace(opts.WebVersion)
	if webVersion == "" {
		webVersion = "dev"
	}
	supportedCoreVersion := strings.TrimSpace(opts.SupportedCoreVersion)
	if supportedCoreVersion == "" {
		supportedCoreVersion = DefaultSupportedCoreVersion
	}
	uiLanguage, err := config.NormalizeUILanguage(opts.UILanguage)
	if err != nil {
		return nil, err
	}

	defaultNATCIDR := strings.TrimSpace(opts.DefaultNATCIDR)
	if defaultNATCIDR == "" {
		defaultNATCIDR = config.DefaultNATCIDR
	}
	defaultNATThirdOctet := opts.DefaultNATThirdOctet
	if defaultNATThirdOctet <= 0 {
		defaultNATThirdOctet = config.DefaultNATThirdOctet
	}
	if defaultNATThirdOctet < 1 || defaultNATThirdOctet > 254 {
		return nil, fmt.Errorf("defaultNatThirdOctet must be between 1 and 254")
	}
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = config.ModeLocal
	}
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		if mode == config.ModePublic {
			host = "0.0.0.0"
		} else {
			host = "127.0.0.1"
		}
	}
	port := opts.Port
	if port <= 0 {
		port = 9090
	}
	overviewRefreshSecs := opts.OverviewRefreshSecs
	if overviewRefreshSecs <= 0 {
		overviewRefreshSecs = 3
	}
	batteryStatsSampleSecs := opts.BatteryStatsSampleSecs
	if batteryStatsSampleSecs <= 0 {
		batteryStatsSampleSecs = batteryStatsDefaultSampleSeconds
	}
	if batteryStatsSampleSecs < batteryStatsMinSampleSeconds || batteryStatsSampleSecs > batteryStatsMaxSampleSeconds {
		return nil, fmt.Errorf("batteryStatsSampleSeconds must be between %d and %d", batteryStatsMinSampleSeconds, batteryStatsMaxSampleSeconds)
	}
	batteryStatsWriteMins := opts.BatteryStatsWriteMins
	if batteryStatsWriteMins <= 0 {
		batteryStatsWriteMins = batteryStatsDefaultWriteMinutes
	}
	if batteryStatsWriteMins < batteryStatsMinWriteMinutes || batteryStatsWriteMins > batteryStatsMaxWriteMinutes {
		return nil, fmt.Errorf("batteryStatsWriteMinutes must be between %d and %d", batteryStatsMinWriteMinutes, batteryStatsMaxWriteMinutes)
	}
	batteryStatsRetentionDays := opts.BatteryStatsRetentionDays
	if batteryStatsRetentionDays <= 0 {
		batteryStatsRetentionDays = batteryStatsDefaultRetentionDays
	}
	if batteryStatsRetentionDays < batteryStatsMinRetentionDays || batteryStatsRetentionDays > batteryStatsMaxRetentionDays {
		return nil, fmt.Errorf("batteryStatsRetentionDays must be between %d and %d", batteryStatsMinRetentionDays, batteryStatsMaxRetentionDays)
	}
	batteryDetailEnabled := true
	if opts.BatteryDetailEnabled != nil {
		batteryDetailEnabled = *opts.BatteryDetailEnabled
	}
	overviewPowerEnabled := true
	if opts.OverviewPowerEnabled != nil {
		overviewPowerEnabled = *opts.OverviewPowerEnabled
	}
	batteryMonitoringEnabled := true
	if opts.BatteryMonitoringEnabled != nil {
		batteryMonitoringEnabled = *opts.BatteryMonitoringEnabled
	}
	if opts.BatterySeriesCells < 0 || opts.BatterySeriesCells > 6 {
		return nil, fmt.Errorf("batterySeriesCells must be 0(auto) or between 1 and 6")
	}
	configuredBatterySeriesCells = opts.BatterySeriesCells

	rootfsRepos := config.EnsureDefaultRootfsRepositoriesForUILanguage(opts.RootfsRepos, uiLanguage)
	srv := &Server{
		socketd:                     socketd.NewClient(6 * time.Second),
		coreUpdateHTTPClient:        newCoreUpdateHTTPClient(),
		socketdEnabled:              opts.SocketdEnabled,
		droidspacesPath:             path,
		webVersion:                  webVersion,
		supportedCoreVersion:        supportedCoreVersion,
		authToken:                   opts.AuthToken,
		uiLanguage:                  uiLanguage,
		uiLanguageConfigured:        opts.UILanguageConfigured,
		workspace:                   workspace,
		configPath:                  opts.ConfigPath,
		mode:                        mode,
		host:                        host,
		port:                        port,
		corePath:                    corePath,
		imageRoot:                   imageRoot,
		templateImageRoot:           templateImageRoot,
		rootfsRepos:                 rootfsRepos,
		rootfsReposConfigured:       opts.RootfsRepositoriesConfigured,
		rootfsClient:                rootfs.NewClient(opts.RootfsSkipTLSVerify),
		rootfsSkipTLSVerify:         opts.RootfsSkipTLSVerify,
		cgroupRoot:                  defaultContainerCgroupRoot,
		defaultNATCIDR:              defaultNATCIDR,
		defaultNATThirdOctet:        defaultNATThirdOctet,
		nestedAndroidNATCompat:      opts.NestedAndroidNATCompat,
		nestedAndroidNATScope:       nestedAndroidNATScopeForWorkspace(workspace),
		batteryDirectPower:          opts.BatteryDirectPower,
		batterySeriesCells:          opts.BatterySeriesCells,
		overviewPowerEnabled:        overviewPowerEnabled,
		batteryMonitoringEnabled:    batteryMonitoringEnabled,
		batteryDetailEnabled:        batteryDetailEnabled,
		batteryStatsSampleSecs:      int64(batteryStatsSampleSecs),
		batteryStatsWriteMins:       int64(batteryStatsWriteMins),
		batteryStatsRetentionDays:   int64(batteryStatsRetentionDays),
		overviewRefreshSecs:         overviewRefreshSecs,
		disableBatterySampler:       opts.DisableBatterySampler,
		disableRootfsCatalogRefresh: opts.DisableRootfsCatalogRefresh,
		natIPReservations:           map[string]string{},
		portForwardReservations:     map[string][]socketd.Port{},
		containerDistroCache:        map[string]containerDistroCacheEntry{},
		tasks:                       map[string]*taskState{},
		containerTasks:              map[string]string{},
		rootfsDownloads:             map[string]*sharedRootfsDownload{},
		rootfsDownloadRequests:      map[string]*rootfsDownloadRequestFlight{},
	}
	if !opts.DisableBatterySampler && batteryMonitoringEnabled {
		srv.startBatteryStatsSampler()
	}
	srv.startNestedAndroidNATCompatMonitor()
	srv.startRootfsCatalogRefreshScheduler()
	return srv, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/login", s.handleLogin)
	mux.HandleFunc("/api/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("/api/core/update", s.withAuth(s.handleCoreUpdate))
	mux.HandleFunc("/api/settings", s.withAuth(s.handleSystemSettings))
	mux.HandleFunc("/api/settings/ui-language", s.withAuth(s.handleUILanguageSettings))
	mux.HandleFunc("/api/containers", s.withAuth(s.handleContainers))
	mux.HandleFunc("/api/containers/", s.withAuth(s.handleContainer))
	mux.HandleFunc("/api/boot-priority", s.withAuth(s.handleBootPriority))
	mux.HandleFunc("/api/events", s.withAuth(s.handleEvents))
	mux.HandleFunc("/api/rootfs", s.withAuth(s.handleRootfsList))
	mux.HandleFunc("/api/rootfs/repositories", s.withAuth(s.handleRootfsRepositories))
	mux.HandleFunc("/api/rootfs/local", s.withAuth(s.handleLocalRootfsList))
	mux.HandleFunc("/api/rootfs/local/upload", s.withAuth(s.handleLocalRootfsUpload))
	mux.HandleFunc("/api/rootfs/local/delete", s.withAuth(s.handleLocalRootfsDelete))
	mux.HandleFunc("/api/rootfs/local/download", s.withAuth(s.handleLocalRootfsDownload))
	mux.HandleFunc("/api/rootfs/download", s.withAuth(s.handleRootfsDownload))
	mux.HandleFunc("/api/network/settings", s.withAuth(s.handleNetworkSettings))
	mux.HandleFunc("/api/diagnostics/backend", s.withAuth(s.handleBackendDiagnostics))
	mux.HandleFunc("/api/logs/webui", s.withAuth(s.handleWebUILog))
	mux.HandleFunc("/api/diagnostics/webui-log", s.withAuth(s.handleWebUILog))
	mux.HandleFunc("/api/diagnostics/requirements", s.withAuth(s.handleDiagnosticsRequirements))
	mux.HandleFunc("/api/diagnostics/bugreport", s.withAuth(s.handleDiagnosticsBugreport))
	mux.HandleFunc("/api/diagnostics/settings", s.withAuth(s.handleDiagnosticsSettings))
	mux.HandleFunc("/api/tasks", s.withAuth(s.handleTasks))
	mux.HandleFunc("/api/tasks/", s.withAuth(s.handleTask))
	mux.HandleFunc("/api/downloads/", s.withAuth(s.handleDownload))
	mux.HandleFunc("/api/host", s.withAuth(s.handleHost))
	mux.HandleFunc("/api/battery/power", s.withAuth(s.handleBatteryPower))
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
	authToken := s.currentAuthToken()
	if authToken == "" {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authEnabled": false})
		return
	}
	got := s.requestToken(r)
	if got != authToken {
		writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing or invalid auth token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authEnabled": true})
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authToken := s.currentAuthToken()
		if authToken != "" {
			got := s.requestToken(r)
			if got != authToken {
				writeJSON(w, http.StatusUnauthorized, apiError{Error: "missing or invalid auth token"})
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) currentAuthToken() string {
	s.systemSettingsMu.RLock()
	defer s.systemSettingsMu.RUnlock()
	return s.authToken
}

func (s *Server) requestToken(r *http.Request) string {
	if got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "); got != "" {
		return got
	}
	return r.URL.Query().Get("token")
}

func (s *Server) injectIndexConfig(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The embedded UI is updated in place on Android, so always revalidate it.
		w.Header().Set("Cache-Control", "no-cache")
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
		s.systemSettingsMu.RLock()
		authRequired := s.authToken != ""
		uiLanguage := s.uiLanguage
		uiLanguageConfigured := s.uiLanguageConfigured
		s.systemSettingsMu.RUnlock()
		configScript := fmt.Sprintf(`<script>window.DS_AUTH_REQUIRED = %t; window.DS_UI_LANGUAGE_DEFAULT = %s; window.DS_UI_LANGUAGE_CONFIGURED = %t;</script>`, authRequired, strconv.Quote(uiLanguage), uiLanguageConfigured)
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

	if r.URL.Query().Get("refreshCoreVersion") == "1" {
		s.invalidateCoreVersionCache()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	versionCtx, versionCancel := context.WithTimeout(r.Context(), 3*time.Second)
	s.systemSettingsMu.RLock()
	coreVersion := s.runtimeCoreVersion(versionCtx)
	s.systemSettingsMu.RUnlock()
	versionCancel()

	s.systemSettingsMu.RLock()
	s.rootfsCacheMu.Lock()
	rootfsRepoCount := len(s.rootfsRepos)
	s.rootfsCacheMu.Unlock()
	socketdEnabled := s.socketdEnabled
	workspacePath := s.workspace
	status := map[string]any{
		"backend":                     "unreachable",
		"mode":                        s.mode,
		"host":                        s.host,
		"port":                        s.port,
		"configPath":                  s.configPath,
		"droidspacesPath":             s.droidspacesPath,
		"webVersion":                  s.webVersion,
		"coreVersion":                 coreVersion,
		"supportedCoreVersion":        s.supportedCoreVersion,
		"corePath":                    s.corePath,
		"imageRoot":                   s.imageRoot,
		"templateImageRoot":           s.templateImageRoot,
		"rootfsRepoCount":             rootfsRepoCount,
		"rootfsSkipTLSVerify":         s.rootfsSkipTLSVerify,
		"isAndroid":                   config.IsAndroid(),
		"defaultNatCIDR":              s.defaultNATCIDR,
		"defaultNatThirdOctet":        s.defaultNATThirdOctet,
		"batteryDirectPowerSupported": s.batteryDirectPower,
		"overviewPowerEnabled":        s.overviewPowerEnabledSetting(),
		"batteryMonitoringEnabled":    s.batteryMonitoringEnabledSetting(),
		"batteryDetailEnabled":        s.batteryDetailEnabled,
		"batteryStatsSampleSeconds":   s.batteryStatsSampleSeconds(),
		"batteryStatsWriteMinutes":    s.batteryStatsWriteMinutes(),
		"batteryStatsRetentionDays":   s.batteryStatsRetentionDaysSetting(),
		"socketdEnabled":              s.socketdEnabled,
		"workspace":                   s.workspace,
		"authEnabled":                 s.authToken != "",
		"listenHint":                  "local 模式仅监听本机；public 模式建议配置固定 authToken",
		"backendErrors":               s.backendDiagnosticLog(),
	}
	s.systemSettingsMu.RUnlock()

	if !socketdEnabled {
		status["backend"] = "socketd-disabled"
		if snap, snapErr := workspace.ReadSnapshot(workspacePath, true); snapErr == nil {
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else {
			status["fallbackError"] = snapErr.Error()
		}
		writeJSON(w, http.StatusOK, status)
		return
	}

	if err := s.socketd.Ping(ctx); err != nil {
		s.recordBackendDiagnostic("status/socketd-ping", err)
		status["backendError"] = err.Error()
		status["backendErrorHint"] = backendErrorHint(err)
		s.systemSettingsMu.RLock()
		snap, cliErr := s.cliSnapshot(ctx, true)
		s.systemSettingsMu.RUnlock()
		if cliErr == nil {
			status["backend"] = "cli-fallback"
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else if snap, snapErr := workspace.ReadSnapshot(workspacePath, true); snapErr == nil {
			status["backend"] = "workspace-fallback"
			status["info"] = snap.Info
			status["fallbackSource"] = snap.Source
		} else {
			s.recordBackendDiagnostic("status/fallback", snapErr)
			status["fallbackError"] = snapErr.Error()
		}
		status["backendErrors"] = s.backendDiagnosticLog()
		writeJSON(w, http.StatusOK, status)
		return
	}
	status["backend"] = "ready"

	if caps, err := s.socketd.Capabilities(ctx); err == nil {
		status["capabilities"] = caps
	}
	if info, err := s.socketd.Info(ctx); err == nil {
		status["info"] = info
	} else if snap, snapErr := workspace.ReadSnapshot(workspacePath, true); snapErr == nil {
		status["info"] = snap.Info
		status["fallbackSource"] = snap.Source
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleCoreUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		info, err := s.coreUpdateInfo(ctx)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, info)
	case http.MethodPost:
		s.coreUpdateMu.Lock()
		if s.coreUpdateRunning {
			s.coreUpdateMu.Unlock()
			writeJSON(w, http.StatusConflict, apiError{Error: "a core update is already running"})
			return
		}
		s.coreUpdateRunning = true
		task := s.newTask("core-update", "Droidspaces core")
		s.updateTask(task.ID, func(t *taskState) { t.Status = "running" })
		s.coreUpdateMu.Unlock()

		go func(taskID string) {
			defer func() {
				s.coreUpdateMu.Lock()
				s.coreUpdateRunning = false
				s.coreUpdateMu.Unlock()
			}()
			s.runCoreUpdateTask(taskID)
		}(task.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) coreUpdateInfo(ctx context.Context) (map[string]any, error) {
	s.invalidateCoreVersionCache()
	versionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	currentVersion := s.runtimeCoreVersion(versionCtx)
	cancel()

	release, err := s.fetchLatestCoreRelease(ctx)
	if err != nil {
		return nil, err
	}
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	asset, err := selectCoreUpdateAsset(release, architecture)
	if err != nil {
		return nil, err
	}
	updateAvailable, versionComparable := coreUpdateAvailable(currentVersion, release.TagName)
	var updateStatus any
	if versionComparable {
		updateStatus = updateAvailable
	}

	return map[string]any{
		"status":          "ready",
		"currentVersion":  currentVersion,
		"latestVersion":   release.TagName,
		"latestName":      release.Name,
		"publishedAt":     release.PublishedAt,
		"releaseURL":      release.HTMLURL,
		"architecture":    architecture,
		"assetName":       asset.Name,
		"assetSize":       asset.Size,
		"assetDigest":     asset.Digest,
		"source":          "GitHub 官方 Release",
		"updateAvailable": updateStatus,
		"asset": map[string]any{
			"name":   asset.Name,
			"size":   asset.Size,
			"digest": asset.Digest,
		},
	}, nil
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

func (s *Server) handleBootPriority(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		containers, err := s.autoBootContainers()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"containers": containers})
	case http.MethodPut:
		var req bootPriorityUpdateRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		s.containerConfigMu.Lock()
		defer s.containerConfigMu.Unlock()
		containers, err := s.autoBootContainers()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		if err := validateBootPriorityOrder(req.Names, containers); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
			return
		}
		if err := s.saveBootPriorityOrder(req.Names); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		containers, err = s.autoBootContainers()
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "containers": containers})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) autoBootContainers() ([]containerView, error) {
	snap, err := workspace.ReadSnapshot(s.workspace, true)
	if err != nil {
		return nil, err
	}
	containers := make([]containerView, 0)
	for _, container := range snap.Containers {
		view := newContainerView(container)
		if configPath, ok := s.containerConfigPath(view.Name); ok {
			view.applyContainerConfigState(readContainerConfigValues(configPath))
		}
		if view.RunAtBoot {
			containers = append(containers, view)
		}
	}
	sort.SliceStable(containers, func(i, j int) bool {
		left, right := containers[i].RunAtBootPriority, containers[j].RunAtBootPriority
		maxPriority := int(^uint(0) >> 1)
		if left <= 0 {
			left = maxPriority
		}
		if right <= 0 {
			right = maxPriority
		}
		if left != right {
			return left < right
		}
		return strings.ToLower(containers[i].Name) < strings.ToLower(containers[j].Name)
	})
	return containers, nil
}

func validateBootPriorityOrder(names []string, containers []containerView) error {
	if len(names) != len(containers) {
		return fmt.Errorf("boot priority order must include every enabled container")
	}
	expected := make(map[string]bool, len(containers))
	for _, container := range containers {
		expected[container.Name] = true
	}
	seen := make(map[string]bool, len(names))
	for _, raw := range names {
		name, err := cleanTarget(strings.TrimSpace(raw))
		if err != nil || hasConfigUnsafeChars(name) {
			return fmt.Errorf("invalid container name in boot priority order")
		}
		if !expected[name] {
			return fmt.Errorf("%q is not enabled for boot", name)
		}
		if seen[name] {
			return fmt.Errorf("boot priority order contains %q more than once", name)
		}
		seen[name] = true
	}
	return nil
}

func (s *Server) saveBootPriorityOrder(names []string) error {
	original := make(map[string][]byte, len(names))
	paths := make(map[string]string, len(names))
	for _, name := range names {
		path, ok := s.containerConfigPath(name)
		if !ok {
			return fmt.Errorf("container config for %q was not found", name)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		paths[name] = path
		original[name] = data
	}
	for index, name := range names {
		if err := workspace.UpdateContainerConfig(paths[name], map[string]string{"run_at_boot_priority": strconv.Itoa(index + 1)}); err != nil {
			for _, restoredName := range names[:index] {
				_ = os.WriteFile(paths[restoredName], original[restoredName], 0644)
			}
			return err
		}
	}
	return nil
}

func (s *Server) listContainers(w http.ResponseWriter, r *http.Request) {
	includeAll := r.URL.Query().Get("all") != "0"
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if !s.socketdEnabled {
		snap, snapErr := workspace.ReadSnapshot(s.workspace, includeAll)
		if snapErr != nil {
			s.recordBackendDiagnostic("containers/workspace", snapErr)
			writeBackendError(w, snapErr)
			return
		}
		writeJSON(w, http.StatusOK, s.containerListResponse(ctx, snap, nil))
		return
	}

	var socketdErr error
	containers, err := s.socketd.ListContainers(ctx, includeAll)
	if err == nil {
		containers = s.mergeWorkspaceContainers(containers, includeAll)
		writeJSON(w, http.StatusOK, map[string]any{"containers": s.enrichContainerViews(ctx, containers), "source": "socketd"})
		return
	}
	socketdErr = err
	s.recordBackendDiagnostic("containers/socketd-list", socketdErr)

	if snap, cliErr := s.cliSnapshot(ctx, includeAll); cliErr == nil {
		writeJSON(w, http.StatusOK, s.containerListResponse(ctx, snap, socketdErr))
		return
	}

	snap, snapErr := workspace.ReadSnapshot(s.workspace, includeAll)
	if snapErr != nil {
		s.recordBackendDiagnostic("containers/fallback", fallbackError(socketdErr, snapErr))
		writeBackendError(w, fallbackError(socketdErr, snapErr))
		return
	}
	writeJSON(w, http.StatusOK, s.containerListResponse(ctx, snap, socketdErr))
}

func (s *Server) containerListResponse(ctx context.Context, snap workspace.Snapshot, backendErr error) map[string]any {
	resp := map[string]any{"containers": s.enrichContainerViews(ctx, snap.Containers), "source": snap.Source, "info": snap.Info}
	if backendErr != nil {
		resp["backendError"] = backendErr.Error()
		resp["backendErrorHint"] = backendErrorHint(backendErr)
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

	if parts[1] == "config" {
		s.updateContainerConfig(w, r, target)
		return
	}

	if parts[1] == "users" {
		s.handleContainerUsers(w, r, target)
		return
	}

	if parts[1] == "services" {
		s.handleContainerServices(w, r, target, parts[2:])
		return
	}

	if parts[1] == "sparse" {
		s.handleSparseAction(w, r, target, parts[2:])
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
	case "network-diagnose", "network-diagnostics":
		s.networkDiagnoseContainer(w, r, target)
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
		resp := newInspectResponse(fallback, "workspace")
		if configPath, ok := s.containerConfigPath(target); ok {
			resp.applyConfigValues(readContainerConfigValues(configPath))
		}
		s.enrichContainerView(ctx, &resp.containerView)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	var socketdErr error
	inspect, err := s.socketd.InspectContainer(ctx, target)
	if err == nil {
		resp := newInspectResponse(inspect, "socketd")
		if configPath, ok := s.containerConfigPath(target); ok {
			resp.applyConfigValues(readContainerConfigValues(configPath))
		}
		s.enrichContainerView(ctx, &resp.containerView)
		writeJSON(w, http.StatusOK, resp)
		return
	}
	socketdErr = err
	s.recordBackendDiagnostic("inspect/socketd", socketdErr)
	if fallback, cliErr := s.inspectViaCLI(ctx, target); cliErr == nil {
		if socketdErr != nil {
			fallback.BackendError = socketdErr.Error()
		}
		if configPath, ok := s.containerConfigPath(target); ok {
			fallback.applyConfigValues(readContainerConfigValues(configPath))
		}
		s.enrichContainerView(ctx, &fallback.containerView)
		writeJSON(w, http.StatusOK, fallback)
		return
	}
	fallback, fallbackErr := workspace.Inspect(s.workspace, target)
	if fallbackErr != nil {
		s.recordBackendDiagnostic("inspect/fallback", fallbackError(socketdErr, fallbackErr))
		writeBackendError(w, fallbackError(socketdErr, fallbackErr))
		return
	}
	resp := newInspectResponse(fallback, "workspace")
	if configPath, ok := s.containerConfigPath(target); ok {
		resp.applyConfigValues(readContainerConfigValues(configPath))
	}
	if socketdErr != nil {
		resp.BackendError = socketdErr.Error()
	}
	s.enrichContainerView(ctx, &resp.containerView)
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
	if r.URL.Query().Get("async") == "1" {
		task, release, err := s.beginContainerTask("container-"+action, target)
		if err != nil {
			writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
			return
		}
		s.updateTask(task.ID, func(t *taskState) {
			t.Status = "running"
			t.Percent = 1
		})
		task, _ = s.getTask(task.ID)
		go s.runLifecycleTask(task.ID, target, action, timeoutSeconds, release)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":     true,
			"action": action,
			"target": target,
			"taskId": task.ID,
			"task":   task,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(timeoutSeconds+40)*time.Second)
	defer cancel()
	source, output, err := s.performLifecycle(ctx, target, action, timeoutSeconds)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action, "target": target, "source": source, "output": output})
}

func (s *Server) runLifecycleTask(taskID string, target string, action string, timeoutSeconds int, release func()) {
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds+40)*time.Second)
	defer cancel()

	s.appendTaskLog(taskID, "Submitting container "+action+" operation...")
	s.updateTask(taskID, func(t *taskState) { t.Percent = 20 })
	source, output, err := s.performLifecycle(ctx, target, action, timeoutSeconds)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	if output = strings.TrimSpace(output); output != "" {
		s.appendTaskLog(taskID, output)
	}
	s.appendTaskLog(taskID, "Container "+action+" completed via "+source+".")
	s.completeTask(taskID, "", "")
}

func (s *Server) performLifecycle(ctx context.Context, target string, action string, timeoutSeconds int) (string, string, error) {
	var socketdErr error
	if s.socketdEnabled {
		switch action {
		case "start":
			socketdErr = s.socketd.StartContainer(ctx, target)
		case "stop":
			socketdErr = s.socketd.StopContainer(ctx, target, timeoutSeconds)
		case "restart":
			socketdErr = s.socketd.RestartContainer(ctx, target, timeoutSeconds)
		default:
			return "", "", fmt.Errorf("unsupported lifecycle action %q", action)
		}
		if socketdErr == nil {
			s.resetContainerDistroOnStart(target, action)
			s.reconcileNestedAndroidNATCompatAsync()
			return "socketd", "", nil
		}
		s.recordBackendDiagnostic("lifecycle/socketd", socketdErr)
	}

	result, cliErr := s.lifecycleViaCLI(ctx, target, action)
	if cliErr != nil {
		message := fmt.Sprintf("cli: %v\n%s", cliErr, result.Output)
		if socketdErr != nil {
			message = fmt.Sprintf("socketd: %v; %s", socketdErr, message)
		}
		s.recordBackendDiagnostic("lifecycle/cli", fmt.Errorf("%s", message))
		return "", result.Output, fmt.Errorf("%s", message)
	}
	s.resetContainerDistroOnStart(target, action)
	s.reconcileNestedAndroidNATCompatAsync()
	return "cli", result.Output, nil
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
		s.recordBackendDiagnostic("events/socketd-poll", err)
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

type githubCoreRelease struct {
	TagName     string                   `json:"tag_name"`
	Name        string                   `json:"name"`
	HTMLURL     string                   `json:"html_url"`
	PublishedAt string                   `json:"published_at"`
	Assets      []githubCoreReleaseAsset `json:"assets"`
}

type githubCoreReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
	Size               int64  `json:"size"`
	ContentType        string `json:"content_type"`
}

type coreUpdateTarget struct {
	path         string
	mode         fs.FileMode
	selinuxLabel []byte
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

const (
	DefaultSupportedCoreVersion = "v6.5.0"
	coreVersionCacheTTL         = time.Minute

	officialCoreReleaseAPIURL             = "https://api.github.com/repos/ravindu644/Droidspaces-OSS/releases/latest"
	officialCoreReleaseDownloadPath       = "/ravindu644/Droidspaces-OSS/releases/download/"
	coreUpdateMaxMetadataBytes      int64 = 4 << 20
	coreUpdateMaxArchiveBytes       int64 = 128 << 20
	coreUpdateMaxBinaryBytes        int64 = 64 << 20
	coreUpdateMaxArchiveEntries           = 256
	coreUpdateHTTPTimeout                 = 15 * time.Minute
	rootfsMetadataTaskTimeout             = 45 * time.Second
	rootfsDownloadTaskTimeout             = 6 * time.Hour
)

func (s *Server) handleRootfsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	arch := r.URL.Query().Get("arch")
	refresh := strings.TrimSpace(r.URL.Query().Get("refresh")) == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()

	result := s.cachedRootfsList(ctx, arch, refresh)
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":               result.Assets,
		"errors":               result.Errors,
		"templateImageRoot":    result.TemplateImageRoot,
		"repositories":         result.Repositories,
		"cache":                result.Cache,
		"defaultNatCIDR":       s.defaultNATCIDR,
		"defaultNatThirdOctet": s.defaultNATThirdOctet,
	})
}

func (s *Server) handleSystemSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.systemSettingsMu.RLock()
		response := s.systemSettingsResponse(false, false)
		s.systemSettingsMu.RUnlock()
		writeJSON(w, http.StatusOK, response)
	case http.MethodPut, http.MethodPost:
		var req systemSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		s.systemSettingsMu.Lock()
		defer s.systemSettingsMu.Unlock()
		settings, err := s.normalizeSystemSettings(req)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
			return
		}
		restartRequired := settings.Mode != s.mode || settings.Host != s.host || settings.Port != s.port
		if err := s.persistWebConfig(func(data map[string]any) {
			data["mode"] = settings.Mode
			data["host"] = settings.Host
			data["port"] = settings.Port
			data["authToken"] = settings.AuthToken
			data["uiLanguage"] = settings.UILanguage
			data["droidspacesPath"] = settings.DroidspacesPath
			data["corePath"] = settings.CorePath
			data["imageRoot"] = settings.ImageRoot
			data["templateImageRoot"] = settings.TemplateImageRoot
			data["workspace"] = settings.Workspace
			data["socketdEnabled"] = settings.SocketdEnabled
			data["rootfsSkipTLSVerify"] = settings.RootfsSkipTLSVerify
			data["defaultNatCIDR"] = settings.DefaultNATCIDR
			data["defaultNatThirdOctet"] = settings.DefaultNATThirdOctet
			data["nestedAndroidNatCompat"] = settings.NestedAndroidNATCompat
			data["batteryDirectPowerSupported"] = settings.BatteryDirectPower
			data["batterySeriesCells"] = settings.BatterySeriesCells
			data["overviewPowerEnabled"] = settings.OverviewPowerEnabled
			data["batteryMonitoringEnabled"] = settings.BatteryMonitoringEnabled
			data["batteryDetailEnabled"] = settings.BatteryDetailEnabled
			data["batteryStatsSampleSeconds"] = settings.BatteryStatsSampleSecs
			data["batteryStatsWriteMinutes"] = settings.BatteryStatsWriteMins
			data["batteryStatsRetentionDays"] = settings.BatteryStatsRetentionDays
			data["overviewRefreshSeconds"] = settings.OverviewRefreshSecs
			data["rootfsRepositories"] = settings.RootfsRepositories
			delete(data, "defaultNatUpstreamIfname")
			delete(data, "defaultNatUpstreamIfnames")
			delete(data, "natUpstreamIfname")
			delete(data, "natUpstreamIfnames")
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		s.applySystemSettings(settings, restartRequired)
		writeJSON(w, http.StatusOK, s.systemSettingsResponse(true, restartRequired))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) handleUILanguageSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	if strings.TrimSpace(s.configPath) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "configuration path is not configured"})
		return
	}

	var req uiLanguageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	s.systemSettingsMu.Lock()
	defer s.systemSettingsMu.Unlock()
	uiLanguage, err := config.NormalizeUILanguage(req.UILanguage)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	s.rootfsCacheMu.RLock()
	repositories := append([]config.RootfsRepository(nil), s.rootfsRepos...)
	repositoriesConfigured := s.rootfsReposConfigured
	s.rootfsCacheMu.RUnlock()
	if repositoriesConfigured {
		repositories = config.EnsureDefaultRootfsRepositoriesForUILanguage(repositories, uiLanguage)
	} else {
		// The source was inherited from the old/default configuration rather
		// than selected by the user, so replace only that managed source with
		// the default for the language just chosen.
		repositories = config.ApplyUILanguageRootfsDefaults(repositories, uiLanguage)
	}
	if err := s.persistWebConfig(func(data map[string]any) {
		data["uiLanguage"] = uiLanguage
		data["rootfsRepositories"] = repositories
	}); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	s.uiLanguage = uiLanguage
	s.uiLanguageConfigured = true
	s.rootfsCacheMu.Lock()
	s.rootfsRepos = repositories
	s.rootfsReposConfigured = true
	s.rootfsCacheMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                   true,
		"saved":                true,
		"configWritten":        true,
		"uiLanguage":           s.uiLanguage,
		"uiLanguageConfigured": s.uiLanguageConfigured,
		"rootfsRepositories":   repositories,
	})
}

func (s *Server) applySystemSettings(settings normalizedSystemSettings, deferListenSettings bool) {
	previousNestedAndroidNATCompat, previousScope := s.nestedAndroidNATCompatState()
	nextWorkspace := strings.TrimSpace(settings.Workspace)
	workspaceChanged := s.workspace != nextWorkspace
	compatChanged := previousNestedAndroidNATCompat != settings.NestedAndroidNATCompat || previousScope.workspace != nextWorkspace
	previousBatteryMonitoring := s.batteryMonitoringEnabledSetting()
	batterySamplerRestart := previousBatteryMonitoring != settings.BatteryMonitoringEnabled || s.workspace != nextWorkspace
	retentionChanged := s.batteryStatsRetentionDaysSetting() != settings.BatteryStatsRetentionDays
	// Publish the new state before cancelling. A reconcile that was
	// queued before this update re-reads state after taking its execution lock,
	// so it cannot recreate rules for the old configuration afterwards. Policy
	// rules are shared by the network namespace and intentionally remain while
	// another WebUI workspace might use the same Droidspaces NAT CIDR.
	if compatChanged {
		s.setNestedAndroidNATCompatState(settings.NestedAndroidNATCompat, settings.Workspace)
		s.stopNestedAndroidNATCompatMonitor()
	}
	if !settings.BatteryMonitoringEnabled && previousBatteryMonitoring {
		// Publish disabled before cancellation so a sampler already between its
		// timer and collection checks cannot record another sample.
		s.setBatteryFeatureSettings(settings.OverviewPowerEnabled, false)
		s.stopBatteryStatsSampler()
	} else if batterySamplerRestart && previousBatteryMonitoring {
		// A workspace change needs a clean stop before the storage path changes.
		s.stopBatteryStatsSampler()
	}
	s.rootfsCacheMu.Lock()
	defer s.rootfsCacheMu.Unlock()

	if !deferListenSettings {
		s.mode = settings.Mode
		s.host = settings.Host
		s.port = settings.Port
	}
	s.authToken = settings.AuthToken
	s.uiLanguage = settings.UILanguage
	s.uiLanguageConfigured = true
	s.droidspacesPath = settings.DroidspacesPath
	s.corePath = settings.CorePath
	s.imageRoot = settings.ImageRoot
	s.templateImageRoot = settings.TemplateImageRoot
	s.workspace = nextWorkspace
	s.socketdEnabled = settings.SocketdEnabled
	s.rootfsSkipTLSVerify = settings.RootfsSkipTLSVerify
	s.rootfsRepos = settings.RootfsRepositories
	s.rootfsReposConfigured = true
	s.rootfsClient = rootfs.NewClient(settings.RootfsSkipTLSVerify)
	s.defaultNATCIDR = settings.DefaultNATCIDR
	s.defaultNATThirdOctet = settings.DefaultNATThirdOctet
	s.batteryDirectPower = settings.BatteryDirectPower
	s.batterySeriesCells = settings.BatterySeriesCells
	s.batteryDetailEnabled = settings.BatteryDetailEnabled
	s.setBatteryFeatureSettings(settings.OverviewPowerEnabled, settings.BatteryMonitoringEnabled)
	atomic.StoreInt64(&s.batteryStatsSampleSecs, int64(settings.BatteryStatsSampleSecs))
	atomic.StoreInt64(&s.batteryStatsWriteMins, int64(settings.BatteryStatsWriteMins))
	atomic.StoreInt64(&s.batteryStatsRetentionDays, int64(settings.BatteryStatsRetentionDays))
	s.overviewRefreshSecs = settings.OverviewRefreshSecs
	configuredBatterySeriesCells = settings.BatterySeriesCells
	if workspaceChanged {
		s.batteryStatsMu.Lock()
		s.batteryStats = batteryStatsState{}
		s.batteryStatsMu.Unlock()
	} else if retentionChanged {
		s.batteryStatsMu.Lock()
		storageRoot := s.batteryStatsStorageRoot()
		s.flushBatteryStatsLocked(storageRoot, time.Now())
		s.loadBatteryStatsLocked(storageRoot, time.Now())
		s.batteryStatsMu.Unlock()
	}
	if settings.BatteryMonitoringEnabled && batterySamplerRestart {
		s.startBatteryStatsSampler()
	}
	if settings.NestedAndroidNATCompat && compatChanged {
		s.startNestedAndroidNATCompatMonitor()
		s.reconcileNestedAndroidNATCompatAsync()
	}
}

func (s *Server) normalizeSystemSettings(req systemSettingsRequest) (normalizedSystemSettings, error) {
	mode := strings.TrimSpace(req.Mode)
	if mode == "" {
		mode = config.ModeLocal
	}
	if mode != config.ModeLocal && mode != config.ModePublic {
		return normalizedSystemSettings{}, fmt.Errorf("mode must be %q or %q", config.ModeLocal, config.ModePublic)
	}
	host := strings.TrimSpace(req.Host)
	if host == "" {
		if mode == config.ModePublic {
			host = "0.0.0.0"
		} else {
			host = "127.0.0.1"
		}
	}
	if mode == config.ModePublic && host == "127.0.0.1" {
		host = "0.0.0.0"
	}
	if mode == config.ModeLocal {
		host = "127.0.0.1"
	}
	if hasConfigUnsafeChars(host) {
		return normalizedSystemSettings{}, fmt.Errorf("host contains invalid characters")
	}
	if req.Port <= 0 || req.Port > 65535 {
		return normalizedSystemSettings{}, fmt.Errorf("port must be between 1 and 65535")
	}
	authToken := strings.TrimSpace(req.AuthToken)
	if mode == config.ModePublic && authToken == "" {
		token, err := config.GenerateAuthToken(8)
		if err != nil {
			return normalizedSystemSettings{}, fmt.Errorf("generate authToken: %w", err)
		}
		authToken = token
	}
	uiLanguage := strings.TrimSpace(req.UILanguage)
	if uiLanguage == "" {
		uiLanguage = s.uiLanguage
	}
	uiLanguage, err := config.NormalizeUILanguage(uiLanguage)
	if err != nil {
		return normalizedSystemSettings{}, err
	}
	paths := map[string]string{
		"droidspacesPath":   strings.TrimSpace(req.DroidspacesPath),
		"corePath":          strings.TrimSpace(req.CorePath),
		"imageRoot":         strings.TrimSpace(req.ImageRoot),
		"templateImageRoot": strings.TrimSpace(req.TemplateImageRoot),
		"workspace":         strings.TrimSpace(req.Workspace),
	}
	if paths["workspace"] == "" {
		paths["workspace"] = strings.TrimSpace(s.workspace)
		if paths["workspace"] == "" {
			paths["workspace"] = config.Default().Workspace
		}
	}
	if paths["droidspacesPath"] == "" {
		paths["droidspacesPath"] = filepath.Join(paths["workspace"], "bin", "droidspaces")
	}
	if paths["corePath"] == "" {
		paths["corePath"] = filepath.Dir(paths["droidspacesPath"])
	}
	if paths["templateImageRoot"] == "" {
		paths["templateImageRoot"] = filepath.Join(paths["workspace"], "rootfs")
	}
	if paths["imageRoot"] == "" {
		paths["imageRoot"] = paths["templateImageRoot"]
	}
	for key, value := range paths {
		if hasConfigUnsafeChars(value) {
			return normalizedSystemSettings{}, fmt.Errorf("%s contains invalid characters", key)
		}
	}
	socketdEnabled := s.socketdEnabled
	if req.SocketdEnabled != nil {
		socketdEnabled = *req.SocketdEnabled
	}
	rootfsSkipTLSVerify := s.rootfsSkipTLSVerify
	if req.RootfsSkipTLSVerify != nil {
		rootfsSkipTLSVerify = *req.RootfsSkipTLSVerify
	}
	batteryDirectPower := s.batteryDirectPower
	if req.BatteryDirectPower != nil {
		batteryDirectPower = *req.BatteryDirectPower
	}
	batterySeriesCells := s.batterySeriesCells
	if req.BatterySeriesCells != nil {
		batterySeriesCells = *req.BatterySeriesCells
	}
	if batterySeriesCells < 0 || batterySeriesCells > 6 {
		return normalizedSystemSettings{}, fmt.Errorf("batterySeriesCells must be 0(auto) or between 1 and 6")
	}
	batteryDetailEnabled := s.batteryDetailEnabled
	if req.BatteryDetailEnabled != nil {
		batteryDetailEnabled = *req.BatteryDetailEnabled
	}
	overviewPowerEnabled := s.overviewPowerEnabledSetting()
	if req.OverviewPowerEnabled != nil {
		overviewPowerEnabled = *req.OverviewPowerEnabled
	}
	batteryMonitoringEnabled := s.batteryMonitoringEnabledSetting()
	if req.BatteryMonitoringEnabled != nil {
		batteryMonitoringEnabled = *req.BatteryMonitoringEnabled
	}
	batteryStatsSampleSecs := req.BatteryStatsSampleSecs
	if batteryStatsSampleSecs <= 0 {
		batteryStatsSampleSecs = s.batteryStatsSampleSeconds()
	}
	if batteryStatsSampleSecs < batteryStatsMinSampleSeconds || batteryStatsSampleSecs > batteryStatsMaxSampleSeconds {
		return normalizedSystemSettings{}, fmt.Errorf("batteryStatsSampleSeconds must be between %d and %d", batteryStatsMinSampleSeconds, batteryStatsMaxSampleSeconds)
	}
	batteryStatsWriteMins := req.BatteryStatsWriteMins
	if batteryStatsWriteMins <= 0 {
		batteryStatsWriteMins = s.batteryStatsWriteMinutes()
	}
	if batteryStatsWriteMins < batteryStatsMinWriteMinutes || batteryStatsWriteMins > batteryStatsMaxWriteMinutes {
		return normalizedSystemSettings{}, fmt.Errorf("batteryStatsWriteMinutes must be between %d and %d", batteryStatsMinWriteMinutes, batteryStatsMaxWriteMinutes)
	}
	batteryStatsRetentionDays := req.BatteryStatsRetentionDays
	if batteryStatsRetentionDays <= 0 {
		batteryStatsRetentionDays = s.batteryStatsRetentionDaysSetting()
	}
	if batteryStatsRetentionDays < batteryStatsMinRetentionDays || batteryStatsRetentionDays > batteryStatsMaxRetentionDays {
		return normalizedSystemSettings{}, fmt.Errorf("batteryStatsRetentionDays must be between %d and %d", batteryStatsMinRetentionDays, batteryStatsMaxRetentionDays)
	}
	overviewRefreshSecs := req.OverviewRefreshSecs
	if overviewRefreshSecs <= 0 {
		overviewRefreshSecs = s.overviewRefreshSecs
	}
	if overviewRefreshSecs <= 0 {
		overviewRefreshSecs = 3
	}
	if overviewRefreshSecs < 1 || overviewRefreshSecs > 60 {
		return normalizedSystemSettings{}, fmt.Errorf("overviewRefreshSeconds must be between 1 and 60")
	}
	cidr := strings.TrimSpace(req.DefaultNATCIDR)
	if cidr == "" {
		cidr = config.DefaultNATCIDR
	}
	if cidr != config.DefaultNATCIDR {
		return normalizedSystemSettings{}, fmt.Errorf("defaultNatCIDR currently must be %q", config.DefaultNATCIDR)
	}
	natThirdOctet := req.DefaultNATThirdOctet
	if natThirdOctet <= 0 {
		natThirdOctet = s.defaultNATThirdOctet
	}
	if natThirdOctet <= 0 {
		natThirdOctet = config.DefaultNATThirdOctet
	}
	if natThirdOctet < 1 || natThirdOctet > 254 {
		return normalizedSystemSettings{}, fmt.Errorf("defaultNatThirdOctet must be between 1 and 254")
	}
	nestedAndroidNATCompat := s.nestedAndroidNATCompatEnabled()
	if req.NestedAndroidNATCompat != nil {
		nestedAndroidNATCompat = *req.NestedAndroidNATCompat
	}
	repos, err := normalizeRootfsRepositories(req.RootfsRepositories)
	if err != nil {
		return normalizedSystemSettings{}, err
	}
	return normalizedSystemSettings{
		Mode:                      mode,
		Host:                      host,
		Port:                      req.Port,
		AuthToken:                 authToken,
		UILanguage:                uiLanguage,
		DroidspacesPath:           paths["droidspacesPath"],
		CorePath:                  paths["corePath"],
		ImageRoot:                 paths["imageRoot"],
		TemplateImageRoot:         paths["templateImageRoot"],
		Workspace:                 paths["workspace"],
		SocketdEnabled:            socketdEnabled,
		RootfsSkipTLSVerify:       rootfsSkipTLSVerify,
		DefaultNATCIDR:            cidr,
		DefaultNATThirdOctet:      natThirdOctet,
		NestedAndroidNATCompat:    nestedAndroidNATCompat,
		BatteryDirectPower:        batteryDirectPower,
		BatterySeriesCells:        batterySeriesCells,
		OverviewPowerEnabled:      overviewPowerEnabled,
		BatteryMonitoringEnabled:  batteryMonitoringEnabled,
		BatteryDetailEnabled:      batteryDetailEnabled,
		BatteryStatsSampleSecs:    batteryStatsSampleSecs,
		BatteryStatsWriteMins:     batteryStatsWriteMins,
		BatteryStatsRetentionDays: batteryStatsRetentionDays,
		OverviewRefreshSecs:       overviewRefreshSecs,
		RootfsRepositories:        repos,
	}, nil
}

func (s *Server) systemSettingsResponse(saved bool, restartRequired bool) map[string]any {
	return map[string]any{
		"ok":                          true,
		"saved":                       saved,
		"configWritten":               saved && s.configPath != "",
		"restartRequired":             restartRequired,
		"mode":                        s.mode,
		"host":                        s.host,
		"port":                        s.port,
		"authToken":                   s.authToken,
		"uiLanguage":                  s.uiLanguage,
		"uiLanguageConfigured":        s.uiLanguageConfigured,
		"authEnabled":                 s.authToken != "",
		"droidspacesPath":             s.droidspacesPath,
		"corePath":                    s.corePath,
		"imageRoot":                   s.imageRoot,
		"templateImageRoot":           s.templateImageRoot,
		"workspace":                   s.workspace,
		"configPath":                  s.configPath,
		"socketdEnabled":              s.socketdEnabled,
		"rootfsSkipTLSVerify":         s.rootfsSkipTLSVerify,
		"defaultNatCIDR":              s.defaultNATCIDR,
		"defaultNatThirdOctet":        s.defaultNATThirdOctet,
		"nestedAndroidNatCompat":      s.nestedAndroidNATCompatEnabled(),
		"batteryDirectPowerSupported": s.batteryDirectPower,
		"batterySeriesCells":          s.batterySeriesCells,
		"overviewPowerEnabled":        s.overviewPowerEnabledSetting(),
		"batteryMonitoringEnabled":    s.batteryMonitoringEnabledSetting(),
		"batteryDetailEnabled":        s.batteryDetailEnabled,
		"batteryStatsSampleSeconds":   s.batteryStatsSampleSeconds(),
		"batteryStatsWriteMinutes":    s.batteryStatsWriteMinutes(),
		"batteryStatsRetentionDays":   s.batteryStatsRetentionDaysSetting(),
		"overviewRefreshSeconds":      s.overviewRefreshSecs,
		"natGatewayIP":                "172.28.0.1",
		// Droidspaces v6.5 detects the uplink itself. Retain these fields for
		// clients from earlier WebUI versions, but no longer expose a preset.
		"upstreamMode":              "core-auto-detect",
		"androidNATUpstreamPresets": false,
		"rootfsRepositories":        s.rootfsRepos,
		"integration":               s.diagnosticsSettings(),
	}
}

func (s *Server) handleRootfsRepositories(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.rootfsCacheMu.Lock()
		repositories := append([]config.RootfsRepository(nil), s.rootfsRepos...)
		s.rootfsCacheMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"repositories": repositories})
	case http.MethodPut, http.MethodPost:
		var req rootfsRepositoriesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		repos, err := normalizeRootfsRepositories(req.Repositories)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
			return
		}
		if err := s.persistWebConfig(func(data map[string]any) { data["rootfsRepositories"] = repos }); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		s.rootfsCacheMu.Lock()
		s.rootfsRepos = repos
		s.rootfsReposConfigured = true
		s.rootfsCacheMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "repositories": repos})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
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

func (s *Server) handleLocalRootfsUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRootfsUploadBytes+1)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid multipart upload"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "missing file field"})
		return
	}
	defer file.Close()
	name := sanitizeRootfsUploadName(header.Filename)
	if name == "" || (!isRootfsArchive(name) && !strings.HasSuffix(strings.ToLower(name), ".img")) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "upload must be .img, .tar.gz, .tgz, or .tar.xz"})
		return
	}
	uploadRoot := filepath.Join(s.templateImageRoot, rootfsUploadsDirectory)
	if err := os.MkdirAll(uploadRoot, 0755); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	dest := filepath.Join(uploadRoot, name)
	if _, err := os.Stat(dest); err == nil {
		base := strings.TrimSuffix(name, filepath.Ext(name))
		ext := filepath.Ext(name)
		if strings.HasSuffix(strings.ToLower(name), ".tar.gz") {
			base = strings.TrimSuffix(name, ".tar.gz")
			ext = ".tar.gz"
		} else if strings.HasSuffix(strings.ToLower(name), ".tar.xz") {
			base = strings.TrimSuffix(name, ".tar.xz")
			ext = ".tar.xz"
		}
		dest = filepath.Join(uploadRoot, fmt.Sprintf("%s-%d%s", base, time.Now().Unix(), ext))
	}
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	defer out.Close()
	written, err := io.Copy(out, io.LimitReader(file, maxRootfsUploadBytes+1))
	if err != nil {
		_ = os.Remove(dest)
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	if written > maxRootfsUploadBytes {
		_ = os.Remove(dest)
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{Error: "upload exceeds 16GB limit"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "path": dest, "name": filepath.Base(dest)})
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
	if !s.localRootfsFileAllowed(path) {
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

func (s *Server) handleLocalRootfsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	path := strings.TrimSpace(r.URL.Query().Get("path"))
	if err := s.validateLocalRootfsFileForDelete(path); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, os.ErrPermission) {
			status = http.StatusForbidden
		} else if errors.Is(err, os.ErrNotExist) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, apiError{Error: err.Error()})
		return
	}
	if err := os.Remove(path); err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": path})
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

// configuredRootfsAsset resolves a client-selected URL against the currently
// configured repositories. The server always downloads the verified asset
// metadata rather than trusting client-supplied filenames, sizes, or URLs.
func (s *Server) configuredRootfsAsset(ctx context.Context, downloadURL string, architecture string) (rootfs.Asset, error) {
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return rootfs.Asset{}, fmt.Errorf("cloudRootfsUrl is required for cloud source")
	}
	architecture = strings.TrimSpace(architecture)
	if architecture == "" {
		architecture = rootfs.DeviceArch()
	}

	rootfsClient, repositories, _ := s.rootfsConfigurationSnapshot()
	allowedAssets, fetchErrors := rootfsClient.FetchAll(ctx, repositories, architecture)
	for _, candidate := range allowedAssets {
		if strings.TrimSpace(candidate.DownloadURL) != downloadURL {
			continue
		}
		if candidate.UniqueFilename == "" {
			candidate.UniqueFilename = rootfs.UniqueFilename(candidate)
		}
		return candidate, nil
	}

	message := "download URL is not present in configured rootfs repositories"
	if len(fetchErrors) > 0 {
		message += ": " + strings.Join(fetchErrors, "; ")
	}
	return rootfs.Asset{}, errors.New(message)
}

func (s *Server) rootfsConfigurationSnapshot() (*rootfs.Client, []config.RootfsRepository, string) {
	s.rootfsCacheMu.RLock()
	client := s.rootfsClient
	repositories := append([]config.RootfsRepository(nil), s.rootfsRepos...)
	templateImageRoot := s.templateImageRoot
	s.rootfsCacheMu.RUnlock()
	if len(repositories) == 0 {
		repositories = config.DefaultRootfsRepositories()
	}
	return client, repositories, templateImageRoot
}

func rootfsDownloadFilename(asset rootfs.Asset) string {
	filename := strings.TrimSpace(asset.UniqueFilename)
	if filename == "" {
		filename = rootfs.UniqueFilename(asset)
	}
	return filename
}

func rootfsDownloadRequestKey(downloadURL string, architecture string) string {
	downloadURL = strings.TrimSpace(downloadURL)
	architecture = strings.ToLower(strings.TrimSpace(architecture))
	if architecture == "" {
		architecture = rootfs.DeviceArch()
	}
	return architecture + "\x00" + downloadURL
}

func rootfsDownloadRequestName(downloadURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(downloadURL))
	if err == nil {
		if name := pathpkg.Base(strings.TrimSpace(parsed.Path)); name != "" && name != "." && name != "/" {
			return name
		}
	}
	return "Cloud rootfs"
}

// beginRootfsDownloadRequest creates a visible task before any repository
// metadata is fetched. Requests for the same URL and architecture share that
// task while verification and the eventual archive download are in progress.
func (s *Server) beginRootfsDownloadRequest(downloadURL string, architecture string) (*taskState, bool, error) {
	downloadURL = strings.TrimSpace(downloadURL)
	if downloadURL == "" {
		return nil, false, fmt.Errorf("downloadUrl is required")
	}
	architecture = strings.TrimSpace(architecture)
	if architecture == "" {
		architecture = rootfs.DeviceArch()
	}
	key := rootfsDownloadRequestKey(downloadURL, architecture)

	s.rootfsDownloadMu.Lock()
	if existing := s.rootfsDownloadRequests[key]; existing != nil {
		if task, ok := s.getTask(existing.taskID); ok && strings.ToLower(strings.TrimSpace(task.Status)) != "error" {
			s.rootfsDownloadMu.Unlock()
			return task, false, nil
		}
		delete(s.rootfsDownloadRequests, key)
	}
	task := s.newTask("rootfs-download", rootfsDownloadRequestName(downloadURL))
	flight := &rootfsDownloadRequestFlight{taskID: task.ID}
	s.rootfsDownloadRequests[key] = flight
	s.rootfsDownloadMu.Unlock()

	s.appendTaskLog(task.ID, "Download request accepted.")
	s.appendTaskLog(task.ID, "Verifying selected cloud rootfs against configured repositories...")
	go s.runRootfsDownloadRequest(key, flight, downloadURL, architecture)
	return task, true, nil
}

func (s *Server) runRootfsDownloadRequest(requestKey string, flight *rootfsDownloadRequestFlight, downloadURL string, architecture string) {
	taskID := flight.taskID
	defer func() {
		s.rootfsDownloadMu.Lock()
		if s.rootfsDownloadRequests[requestKey] == flight {
			delete(s.rootfsDownloadRequests, requestKey)
		}
		s.rootfsDownloadMu.Unlock()
	}()

	s.updateTask(taskID, func(t *taskState) {
		t.Status = "running"
		t.Percent = 1
	})
	verifyCtx, cancel := context.WithTimeout(context.Background(), rootfsMetadataTaskTimeout)
	asset, err := s.configuredRootfsAsset(verifyCtx, downloadURL, architecture)
	cancel()
	if err != nil {
		s.appendTaskLog(taskID, "Cloud rootfs verification failed: "+err.Error())
		s.failTask(taskID, err)
		return
	}

	job, started, err := s.beginSharedRootfsDownloadWithTask(asset, taskID)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	if job.taskID == taskID {
		if !started {
			s.appendTaskLog(taskID, "Reusing the completed cloud rootfs already in template storage.")
		}
		<-job.done
		return
	}

	if started {
		s.appendTaskLog(taskID, "Waiting for a shared cloud rootfs download task: "+job.taskID)
	} else {
		s.appendTaskLog(taskID, "Joined an in-progress shared cloud rootfs download task: "+job.taskID)
	}
	downloadCtx, downloadCancel := context.WithTimeout(context.Background(), rootfsDownloadTaskTimeout)
	defer downloadCancel()
	downloadedPath, err := s.waitForSharedRootfsDownload(downloadCtx, job, func(sharedTask *taskState) {
		s.updateTask(taskID, func(t *taskState) {
			t.Status = "running"
			t.Name = sharedTask.Name
			t.Downloaded = sharedTask.Downloaded
			t.Total = sharedTask.Total
			t.Percent = sharedTask.Percent
		})
	})
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	s.completeTask(taskID, downloadedPath, "/api/rootfs/local/download?path="+url.QueryEscape(downloadedPath))
}

func (s *Server) beginSharedRootfsDownload(asset rootfs.Asset) (*sharedRootfsDownload, bool, error) {
	return s.beginSharedRootfsDownloadWithTask(asset, "")
}

// beginSharedRootfsDownloadWithTask promotes an already-visible request task
// into the archive download owner when no matching archive task exists.
func (s *Server) beginSharedRootfsDownloadWithTask(asset rootfs.Asset, requestedTaskID string) (*sharedRootfsDownload, bool, error) {
	asset.UniqueFilename = rootfsDownloadFilename(asset)
	if asset.UniqueFilename == "" || asset.UniqueFilename == "." || asset.UniqueFilename == ".." || filepath.Base(asset.UniqueFilename) != asset.UniqueFilename || strings.ContainsAny(asset.UniqueFilename, `/\\`) {
		return nil, false, fmt.Errorf("invalid rootfs filename")
	}
	if requestedTaskID != "" {
		if _, ok := s.getTask(requestedTaskID); !ok {
			return nil, false, fmt.Errorf("rootfs request task was not found")
		}
	}
	rootfsClient, repositories, templateImageRoot := s.rootfsConfigurationSnapshot()
	storage := rootfsTemplateStorageSourceForAssetFromRepositories(asset, repositories)
	storageDirectory := rootfsTemplateStorageDirectoryForAssetWithSource(asset, storage)
	downloadRoot := filepath.Join(templateImageRoot, storageDirectory)
	key := filepath.Clean(filepath.Join(downloadRoot, asset.UniqueFilename))

	s.rootfsDownloadMu.Lock()
	if existing := s.rootfsDownloads[key]; existing != nil {
		select {
		case <-existing.done:
			if !rootfsDownloadedArchiveIsReusable(existing) {
				delete(s.rootfsDownloads, key)
			} else {
				s.rootfsDownloadMu.Unlock()
				return existing, false, nil
			}
		default:
			s.rootfsDownloadMu.Unlock()
			return existing, false, nil
		}
	}
	completedPath := filepath.Join(downloadRoot, asset.UniqueFilename)
	migratedLegacyArchive, err := migrateLegacyLinuxContainersArchive(templateImageRoot, storage, asset, completedPath)
	if err != nil {
		s.rootfsDownloadMu.Unlock()
		return nil, false, err
	}
	if rootfsArchiveMatchesAsset(completedPath, asset) {
		taskID := requestedTaskID
		if taskID == "" {
			taskID = s.newTask("rootfs-download", asset.Name).ID
		}
		job := &sharedRootfsDownload{
			taskID:       taskID,
			done:         make(chan struct{}),
			asset:        asset,
			downloadRoot: downloadRoot,
			storage:      storage,
			path:         completedPath,
		}
		s.updateTask(taskID, func(t *taskState) {
			if strings.TrimSpace(asset.Name) != "" {
				t.Name = asset.Name
			}
			t.Status = "running"
			t.Total = asset.SizeBytes
		})
		if migratedLegacyArchive {
			s.appendTaskLog(taskID, "Moved a verified legacy rootfs into lxc-image template storage.")
		}
		s.appendTaskLog(taskID, "Reusing the completed cloud rootfs already in template storage.")
		s.completeTask(taskID, completedPath, "/api/rootfs/local/download?path="+url.QueryEscape(completedPath))
		close(job.done)
		s.rootfsDownloads[key] = job
		s.rootfsDownloadMu.Unlock()
		return job, false, nil
	}
	taskID := requestedTaskID
	if taskID == "" {
		taskID = s.newTask("rootfs-download", asset.Name).ID
	}
	job := &sharedRootfsDownload{
		taskID:       taskID,
		done:         make(chan struct{}),
		asset:        asset,
		downloadRoot: downloadRoot,
		storage:      storage,
	}
	s.rootfsDownloads[key] = job
	s.rootfsDownloadMu.Unlock()

	s.updateTask(taskID, func(t *taskState) {
		if strings.TrimSpace(asset.Name) != "" {
			t.Name = asset.Name
		}
		t.Status = "running"
		t.Percent = 1
		t.Total = asset.SizeBytes
	})
	s.appendTaskLog(taskID, "Queued shared cloud rootfs download.")
	go s.runSharedRootfsDownload(key, job, rootfsClient)
	return job, true, nil
}

func rootfsArchiveMatchesAsset(path string, asset rootfs.Asset) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return asset.SizeBytes <= 0 || info.Size() == asset.SizeBytes
}

// migrateLegacyLinuxContainersArchive preserves existing downloads while the
// Linux Containers namespace moves from its host-name directory to lxc-image.
// Only a complete archive that matches the selected asset is moved.
func migrateLegacyLinuxContainersArchive(templateImageRoot string, storage localRootfsStorageSource, asset rootfs.Asset, destination string) (bool, error) {
	if storage.directory != rootfsLinuxContainersDirectory || rootfsArchiveMatchesAsset(destination, asset) {
		return false, nil
	}

	filename := rootfsDownloadFilename(asset)
	variant := rootfsAssetStorageVariant(asset)
	legacyDirectories := []string{
		filepath.Join(rootfsLinuxContainersPreviousDirectory, variant),
		rootfsLinuxContainersPreviousDirectory,
		filepath.Join(rootfsLinuxContainersLegacyDir, variant),
		rootfsLinuxContainersLegacyDir,
	}
	for _, directory := range legacyDirectories {
		legacyPath := filepath.Join(templateImageRoot, directory, filename)
		if !rootfsArchiveMatchesAsset(legacyPath, asset) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return false, err
		}
		if err := os.Rename(legacyPath, destination); err != nil {
			return false, fmt.Errorf("move legacy Linux Containers rootfs into lxc-image storage: %w", err)
		}
		return true, nil
	}
	return false, nil
}

func rootfsDownloadedArchiveIsReusable(job *sharedRootfsDownload) bool {
	if job == nil || job.err != nil || strings.TrimSpace(job.path) == "" {
		return false
	}
	return rootfsArchiveMatchesAsset(job.path, job.asset)
}

func (s *Server) runSharedRootfsDownload(key string, job *sharedRootfsDownload, rootfsClient *rootfs.Client) {
	downloadCtx, cancel := context.WithTimeout(context.Background(), rootfsDownloadTaskTimeout)
	defer cancel()

	var downloadedPath string
	var downloadErr error
	defer func() {
		job.path = downloadedPath
		job.err = downloadErr
		s.rootfsDownloadMu.Lock()
		if downloadErr != nil && s.rootfsDownloads[key] == job {
			delete(s.rootfsDownloads, key)
		}
		close(job.done)
		s.rootfsDownloadMu.Unlock()
	}()

	s.appendTaskLog(job.taskID, "Selected cloud rootfs: "+job.asset.Name)
	s.appendTaskLog(job.taskID, "Downloading into template storage: "+job.storage.label+" / "+rootfsAssetStorageVariant(job.asset))
	downloadedPath, downloadErr = rootfsClient.DownloadWithProgressAndLog(downloadCtx, job.asset, job.downloadRoot, func(downloaded int64, total int64) {
		s.updateTask(job.taskID, func(t *taskState) {
			t.Downloaded = downloaded
			t.Total = total
			if total > 0 {
				t.Percent = int(downloaded * 100 / total)
			}
		})
	}, func(message string) {
		s.appendTaskLog(job.taskID, "Rootfs download: "+message)
	})
	if downloadErr != nil {
		s.appendTaskLog(job.taskID, "Rootfs download failed: "+downloadErr.Error())
		s.failTask(job.taskID, downloadErr)
		return
	}
	s.appendTaskLog(job.taskID, "Cloud rootfs download completed: "+downloadedPath)
	s.completeTask(job.taskID, downloadedPath, "/api/rootfs/local/download?path="+url.QueryEscape(downloadedPath))
}

func (s *Server) waitForSharedRootfsDownload(ctx context.Context, job *sharedRootfsDownload, progress func(*taskState)) (string, error) {
	if job == nil {
		return "", fmt.Errorf("rootfs download is not configured")
	}
	ticker := time.NewTicker(600 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-job.done:
			if job.err != nil {
				return "", job.err
			}
			if strings.TrimSpace(job.path) == "" {
				return "", fmt.Errorf("shared rootfs download completed without a path")
			}
			return job.path, nil
		case <-ticker.C:
			if progress != nil {
				if task, ok := s.getTask(job.taskID); ok {
					progress(task)
				}
			}
		}
	}
}

func (s *Server) handleNetworkSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"defaultNatCIDR": s.defaultNATCIDR, "defaultNatThirdOctet": s.defaultNATThirdOctet, "natGatewayIP": "172.28.0.1", "upstreamMode": "core-auto-detect", "androidNATUpstreamPresets": false})
	case http.MethodPut, http.MethodPost:
		var req networkSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		cidr := strings.TrimSpace(req.DefaultNATCIDR)
		if cidr == "" {
			cidr = config.DefaultNATCIDR
		}
		if cidr != config.DefaultNATCIDR {
			writeJSON(w, http.StatusBadRequest, apiError{Error: fmt.Sprintf("当前 core 仅支持 NAT 网段 %s", config.DefaultNATCIDR)})
			return
		}
		natThirdOctet := req.DefaultNATThirdOctet
		if natThirdOctet <= 0 {
			natThirdOctet = s.defaultNATThirdOctet
		}
		if natThirdOctet <= 0 {
			natThirdOctet = config.DefaultNATThirdOctet
		}
		if natThirdOctet < 1 || natThirdOctet > 254 {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "defaultNatThirdOctet must be between 1 and 254"})
			return
		}
		if err := s.persistWebConfig(func(data map[string]any) {
			data["defaultNatCIDR"] = cidr
			data["defaultNatThirdOctet"] = natThirdOctet
			delete(data, "defaultNatUpstreamIfname")
			delete(data, "defaultNatUpstreamIfnames")
			delete(data, "natUpstreamIfname")
			delete(data, "natUpstreamIfnames")
		}); err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
			return
		}
		s.defaultNATCIDR = cidr
		s.defaultNATThirdOctet = natThirdOctet
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "defaultNatCIDR": cidr, "defaultNatThirdOctet": natThirdOctet, "natGatewayIP": "172.28.0.1", "upstreamMode": "core-auto-detect", "androidNATUpstreamPresets": false})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
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
	if strings.TrimSpace(req.DownloadURL) == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "downloadUrl is required"})
		return
	}

	task, started, err := s.beginRootfsDownloadRequest(req.DownloadURL, req.Architecture)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if !started {
		s.appendTaskLog(task.ID, "A duplicate request joined this in-progress rootfs download.")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "shared": !started, "taskId": task.ID, "task": task})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": s.listTasks(), "summary": s.taskSummary()})
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	willStop, _ := s.containerRunning(ctx, target)
	cancel()

	task := s.newTask(kind, target)
	s.updateTask(task.ID, func(t *taskState) {
		t.Status = "running"
		t.WillStopContainer = willStop
		t.RestoreAfterBackup = willStop
	})
	task, _ = s.getTask(task.ID)
	go s.runExportTask(task.ID, target, asTemplate)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":                 true,
		"taskId":             task.ID,
		"task":               task,
		"willStopContainer":  willStop,
		"restoreAfterBackup": willStop,
	})
}

func (s *Server) runExportTask(taskID string, target string, asTemplate bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	restoreAfterBackup := false
	if running, _ := s.containerRunning(ctx, target); running {
		restoreAfterBackup = true
		s.updateTask(taskID, func(t *taskState) {
			t.WillStopContainer = true
			t.RestoreAfterBackup = true
		})
		result, err := s.lifecycleViaCLI(ctx, target, "stop")
		if err != nil {
			s.failTask(taskID, fmt.Errorf("failed to stop container before export: %v\n%s", err, result.Output))
			return
		}
		s.updateTask(taskID, func(t *taskState) {
			t.StoppedContainer = true
		})
	}
	restoreContainer := func() {
		if !restoreAfterBackup {
			return
		}
		startCtx, startCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer startCancel()
		result, err := s.lifecycleViaCLI(startCtx, target, "start")
		if err != nil {
			s.updateTask(taskID, func(t *taskState) {
				message := fmt.Sprintf("failed to restart container after export: %v\n%s", err, result.Output)
				t.RestoreError = message
				if t.Status == "error" && t.Error != "" {
					t.Error += "\n" + message
				}
			})
			return
		}
		s.updateTask(taskID, func(t *taskState) {
			t.RestoredContainer = true
		})
		restoreAfterBackup = false
	}
	defer restoreContainer()
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
	restoreContainer()
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
	return item.toSocketdInspect(), err
}

func (s *Server) handleContainerUsers(w http.ResponseWriter, r *http.Request, target string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	users, err := s.containerUsers(ctx, target)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) containerUsers(ctx context.Context, target string) ([]containerUser, error) {
	cmd := `awk -F: '$1 == "root" || ($3 >= 1000 && $3 < 65534 && $1 !~ /^(nixbld)/) {print $1"|"$3"|"$4"|"$6"|"$7}' /etc/passwd 2>/dev/null`
	result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
	if err != nil {
		return nil, err
	}
	root := containerUser{Name: "root", UID: "0", GID: "0"}
	regular := make([]containerUser, 0)
	seen := map[string]bool{}
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) < 5 || parts[0] == "" || seen[parts[0]] {
			continue
		}
		user := containerUser{Name: parts[0], UID: parts[1], GID: parts[2], Home: parts[3], Shell: parts[4]}
		if !isAndroidAppTerminalUser(user) {
			continue
		}
		seen[user.Name] = true
		if user.Name == "root" {
			root = user
			continue
		}
		regular = append(regular, user)
	}
	users := make([]containerUser, 0, 1+len(regular))
	users = append(users, root)
	users = append(users, regular...)
	return users, nil
}

func isAndroidAppTerminalUser(user containerUser) bool {
	name := strings.TrimSpace(user.Name)
	if name == "" || strings.HasPrefix(name, "nixbld") {
		return false
	}
	if name == "root" {
		return true
	}
	uid, err := strconv.Atoi(strings.TrimSpace(user.UID))
	if err != nil {
		return false
	}
	return uid >= 1000 && uid < 65534
}

func (s *Server) handleContainerServices(w http.ResponseWriter, r *http.Request, target string, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		resp := map[string]any{"managers": []string{}, "services": []serviceInfo{}, "servicesByManager": map[string][]serviceInfo{}, "errors": map[string]string{}}
		servicesByManager := map[string][]serviceInfo{}
		services := []serviceInfo{}
		errorsOut := map[string]string{}
		managers := []string{}
		for _, manager := range []string{"systemd", "openrc", "procd"} {
			items, err := s.listServices(ctx, target, manager)
			if err != nil {
				errorsOut[manager] = err.Error()
				continue
			}
			if len(items) == 0 {
				continue
			}
			for i := range items {
				items[i].Manager = manager
			}
			servicesByManager[manager] = items
			services = append(services, items...)
			managers = append(managers, manager)
		}
		resp["managers"] = managers
		resp["services"] = services
		resp["servicesByManager"] = servicesByManager
		resp["errors"] = errorsOut
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if len(parts) < 2 {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	manager, err := cleanServiceSegment(parts[0])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	service, err := url.PathUnescape(parts[1])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid service"})
		return
	}
	service, err = cleanServiceName(service)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if manager == "systemd" && len(parts) == 3 {
		resource, resourceErr := cleanServiceSegment(parts[2])
		if resourceErr != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: resourceErr.Error()})
			return
		}
		switch resource {
		case "inspect":
			s.handleSystemdUnitInspect(w, r, target, service)
			return
		case "override":
			s.handleSystemdUnitOverride(w, r, target, service)
			return
		}
	}
	if r.Method != http.MethodPost || len(parts) != 3 {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	action, err := cleanServiceSegment(parts[2])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	result, err := s.serviceAction(ctx, target, manager, service, action)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("%v\n%s", err, result.Output)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "manager": manager, "service": service, "action": action, "exitCode": result.ExitCode, "output": result.Output})
}

func cleanServiceSegment(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || strings.ContainsAny(value, "/\\\x00") {
		return "", fmt.Errorf("invalid service path")
	}
	return value, nil
}

var serviceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@:+-]{1,255}$`)

func cleanServiceName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !serviceNamePattern.MatchString(value) {
		return "", fmt.Errorf("invalid service name")
	}
	return value, nil
}

func (s *Server) handleSystemdUnitInspect(w http.ResponseWriter, r *http.Request, target string, unit string) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	inspection, result, err := s.inspectSystemdUnit(ctx, target, unit)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("%v\n%s", err, result.Output)})
		return
	}
	writeJSON(w, http.StatusOK, inspection)
}

func (s *Server) handleSystemdUnitOverride(w http.ResponseWriter, r *http.Request, target string, unit string) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	dir, path := systemdOverridePaths(unit)
	switch r.Method {
	case http.MethodGet:
		cmd := "command -v systemctl >/dev/null 2>&1 || exit 42; if [ -f " + shellQuote(path) + " ]; then cat " + shellQuote(path) + "; else exit 44; fi"
		result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
		if err != nil && result.ExitCode != 44 {
			writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("%v\n%s", err, result.Output)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"exists": result.ExitCode != 44, "content": result.Output})
	case http.MethodPut:
		var req systemdOverrideRequest
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		if strings.ContainsRune(req.Content, '\x00') || len(req.Content) > 64<<10 {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "override content must be at most 64 KiB and contain no NUL bytes"})
			return
		}
		encoded := base64.StdEncoding.EncodeToString([]byte(req.Content))
		cmd := "command -v systemctl >/dev/null 2>&1 || exit 42; umask 022; mkdir -p " + shellQuote(dir) + " && { command -v base64 >/dev/null 2>&1 && base64 -d || busybox base64 -d; } <<'DS_WEBUI_OVERRIDE' > " + shellQuote(path) + "\n" + encoded + "\nDS_WEBUI_OVERRIDE\nsystemctl daemon-reload"
		result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("%v\n%s", err, result.Output)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": result.Output})
	case http.MethodDelete:
		cmd := "command -v systemctl >/dev/null 2>&1 || exit 42; rm -f " + shellQuote(path) + "; rmdir " + shellQuote(dir) + " 2>/dev/null || true; systemctl daemon-reload"
		result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("%v\n%s", err, result.Output)})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": result.Output})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func systemdOverridePaths(unit string) (string, string) {
	dir := "/etc/systemd/system/" + unit + ".d"
	return dir, dir + "/override.conf"
}

func (s *Server) inspectSystemdUnit(ctx context.Context, target string, unit string) (systemdUnitInspection, cliCommandResult, error) {
	const markerStatus = "__DS_WEBUI_STATUS__"
	const markerDeps = "__DS_WEBUI_DEPS__"
	properties := []string{"Description", "LoadState", "ActiveState", "SubState", "UnitFileState", "FragmentPath", "DropInPaths", "MainPID", "ExecMainStartTimestamp", "Restart", "MemoryCurrent", "CPUUsageNSec"}
	quotedUnit := shellQuote(unit)
	cmd := "command -v systemctl >/dev/null 2>&1 || exit 42; systemctl show " + quotedUnit + " --no-pager"
	for _, property := range properties {
		cmd += " -p " + property
	}
	cmd += "; printf '\n" + markerStatus + "\n'; systemctl status " + quotedUnit + " --no-pager -l 2>&1 || true; printf '\n" + markerDeps + "\n'; systemctl list-dependencies " + quotedUnit + " --no-pager --plain 2>&1 || true"
	result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
	if err != nil {
		return systemdUnitInspection{}, result, err
	}
	return parseSystemdUnitInspection(unit, result.Output, markerStatus, markerDeps), result, nil
}

func parseSystemdUnitInspection(unit string, output string, statusMarker string, depsMarker string) systemdUnitInspection {
	inspection := systemdUnitInspection{Unit: unit, Properties: map[string]string{}, Dependencies: []string{}}
	propertiesText, remainder, foundStatus := strings.Cut(output, "\n"+statusMarker+"\n")
	if !foundStatus {
		propertiesText = output
	}
	for _, line := range strings.Split(propertiesText, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) != "" {
			inspection.Properties[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	if !foundStatus {
		return inspection
	}
	statusText, depsText, foundDeps := strings.Cut(remainder, "\n"+depsMarker+"\n")
	inspection.StatusText = strings.TrimSpace(statusText)
	if !foundDeps {
		return inspection
	}
	for _, line := range strings.Split(depsText, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			inspection.Dependencies = append(inspection.Dependencies, line)
		}
	}
	return inspection
}

func (s *Server) listServices(ctx context.Context, target string, manager string) ([]serviceInfo, error) {
	cmd := ""
	switch manager {
	case "systemd":
		cmd = `command -v systemctl >/dev/null 2>&1 || exit 42; systemctl list-unit-files --type=service --no-legend --no-pager 2>/dev/null | head -200 | while read -r unit enabled rest; do [ -n "$unit" ] || continue; active="$(systemctl is-active "$unit" 2>/dev/null || true)"; [ -n "$active" ] || active="unknown"; desc="$(systemctl show "$unit" -p Description --value 2>/dev/null || true)"; [ -n "$desc" ] || desc="$unit"; printf '%s|%s|%s|%s\n' "$unit" "$enabled" "$active" "$desc"; done`
	case "openrc":
		cmd = `command -v rc-service >/dev/null 2>&1 || exit 42; for f in /etc/init.d/*; do [ -f "$f" ] || continue; n=${f##*/}; rc-service "$n" status >/dev/null 2>&1; r=$?; rc-update show 2>/dev/null | grep -q "^[[:space:]]*$n[[:space:]]"; e=$?; state="stopped"; [ "$r" = 0 ] && state="running"; enabled="disabled"; [ "$e" = 0 ] && enabled="enabled"; echo "$n|$enabled|$state|$n"; done | head -200`
	case "procd":
		cmd = `command -v service >/dev/null 2>&1 || [ -d /etc/init.d ] || exit 42; for f in /etc/init.d/*; do [ -f "$f" ] || continue; n=${f##*/}; "$f" enabled >/dev/null 2>&1; e=$?; "$f" running >/dev/null 2>&1; r=$?; state="stopped"; [ "$r" = 0 ] && state="running"; enabled="disabled"; [ "$e" = 0 ] && enabled="enabled"; echo "$n|$enabled|$state|$n"; done | head -200`
	default:
		return nil, fmt.Errorf("unsupported service manager")
	}
	result, err := s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
	if err != nil {
		if result.ExitCode == 42 {
			return nil, nil
		}
		return nil, err
	}
	items := make([]serviceInfo, 0)
	for _, line := range strings.Split(result.Output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		item, ok := parseServiceInfoLine(line)
		if !ok {
			continue
		}
		items = append(items, item)
	}
	return items, nil
}

func parseServiceInfoLine(line string) (serviceInfo, bool) {
	parts := strings.SplitN(line, "|", 4)
	if len(parts) < 2 {
		return serviceInfo{}, false
	}
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return serviceInfo{}, false
	}
	enableState := normalizeServiceEnableState(parts[1])
	state := "unknown"
	if len(parts) > 2 {
		state = normalizeServiceRunState(parts[2])
	}
	desc := name
	if len(parts) > 3 && strings.TrimSpace(parts[3]) != "" {
		desc = strings.TrimSpace(parts[3])
	}
	return serviceInfo{
		Name:        name,
		State:       state,
		EnableState: enableState,
		Enabled:     serviceEnableStateEnabled(enableState),
		Running:     serviceRunStateRunning(state),
		Description: desc,
	}, true
}

func normalizeServiceRunState(value string) string {
	state := strings.ToLower(strings.TrimSpace(value))
	if state == "" {
		return "unknown"
	}
	switch state {
	case "active", "running", "started", "online":
		return "running"
	case "inactive", "stopped", "dead", "exited":
		return "stopped"
	case "activating", "starting":
		return "starting"
	case "deactivating", "stopping":
		return "stopping"
	case "failed", "crashed":
		return "failed"
	default:
		if strings.Contains(state, "running") {
			return "running"
		}
		if strings.Contains(state, "inactive") || strings.Contains(state, "stopped") {
			return "stopped"
		}
		return state
	}
}

func serviceRunStateRunning(state string) bool {
	return normalizeServiceRunState(state) == "running"
}

func normalizeServiceEnableState(value string) string {
	state := strings.ToLower(strings.TrimSpace(value))
	if state == "" {
		return "unknown"
	}
	if fields := strings.Fields(state); len(fields) > 0 {
		state = fields[0]
	}
	return state
}

func serviceEnableStateEnabled(state string) bool {
	state = normalizeServiceEnableState(state)
	return state == "enabled" || strings.HasPrefix(state, "enabled-")
}

func (s *Server) serviceAction(ctx context.Context, target string, manager string, service string, action string) (cliCommandResult, error) {
	service, err := cleanServiceName(service)
	if err != nil {
		return cliCommandResult{}, err
	}
	allowed := map[string]map[string]bool{
		"systemd": {"start": true, "stop": true, "restart": true, "enable": true, "disable": true, "mask": true, "unmask": true},
		"openrc":  {"start": true, "stop": true, "restart": true, "enable": true, "disable": true},
		"procd":   {"start": true, "stop": true, "restart": true, "reload": true, "enable": true, "disable": true},
	}
	if !allowed[manager][action] {
		return cliCommandResult{}, fmt.Errorf("unsupported %s action %q", manager, action)
	}
	qsvc := shellQuote(service)
	cmd := ""
	switch manager {
	case "systemd":
		cmd = "systemctl " + action + " " + qsvc
	case "openrc":
		if action == "enable" || action == "disable" {
			cmd = "rc-update " + action + " " + qsvc
		} else {
			cmd = "rc-service " + qsvc + " " + action
		}
	case "procd":
		cmd = "/etc/init.d/" + shellQuote(service) + " " + action
	}
	return s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", cmd)
}

func (s *Server) handleSparseAction(w http.ResponseWriter, r *http.Request, target string, parts []string) {
	if r.Method != http.MethodPost || len(parts) != 1 {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	action := strings.ToLower(strings.TrimSpace(parts[0]))
	if action != "migrate" && action != "resize" {
		writeJSON(w, http.StatusNotFound, apiError{Error: "unknown sparse action"})
		return
	}
	var req sparseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	if action == "migrate" && req.SizeGB == 0 {
		req.SizeGB = defaultRootfsImageSizeGB
	}
	if req.SizeGB < minRootfsImageSizeGB || req.SizeGB > maxRootfsImageSizeGB {
		writeJSON(w, http.StatusBadRequest, apiError{Error: fmt.Sprintf("sizeGb must be between %d and %d", minRootfsImageSizeGB, maxRootfsImageSizeGB)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	willStop, _ := s.containerRunning(ctx, target)
	cancel()
	task := s.newTask("sparse-"+action, target)
	s.updateTask(task.ID, func(t *taskState) {
		t.Status = "running"
		t.Percent = 1
		t.WillStopContainer = willStop
		t.RestoreAfterBackup = willStop
	})
	go s.runSparseTask(task.ID, target, action, req.SizeGB)
	task, _ = s.getTask(task.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task, "willStopContainer": willStop})
}

func (s *Server) runSparseTask(taskID string, target string, action string, sizeGB int) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	restore := false
	if running, _ := s.containerRunning(ctx, target); running {
		restore = true
		s.appendTaskLog(taskID, "Stopping container before sparse image operation...")
		result, err := s.lifecycleViaCLI(ctx, target, "stop")
		if err != nil {
			s.failTask(taskID, fmt.Errorf("failed to stop container: %v\n%s", err, result.Output))
			return
		}
		s.updateTask(taskID, func(t *taskState) { t.StoppedContainer = true })
	}
	defer func() {
		if !restore {
			return
		}
		startCtx, startCancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer startCancel()
		result, err := s.lifecycleViaCLI(startCtx, target, "start")
		if err != nil {
			s.updateTask(taskID, func(t *taskState) {
				message := fmt.Sprintf("failed to restart container after sparse operation: %v\n%s", err, result.Output)
				t.RestoreError = message
				if t.Status == "error" && t.Error != "" {
					t.Error += "\n" + message
				}
			})
			return
		}
		s.updateTask(taskID, func(t *taskState) { t.RestoredContainer = true })
	}()
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
	s.updateTask(taskID, func(t *taskState) { t.Percent = 8 })
	if action == "migrate" {
		err = s.migrateContainerToSparse(ctx, taskID, target, rootfsPath, sizeGB)
	} else {
		err = s.resizeContainerSparse(ctx, taskID, target, rootfsPath, sizeGB)
	}
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	s.completeTask(taskID, rootfsPath, "")
}

func (s *Server) migrateContainerToSparse(ctx context.Context, taskID string, target string, rootfsPath string, sizeGB int) error {
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("container rootfs is not a directory; use resize for rootfs.img")
	}
	containerDir := filepath.Dir(rootfsPath)
	finalImage := filepath.Join(containerDir, "rootfs.img")
	if _, err := os.Stat(finalImage); err == nil {
		return fmt.Errorf("rootfs.img already exists")
	}
	tmpImage := filepath.Join(containerDir, ".rootfs-"+taskID+".img")
	mountPoint := filepath.Join(containerDir, ".sparse-mount-"+taskID)
	defer os.Remove(tmpImage)
	defer os.RemoveAll(mountPoint)
	s.appendTaskLog(taskID, fmt.Sprintf("Creating %dGB sparse image...", sizeGB))
	if err := s.truncateRootfsImage(ctx, tmpImage, sizeGB); err != nil {
		return err
	}
	s.updateTask(taskID, func(t *taskState) { t.Percent = 20 })
	if err := s.formatRootfsImage(ctx, tmpImage); err != nil {
		return err
	}
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		return err
	}
	s.updateTask(taskID, func(t *taskState) { t.Percent = 35 })
	if err := s.mountRootfsImage(ctx, tmpImage, mountPoint); err != nil {
		return err
	}
	mounted := true
	defer func() {
		if mounted {
			_ = s.umountRootfsImage(context.Background(), mountPoint)
		}
	}()
	s.appendTaskLog(taskID, "Copying rootfs into sparse image...")
	if err := s.copyRootfsDirectoryInto(ctx, rootfsPath, mountPoint); err != nil {
		return err
	}
	s.updateTask(taskID, func(t *taskState) { t.Percent = 68 })
	if err := s.umountRootfsImage(ctx, mountPoint); err != nil {
		return err
	}
	mounted = false
	_ = exec.CommandContext(ctx, "chcon", "u:object_r:vold_data_file:s0", tmpImage).Run()
	if err := s.fsckRootfsImage(ctx, tmpImage); err != nil {
		return err
	}
	if err := os.Rename(tmpImage, finalImage); err != nil {
		return err
	}
	backupDir := filepath.Join(containerDir, "rootfs.bak-"+taskID)
	if err := os.Rename(rootfsPath, backupDir); err != nil {
		_ = os.Remove(finalImage)
		return err
	}
	updates := map[string]string{"rootfs_path": finalImage, "use_sparse_image": "1", "sparse_image_size_gb": strconv.Itoa(sizeGB)}
	configPath := filepath.Join(containerDir, "container.config")
	if err := workspace.UpdateContainerConfig(configPath, updates); err != nil {
		_ = os.Rename(backupDir, rootfsPath)
		_ = os.Remove(finalImage)
		return err
	}
	_ = os.RemoveAll(backupDir)
	s.appendTaskLog(taskID, "Sparse image migration complete.")
	return nil
}

func (s *Server) resizeContainerSparse(ctx context.Context, taskID string, target string, rootfsPath string, sizeGB int) error {
	if !strings.HasSuffix(strings.ToLower(rootfsPath), ".img") {
		return fmt.Errorf("container rootfs is not rootfs.img")
	}
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return err
	}
	targetBytes := int64(sizeGB) * 1024 * 1024 * 1024
	if info.Size() < targetBytes {
		s.appendTaskLog(taskID, "Expanding sparse file before filesystem resize...")
		if err := s.truncateRootfsImage(ctx, rootfsPath, sizeGB); err != nil {
			return err
		}
	}
	s.appendTaskLog(taskID, "Checking sparse image filesystem...")
	if err := s.fsckRootfsImage(ctx, rootfsPath); err != nil {
		return err
	}
	s.updateTask(taskID, func(t *taskState) { t.Percent = 35 })
	s.appendTaskLog(taskID, fmt.Sprintf("Resizing filesystem to %dG...", sizeGB))
	cmd := exec.CommandContext(ctx, "resize2fs", rootfsPath, fmt.Sprintf("%dG", sizeGB))
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "is now") {
		return fmt.Errorf("resize2fs failed: %v\n%s", err, string(out))
	}
	s.updateTask(taskID, func(t *taskState) { t.Percent = 70 })
	if info.Size() > targetBytes {
		s.appendTaskLog(taskID, "Shrinking sparse file after filesystem resize...")
		if err := s.truncateRootfsImage(ctx, rootfsPath, sizeGB); err != nil {
			return err
		}
	}
	if err := s.fsckRootfsImage(ctx, rootfsPath); err != nil {
		return err
	}
	containerDir := filepath.Dir(rootfsPath)
	configPath := filepath.Join(containerDir, "container.config")
	if err := workspace.UpdateContainerConfig(configPath, map[string]string{"use_sparse_image": "1", "sparse_image_size_gb": strconv.Itoa(sizeGB)}); err != nil {
		return err
	}
	s.appendTaskLog(taskID, "Sparse image resize complete.")
	return nil
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	cpuUsage := s.cpuUsagePercent()
	memory := readMemoryReport()
	network := readNetworkReport()
	battery := s.disabledBatteryReport()
	if s.batteryMonitoringEnabledSetting() {
		battery = s.normalizeBatteryReport(readBatteryReport())
		battery.Enabled = true
		battery.Stats = s.updateBatteryStats(battery, time.Now())
	}
	systemVersion := readSystemVersion()
	kernelVersion := readKernelVersion()
	uptimeSeconds := readHostUptimeSeconds()

	writeJSON(w, http.StatusOK, map[string]any{
		"time":          time.Now().Unix(),
		"uptimeSeconds": uptimeSeconds,
		"goos":          runtime.GOOS,
		"goarch":        runtime.GOARCH,
		"goVersion":     runtime.Version(),
		"numCPU":        runtime.NumCPU(),
		"systemVersion": systemVersion,
		"kernelVersion": kernelVersion,
		"cpuUsage":      cpuUsage,
		"memory":        memory,
		"network":       network,
		"battery":       battery,
		"resources": map[string]any{
			"cpuUsage": cpuUsage,
			"memory":   memory,
			"network":  network,
			"battery":  battery,
		},
		"paths": []pathReport{
			s.pathReport("workspace", s.workspace),
			s.pathReport("templateImageRoot", s.templateImageRoot),
			s.pathReport("imageRoot", s.imageRoot),
			s.pathReport("corePath", s.corePath),
			s.pathReport("droidspacesPath", s.droidspacesPath),
			s.pathReport("configPath", s.configPath),
		},
	})
}

func readHostUptimeSeconds() uint64 {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0
	}
	seconds, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0 {
		return 0
	}
	return uint64(seconds)
}

func (s *Server) handleBatteryPower(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	hours := 24
	if raw := strings.TrimSpace(r.URL.Query().Get("hours")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > batteryStatsMaxPowerRangeHours {
			writeJSON(w, http.StatusBadRequest, apiError{Error: fmt.Sprintf("hours must be between 1 and %d", batteryStatsMaxPowerRangeHours)})
			return
		}
		hours = value
	}
	if !s.batteryMonitoringEnabledSetting() {
		writeJSON(w, http.StatusOK, s.disabledBatteryPowerRangeReport(hours, time.Now()))
		return
	}
	report, err := s.batteryPowerRangeReport(hours, time.Now())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) startBatteryStatsSampler() {
	if s.disableBatterySampler || !s.batteryMonitoringEnabledSetting() {
		return
	}
	s.batterySamplerMu.Lock()
	if s.batterySamplerCancel != nil {
		s.batterySamplerMu.Unlock()
		return
	}
	if !s.batteryMonitoringEnabledSetting() {
		s.batterySamplerMu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	s.batterySamplerCtx = ctx
	s.batterySamplerCancel = cancel
	s.batterySamplerDone = done
	s.batterySamplerMu.Unlock()

	go func() {
		defer func() {
			s.batterySamplerMu.Lock()
			if s.batterySamplerCtx == ctx {
				s.batterySamplerCtx = nil
				s.batterySamplerCancel = nil
				s.batterySamplerDone = nil
			}
			close(done)
			s.batterySamplerMu.Unlock()
		}()
		s.collectBatteryStatsSample(time.Now())
		for {
			timer := time.NewTimer(s.batteryStatsSampleInterval())
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
				s.collectBatteryStatsSample(time.Now())
			}
		}
	}()
}

func (s *Server) stopBatteryStatsSampler() {
	s.batterySamplerMu.Lock()
	cancel := s.batterySamplerCancel
	done := s.batterySamplerDone
	s.batterySamplerCtx = nil
	s.batterySamplerCancel = nil
	s.batterySamplerDone = nil
	s.batterySamplerMu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *Server) collectBatteryStatsSample(now time.Time) batteryStatsReport {
	if !s.batteryMonitoringEnabledSetting() {
		return s.disabledBatteryReport().Stats
	}
	battery := s.normalizeBatteryReport(readBatteryReport())
	battery.Enabled = true
	return s.updateBatteryStats(battery, now)
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
	req.RootFSSource = strings.ToLower(strings.TrimSpace(req.RootFSSource))
	req.CloudRootFSURL = strings.TrimSpace(req.CloudRootFSURL)
	if req.RootFSSource == "cloud" && strings.TrimSpace(req.RootFSTaskID) == "" && req.CloudRootFSURL == "" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "cloudRootfsUrl is required for cloud source"})
		return
	}

	name, err := cleanTarget(strings.TrimSpace(req.Name))
	if err != nil || hasConfigUnsafeChars(name) {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid container name"})
		return
	}
	req.Name = name

	netMode := strings.ToLower(strings.TrimSpace(req.NetMode))
	if netMode == "" {
		netMode = "host"
	}
	if netMode != "host" && netMode != "nat" && netMode != "none" && netMode != "gateway" {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "netMode must be host, nat, none, or gateway"})
		return
	}
	req.NetMode = netMode
	// The core detects the NAT uplink since v6.5. Keep accepting these legacy
	// fields so older clients can create containers, but never persist them.
	req.NATUpstreamIfnames = ""
	req.NATUpstreamIfname = ""
	if netModeForcesDisableIPv6(req.NetMode) {
		req.DisableIPv6 = true
	}
	if !req.TermuxX11 {
		req.Tx11ExtraFlags = ""
	}
	if !req.VirGL {
		req.VirGLExtraFlags = ""
	}

	for _, value := range []string{req.Hostname, req.DNSServers, req.PortForwards, req.StaticNATIP, req.NATUpstreamIfnames, req.NATUpstreamIfname, req.GatewayContainer, req.GatewayNet, req.GatewayLanIfname, req.GatewayBridge, req.PrivilegedMode, req.BindMounts, req.CustomInit, req.Tx11ExtraFlags, req.VirGLExtraFlags, req.MemoryLimit, req.CPUs, req.PidsLimit} {
		if hasConfigUnsafeChars(value) {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "config values must not contain newlines"})
			return
		}
	}
	if err := validateCloudInitDocument("cloudInitUserData", req.CloudInitUserData); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if err := validateCloudInitDocument("cloudInitNetworkConfig", req.CloudInitNetwork); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if err := s.validateCreateContainerConfig(name, netMode, req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
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

	releaseNATIP, err := s.reserveCreateNATIP(name, &req)
	if err != nil {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}

	releasePorts, err := s.reservePortForwards(name, req.PortForwards, netMode)
	if err != nil {
		releaseNATIP()
		writePortForwardValidationError(w, err)
		return
	}

	task := s.newTask("container-create", name)
	taskID := task.ID
	s.updateTask(task.ID, func(t *taskState) {
		t.Percent = 1
	})
	go func() {
		defer releasePorts()
		defer releaseNATIP()
		s.runCreateContainerTask(taskID, req)
	}()
	task, _ = s.getTask(taskID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
}

func (s *Server) runCreateContainerTask(taskID string, req createContainerRequest) {
	taskTimeout := 2 * time.Hour
	if strings.EqualFold(strings.TrimSpace(req.RootFSSource), "cloud") && strings.TrimSpace(req.RootFSTaskID) == "" && strings.TrimSpace(req.CloudRootFSURL) != "" {
		taskTimeout = rootfsDownloadTaskTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), taskTimeout)
	defer cancel()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		s.failTask(taskID, fmt.Errorf("container name is empty"))
		return
	}
	containerDir, err := s.containerDir(name)
	if err != nil {
		s.failTask(taskID, err)
		return
	}

	s.updateTask(taskID, func(t *taskState) {
		t.Status = "running"
		t.Percent = 3
	})
	s.appendTaskLog(taskID, "Starting container installation...")
	s.appendTaskLog(taskID, "Container: "+name)

	if _, err := os.Stat(containerDir); err == nil {
		s.failTask(taskID, fmt.Errorf("container already exists"))
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		s.failTask(taskID, err)
		return
	}

	s.appendTaskLog(taskID, "Creating container directory: "+containerDir)
	if err := os.MkdirAll(containerDir, 0755); err != nil {
		s.failTask(taskID, err)
		return
	}
	cleanupContainerDir := true
	defer func() {
		if cleanupContainerDir {
			_ = os.RemoveAll(containerDir)
		}
	}()

	s.updateTask(taskID, func(t *taskState) { t.Percent = 10 })
	s.appendTaskLog(taskID, "Preparing rootfs...")
	rootfsPath, err := s.resolveCreateRootfs(ctx, taskID, &req, containerDir)
	if err != nil {
		s.appendTaskLog(taskID, "Rootfs preparation failed: "+err.Error())
		s.failTask(taskID, err)
		return
	}
	s.updateTask(taskID, func(t *taskState) {
		if t.Percent < 60 {
			t.Percent = 60
		}
	})
	s.appendTaskLog(taskID, "Rootfs ready: "+rootfsPath)
	rootfsSource := strings.ToLower(strings.TrimSpace(req.RootFSSource))
	if rootfsSource == "" {
		rootfsSource = "direct"
	}
	if cloudInitEnabled(req) && rootfsSource != "direct" {
		generatedNetwork, err := prepareCloudInitNATNetworkConfig(&req)
		if err != nil {
			s.appendTaskLog(taskID, "cloud-init NAT network preparation failed: "+err.Error())
			s.failTask(taskID, err)
			return
		}
		if generatedNetwork {
			s.appendTaskLog(taskID, "Prepared cloud-init static NAT network configuration for "+req.StaticNATIP+".")
		}
		s.appendTaskLog(taskID, "Writing cloud-init NoCloud initialization data...")
		if err := s.applyCloudInitToRootfs(ctx, rootfsPath, req); err != nil {
			s.appendTaskLog(taskID, "cloud-init preparation failed: "+err.Error())
			s.failTask(taskID, err)
			return
		}
		s.appendTaskLog(taskID, "cloud-init NoCloud initialization data is ready.")
	}

	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = sanitizeContainerName(name)
	}
	configPath := filepath.Join(containerDir, "container.config")
	content := s.containerConfigContent(name, hostname, rootfsPath, req.NetMode, req)

	s.updateTask(taskID, func(t *taskState) { t.Percent = 65 })
	s.appendTaskLog(taskID, "Writing container configuration...")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		s.failTask(taskID, err)
		return
	}
	s.appendTaskLog(taskID, "Container configuration saved: "+configPath)

	if strings.TrimSpace(req.Env) != "" {
		s.appendTaskLog(taskID, "Writing environment variables (.env)...")
		if err := os.WriteFile(filepath.Join(containerDir, ".env"), []byte(strings.TrimRight(req.Env, "\r\n")+"\n"), 0644); err != nil {
			s.failTask(taskID, err)
			return
		}
	}

	s.updateTask(taskID, func(t *taskState) { t.Percent = 78 })
	s.appendTaskLog(taskID, "Verifying installation...")
	if req.UseSparseImage != nil && *req.UseSparseImage {
		if _, err := os.Stat(rootfsPath); err != nil {
			s.failTask(taskID, fmt.Errorf("container sparse image not found after extraction: %w", err))
			return
		}
	} else if strings.HasSuffix(strings.ToLower(rootfsPath), ".img") {
		if _, err := os.Stat(rootfsPath); err != nil {
			s.failTask(taskID, fmt.Errorf("container sparse image not found after extraction: %w", err))
			return
		}
	} else if info, err := os.Stat(rootfsPath); err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("not a directory")
		}
		s.failTask(taskID, fmt.Errorf("container rootfs directory not found after extraction: %w", err))
		return
	}
	s.appendTaskLog(taskID, "Container installed successfully!")
	cleanupContainerDir = false

	if req.Start {
		s.updateTask(taskID, func(t *taskState) { t.Percent = 84 })
		s.appendTaskLog(taskID, "")
		s.appendTaskLog(taskID, "Starting container...")
		result, startErr := s.runDroidspacesLogged(ctx, taskID, "--config="+configPath, "start")
		s.updateTask(taskID, func(t *taskState) {
			t.ExitCode = result.ExitCode
		})
		if startErr != nil {
			s.failTask(taskID, fmt.Errorf("failed to start container: %v", startErr))
			return
		}
		s.appendTaskLog(taskID, "Container started successfully.")
		if req.NetMode == "nat" && s.nestedAndroidNATCompatEnabled() {
			s.appendTaskLog(taskID, "Reconciling nested Android NAT compatibility...")
			compatCtx, compatCancel := context.WithTimeout(ctx, 12*time.Second)
			compatErr := s.reconcileNestedAndroidNATCompat(compatCtx)
			compatCancel()
			if compatErr != nil {
				s.appendTaskLog(taskID, "Nested Android NAT compatibility warning: "+compatErr.Error())
				s.recordBackendDiagnostic("nat-compat", compatErr)
			} else {
				s.appendTaskLog(taskID, "Nested Android NAT policy routing is ready. Outer Droidspaces FORWARD policy must permit ds-br0 traffic.")
			}
		}
		if req.NetMode == "nat" {
			s.updateTask(taskID, func(t *taskState) { t.Percent = 92 })
			s.appendTaskLog(taskID, "")
			s.appendTaskLog(taskID, "Running NAT network diagnostics...")
			diagCtx, diagCancel := context.WithTimeout(ctx, 45*time.Second)
			diag, _ := s.runContainerNetworkDiagnostics(diagCtx, name)
			diagCancel()
			if strings.TrimSpace(diag.Output) != "" {
				s.appendTaskLog(taskID, strings.TrimRight(diag.Output, "\n"))
			}
			if diag.ExitCode != 0 {
				s.appendTaskLog(taskID, fmt.Sprintf("NAT network diagnostics reported a problem (exit=%d).", diag.ExitCode))
			} else {
				s.appendTaskLog(taskID, "NAT network diagnostics passed.")
			}
		}
	}

	s.completeTask(taskID, configPath, "")
}

func (s *Server) updateContainerConfig(w http.ResponseWriter, r *http.Request, target string) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	containerDir, err := s.containerDir(target)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	configPath := filepath.Join(containerDir, "container.config")
	if _, err := os.Stat(configPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, http.StatusNotFound, apiError{Error: "container config not found"})
			return
		}
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}

	var req updateContainerConfigRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
		return
	}
	updates, err := s.containerConfigUpdates(target, req)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}
	if len(updates) == 0 && req.Env == nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: "no supported config fields provided"})
		return
	}
	currentValues := readContainerConfigValues(configPath)
	finalNetMode := strings.ToLower(strings.TrimSpace(currentValues["net_mode"]))
	if finalNetMode == "" {
		finalNetMode = "host"
	}
	if value, ok := updates["net_mode"]; ok {
		finalNetMode = value
	}
	finalPortForwards := strings.TrimSpace(currentValues["port_forwards"])
	if value, ok := updates["port_forwards"]; ok {
		finalPortForwards = value
	}
	releaseNATIP, err := s.reserveUpdateNATIP(target, finalNetMode, updates)
	if err != nil {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}
	defer releaseNATIP()
	releasePorts, err := s.reservePortForwards(target, finalPortForwards, finalNetMode)
	if err != nil {
		writePortForwardValidationError(w, err)
		return
	}
	defer releasePorts()

	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	running, _ := s.containerRunning(ctx, target)
	restore := running
	if req.RestoreAfterUpdate != nil {
		restore = *req.RestoreAfterUpdate
	}
	if req.Restore != nil {
		restore = *req.Restore
	}

	resp := map[string]any{"ok": true, "name": target, "configPath": configPath, "stopped": false, "restarted": false, "restoreError": ""}
	stopped := false
	if running {
		result, stopErr := s.lifecycleViaCLI(ctx, target, "stop")
		resp["stopExitCode"] = result.ExitCode
		resp["stopOutput"] = result.Output
		if stopErr != nil {
			writeJSON(w, http.StatusBadGateway, apiError{Error: fmt.Sprintf("failed to stop container before config update: %v\n%s", stopErr, result.Output)})
			return
		}
		stopped = true
		resp["stopped"] = true
	}

	restoreIfNeeded := func() string {
		if !stopped || !restore {
			return ""
		}
		result, startErr := s.lifecycleViaCLI(ctx, target, "start")
		resp["startExitCode"] = result.ExitCode
		resp["startOutput"] = result.Output
		if startErr != nil {
			message := fmt.Sprintf("%v\n%s", startErr, result.Output)
			resp["restoreError"] = message
			return message
		}
		resp["restarted"] = true
		return ""
	}

	if len(updates) > 0 {
		if err := workspace.UpdateContainerConfig(configPath, updates); err != nil {
			restoreErr := restoreIfNeeded()
			message := err.Error()
			if restoreErr != "" {
				message += "\nrestore failed: " + restoreErr
			}
			writeJSON(w, http.StatusBadGateway, apiError{Error: message})
			return
		}
	}
	if req.Env != nil {
		envPath := filepath.Join(containerDir, ".env")
		if err := os.WriteFile(envPath, []byte(strings.TrimRight(*req.Env, "\r\n")+"\n"), 0644); err != nil {
			restoreErr := restoreIfNeeded()
			message := err.Error()
			if restoreErr != "" {
				message += "\nrestore failed: " + restoreErr
			}
			writeJSON(w, http.StatusBadGateway, apiError{Error: message})
			return
		}
		resp["envPath"] = envPath
	}
	resp["updated"] = updates

	restoreIfNeeded()
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) containerConfigUpdates(target string, req updateContainerConfigRequest) (map[string]string, error) {
	// A configured uplink bypasses the core's automatic NAT detection. Every
	// config write clears old WebUI-generated values, including requests from
	// older clients that still send either legacy JSON field.
	updates := map[string]string{
		"nat_upstream_ifnames": "",
		"upstream_interfaces":  "",
	}
	requestedNetMode := ""
	addString := func(key string, value *string) error {
		if value == nil {
			return nil
		}
		cleaned := strings.TrimSpace(*value)
		if hasConfigUnsafeChars(cleaned) || len(cleaned) > 4096 {
			return fmt.Errorf("invalid %s", key)
		}
		updates[key] = cleaned
		return nil
	}
	if req.NetMode != nil {
		netMode := strings.ToLower(strings.TrimSpace(*req.NetMode))
		if netMode != "host" && netMode != "nat" && netMode != "none" && netMode != "gateway" {
			return nil, fmt.Errorf("netMode must be host, nat, none, or gateway")
		}
		requestedNetMode = netMode
		updates["net_mode"] = netMode
	}
	for _, item := range []struct {
		key   string
		value *string
	}{
		{"hostname", req.Hostname},
		{"dns_servers", req.DNSServers},
		{"port_forwards", req.PortForwards},
		{"bind_mounts", req.BindMounts},
		{"custom_init", req.CustomInit},
		{"gateway_container", req.GatewayContainer},
		{"gateway_net", req.GatewayNet},
		{"gateway_lan_ifname", req.GatewayLanIfname},
		{"gateway_bridge", req.GatewayBridge},
	} {
		if err := addString(item.key, item.value); err != nil {
			return nil, err
		}
	}
	if req.StaticNATIP != nil {
		staticIP := strings.TrimSpace(*req.StaticNATIP)
		if staticIP != "" {
			if err := validateStaticNATIP(staticIP); err != nil {
				return nil, err
			}
		}
		updates["static_nat_ip"] = staticIP
	}
	if req.PrivilegedMode != nil {
		privileged, err := normalizePrivilegedMode(*req.PrivilegedMode)
		if err != nil {
			return nil, err
		}
		updates["privileged"] = privileged
		if privilegedDisablesDeadlock(privileged) {
			updates["block_nested_ns"] = "0"
		}
	}
	if requestedNetMode == "host" || requestedNetMode == "none" || requestedNetMode == "gateway" {
		if req.PortForwards == nil {
			updates["port_forwards"] = ""
		}
	}
	if requestedNetMode == "host" || requestedNetMode == "none" || requestedNetMode == "gateway" {
		if req.StaticNATIP == nil {
			updates["static_nat_ip"] = ""
		}
	}
	if req.Env != nil {
		if strings.ContainsAny(*req.Env, "\x00\r") || len(*req.Env) > 1<<20 {
			return nil, fmt.Errorf("invalid env")
		}
	}
	for _, item := range []struct {
		key   string
		value *string
	}{
		{"tx11_extra_flags", req.Tx11ExtraFlags},
		{"virgl_extra_flags", req.VirGLExtraFlags},
	} {
		if err := addString(item.key, item.value); err != nil {
			return nil, err
		}
	}
	if req.TermuxX11 != nil && !*req.TermuxX11 {
		updates["tx11_extra_flags"] = ""
	}
	if req.VirGL != nil && !*req.VirGL {
		updates["virgl_extra_flags"] = ""
	}
	if req.MemoryLimit != nil {
		value, err := normalizeMemoryLimit(*req.MemoryLimit)
		if err != nil {
			return nil, err
		}
		updates["memory_limit"] = value
	}
	if req.CPUs != nil {
		quota, period, err := normalizeCPULimit(*req.CPUs)
		if err != nil {
			return nil, err
		}
		updates["cpu_quota"] = quota
		updates["cpu_period"] = period
	}
	if req.PidsLimit != nil {
		value, err := normalizePidsLimit(*req.PidsLimit)
		if err != nil {
			return nil, err
		}
		updates["pids_limit"] = value
	}
	if req.RunAtBootPriority != nil {
		priority := *req.RunAtBootPriority
		if priority < 0 || priority > 10000 {
			return nil, fmt.Errorf("runAtBootPriority must be between 0 and 10000")
		}
		if priority == 0 {
			updates["run_at_boot_priority"] = ""
		} else {
			updates["run_at_boot_priority"] = strconv.Itoa(priority)
		}
	}
	addBool := func(key string, value *bool) {
		if value != nil {
			updates[key] = boolFlag(*value)
		}
	}
	addBool("disable_ipv6", req.DisableIPv6)
	addBool("enable_android_storage", req.AndroidStorage)
	addBool("enable_hw_access", req.HWAccess)
	addBool("enable_gpu_mode", req.GPUMode)
	addBool("enable_termux_x11", req.TermuxX11)
	addBool("enable_virgl", req.VirGL)
	addBool("enable_pulseaudio", req.PulseAudio)
	addBool("selinux_permissive", req.SELinuxPermissive)
	addBool("volatile_mode", req.VolatileMode)
	addBool("run_at_boot", req.RunAtBoot)
	addBool("force_cgroupv1", req.ForceCgroupV1)
	if req.RunAtBoot != nil && !*req.RunAtBoot {
		updates["run_at_boot_priority"] = ""
	}
	effectivePrivileged, privilegedUpdated := updates["privileged"]
	if !privilegedUpdated {
		if configPath, ok := s.containerConfigPath(target); ok {
			effectivePrivileged = readContainerConfigValues(configPath)["privileged"]
		}
	}
	if privilegedDisablesDeadlock(effectivePrivileged) {
		updates["allow_userns"] = "1"
	} else {
		addBool("allow_userns", req.AllowUserNS)
	}
	if req.BlockNestedNS != nil && !privilegedDisablesDeadlock(effectivePrivileged) {
		updates["block_nested_ns"] = boolFlag(*req.BlockNestedNS)
	}
	if netModeForcesDisableIPv6(s.effectiveUpdateNetMode(target, requestedNetMode, updates)) {
		updates["disable_ipv6"] = "1"
	}
	if err := s.validateGatewayUpdate(target, requestedNetMode, updates); err != nil {
		return nil, err
	}
	return updates, nil
}

func (s *Server) effectiveUpdateNetMode(target string, requestedNetMode string, updates map[string]string) string {
	mode := strings.ToLower(strings.TrimSpace(requestedNetMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(updates["net_mode"]))
	}
	if mode == "" {
		if configPath, ok := s.containerConfigPath(target); ok {
			mode = strings.ToLower(strings.TrimSpace(readContainerConfigValues(configPath)["net_mode"]))
		}
	}
	if mode == "" || mode == "unknown" {
		mode = "host"
	}
	return mode
}

func (s *Server) resolveCreateRootfs(ctx context.Context, taskID string, req *createContainerRequest, containerDir string) (string, error) {
	if req == nil {
		return "", fmt.Errorf("container creation request is required")
	}
	source := strings.ToLower(strings.TrimSpace(req.RootFSSource))
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
		preparedPath, err := s.prepareRootfsForContainer(ctx, *req, source, rootfsPath, containerDir)
		if err == nil {
			enableCloudInitForLocalTemplate(req, rootfsPath)
		}
		return preparedPath, err
	case "cloud":
		legacyTaskID := strings.TrimSpace(req.RootFSTaskID)
		if legacyTaskID != "" {
			task, ok := s.getTask(legacyTaskID)
			if !ok || task.Status != "done" || task.Path == "" {
				return "", fmt.Errorf("cloud rootfs download is not ready")
			}
			if !s.pathWithinManagedRoots(task.Path) {
				return "", fmt.Errorf("downloaded rootfs is outside managed roots")
			}
			s.appendTaskLog(taskID, "Using completed cloud rootfs task: "+legacyTaskID)
			preparedPath, err := s.prepareRootfsForContainer(ctx, *req, source, task.Path, containerDir)
			if err == nil {
				enableCloudInitForLocalTemplate(req, task.Path)
			}
			return preparedPath, err
		}

		cloudURL := strings.TrimSpace(req.CloudRootFSURL)
		if cloudURL == "" {
			return "", fmt.Errorf("cloudRootfsUrl is required for cloud source")
		}
		s.appendTaskLog(taskID, "Fetching configured cloud rootfs metadata...")
		asset, err := s.configuredRootfsAsset(ctx, cloudURL, rootfs.DeviceArch())
		if err != nil {
			return "", err
		}
		enableCloudInitForAsset(req, asset)
		s.appendTaskLog(taskID, "Selected cloud rootfs: "+asset.Name)
		s.updateTask(taskID, func(t *taskState) {
			t.Downloaded = 0
			t.Total = asset.SizeBytes
			if t.Percent < 12 {
				t.Percent = 12
			}
		})
		job, started, err := s.beginSharedRootfsDownload(asset)
		if err != nil {
			return "", err
		}
		if started {
			s.appendTaskLog(taskID, "Started shared cloud rootfs download task: "+job.taskID)
		} else {
			s.appendTaskLog(taskID, "Waiting for shared cloud rootfs download task: "+job.taskID)
		}
		downloadedPath, err := s.waitForSharedRootfsDownload(ctx, job, func(sharedTask *taskState) {
			s.updateTask(taskID, func(t *taskState) {
				t.Downloaded = sharedTask.Downloaded
				if sharedTask.Total > 0 {
					t.Total = sharedTask.Total
					progress := 12 + int(sharedTask.Downloaded*43/sharedTask.Total)
					if progress > 55 {
						progress = 55
					}
					if progress > t.Percent {
						t.Percent = progress
					}
				}
			})
		})
		if err != nil {
			return "", err
		}
		s.updateTask(taskID, func(t *taskState) {
			if t.Percent < 55 {
				t.Percent = 55
			}
		})
		s.appendTaskLog(taskID, "Cloud rootfs download completed: "+downloadedPath)
		s.appendTaskLog(taskID, "Preparing downloaded cloud rootfs...")
		return s.prepareRootfsForContainer(ctx, *req, source, downloadedPath, containerDir)
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
	if r.URL.Query().Get("async") == "1" {
		task, release, err := s.beginContainerTask("container-delete", target)
		if err != nil {
			writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
			return
		}
		s.updateTask(task.ID, func(t *taskState) {
			t.Status = "running"
			t.Percent = 1
		})
		task, _ = s.getTask(task.ID)
		go s.runDeleteContainerTask(task.ID, target, containerDir, release)
		writeJSON(w, http.StatusAccepted, map[string]any{
			"ok":     true,
			"name":   target,
			"taskId": task.ID,
			"task":   task,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	removedLogs, err := s.deleteContainerContents(ctx, target, containerDir, nil)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": target, "deleted": containerDir, "removedLogs": removedLogs})
}

func (s *Server) runDeleteContainerTask(taskID string, target string, containerDir string, release func()) {
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	s.appendTaskLog(taskID, "Preparing container deletion...")
	s.updateTask(taskID, func(t *taskState) { t.Percent = 10 })
	removedLogs, err := s.deleteContainerContents(ctx, target, containerDir, func(line string) {
		s.appendTaskLog(taskID, line)
	})
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	if len(removedLogs) > 0 {
		s.appendTaskLog(taskID, fmt.Sprintf("Removed %d container log paths.", len(removedLogs)))
	}
	s.appendTaskLog(taskID, "Container deletion completed.")
	s.completeTask(taskID, containerDir, "")
}

func (s *Server) deleteContainerContents(ctx context.Context, target string, containerDir string, logf func(string)) ([]string, error) {
	if running, _ := s.containerRunning(ctx, target); running {
		if logf != nil {
			logf("Stopping container before deletion...")
		}
		if _, _, err := s.performLifecycle(ctx, target, "stop", 15); err != nil {
			return nil, fmt.Errorf("failed to stop container before delete: %w", err)
		}
	}
	if logf != nil {
		logf("Removing container data...")
	}
	if err := os.RemoveAll(containerDir); err != nil {
		return nil, err
	}
	s.removePidSidecars(target)
	s.reconcileNestedAndroidNATCompatAsync()
	removedLogs, err := s.removeContainerLogs(target)
	if err != nil {
		return nil, err
	}
	return removedLogs, nil
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

func (s *Server) networkDiagnoseContainer(w http.ResponseWriter, r *http.Request, target string) {
	ctx, cancel := context.WithTimeout(r.Context(), 75*time.Second)
	defer cancel()
	result, _ := s.runContainerNetworkDiagnostics(ctx, target)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       result.ExitCode == 0,
		"exitCode": result.ExitCode,
		"output":   result.Output,
		"args":     result.Args,
	})
}

func (s *Server) runContainerNetworkDiagnostics(ctx context.Context, target string) (cliCommandResult, error) {
	return s.runDroidspaces(ctx, "--name", target, "run", "/bin/sh", "-lc", containerNetworkDiagnosticsCommand)
}

const containerNetworkDiagnosticsCommand = `status=0
section() { printf '\n[%s]\n' "$1"; }
run_check() {
  label="$1"
  shift
  section "$label"
  "$@" 2>&1
}
section container
uname -a 2>/dev/null || true
printf 'date: '; date 2>/dev/null || true
section interfaces
(ip -4 addr show 2>/dev/null || ifconfig -a 2>/dev/null || true)
section routes
(ip route show 2>/dev/null || route -n 2>/dev/null || true)
section resolver
(cat /etc/resolv.conf 2>/dev/null || true)
section gateway
gw="$(ip route show default 2>/dev/null | awk 'NR==1{for(i=1;i<=NF;i++) if($i=="via"){print $(i+1); exit}}')"
[ -n "$gw" ] || gw=172.28.0.1
printf 'gateway: %s\n' "$gw"
if ping -c 1 -W 3 "$gw" 2>&1; then
  printf 'gateway ping: ok\n'
else
  printf 'gateway ping: failed\n'
  status=1
fi
section public-ip
if ping -c 1 -W 5 1.1.1.1 2>&1; then
  printf 'public ping: ok\n'
else
  printf 'public ping: failed\n'
  status=1
fi
section dns
if nslookup dl-cdn.alpinelinux.org 2>&1 || getent hosts dl-cdn.alpinelinux.org 2>&1 || ping -c 1 -W 5 dl-cdn.alpinelinux.org 2>&1; then
  printf 'dns lookup: ok\n'
else
  printf 'dns lookup: failed\n'
  status=1
fi
exit "$status"`

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
	env := envWithoutKeys(os.Environ(), "PATH", "TERM", "COLORTERM", "ANDROID_ROOT", "ANDROID_DATA", "ANDROID_STORAGE", "EXTERNAL_STORAGE")
	pathParts := []string{}
	if s.corePath != "" {
		pathParts = append(pathParts, s.corePath)
	}
	if config.IsAndroid() {
		pathParts = append(pathParts,
			"/product/bin",
			"/apex/com.android.runtime/bin",
			"/apex/com.android.art/bin",
			"/system_ext/bin",
			"/system/bin",
			"/system/xbin",
			"/odm/bin",
			"/vendor/bin",
			"/vendor/xbin",
		)
		env = append(env,
			"ANDROID_ROOT="+envOrDefault("ANDROID_ROOT", "/system"),
			"ANDROID_DATA="+envOrDefault("ANDROID_DATA", "/data"),
			"ANDROID_STORAGE="+envOrDefault("ANDROID_STORAGE", "/storage"),
			"EXTERNAL_STORAGE="+envOrDefault("EXTERNAL_STORAGE", "/sdcard"),
		)
	}
	if pathValue := os.Getenv("PATH"); pathValue != "" {
		pathParts = append(pathParts, filepath.SplitList(pathValue)...)
	}
	if len(pathParts) == 0 {
		pathParts = filepath.SplitList(os.Getenv("PATH"))
	}
	env = append(env,
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"PATH="+strings.Join(uniqueNonEmpty(pathParts), string(os.PathListSeparator)),
	)
	return env
}

func envWithoutKeys(env []string, keys ...string) []string {
	blocked := map[string]bool{}
	for _, key := range keys {
		blocked[key] = true
	}
	out := make([]string, 0, len(env))
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if ok && blocked[key] {
			continue
		}
		out = append(out, item)
	}
	return out
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
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
	result, err := s.runDroidspaces(ctx, args...)
	if err == nil {
		s.reconcileNestedAndroidNATCompatAsync()
	}
	return result, err
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

func (s *Server) enrichContainerViews(ctx context.Context, containers []socketd.Container) []containerView {
	views := make([]containerView, 0, len(containers))
	for _, container := range containers {
		view := newContainerView(container)
		s.enrichContainerView(ctx, &view)
		views = append(views, view)
	}
	return views
}

func newContainerView(container socketd.Container) containerView {
	view := containerView{Container: container}
	if view.IPAddress == "" {
		view.IPAddress = container.NATIP
	}
	return view
}

func (s *Server) enrichContainerView(ctx context.Context, view *containerView) {
	if view == nil || view.Name == "" {
		return
	}
	if configPath, ok := s.containerConfigPath(view.Name); ok {
		view.applyContainerConfigState(readContainerConfigValues(configPath))
	}
	view.DistroName = s.containerDistroName(view)
	if view.IPAddress == "" {
		view.IPAddress = view.NATIP
	}
	if view.UseSparseImage || strings.HasSuffix(strings.ToLower(strings.TrimSpace(view.RootFSPath)), ".img") {
		if usage, err := collectContainerDiskUsage(view.RootFSPath); err == nil {
			view.DiskUsage = usage
		}
	}
	if view.Running {
		if usage, err := s.collectContainerUsage(ctx, view.Name); err == nil {
			view.applyUsage(usage)
		}
		if ip, err := s.collectContainerIP(ctx, view.Name); err == nil && ip != "" {
			view.IPAddress = ip
		}
	}
}

// containerDistroName mirrors the Android app's icon lookup source: the
// distribution's PRETTY_NAME (falling back to NAME or ID) from /etc/os-release.
// Directory-backed rootfs paths can be read directly. Sparse images are read
// once from a running container in the background so listing containers stays
// responsive.
func (s *Server) containerDistroName(view *containerView) string {
	if view == nil {
		return ""
	}
	name := strings.TrimSpace(view.Name)
	rootfsPath := strings.TrimSpace(view.RootFSPath)
	if name == "" {
		return distroNameFromRootfs(rootfsPath)
	}

	s.containerDistroMu.Lock()
	if s.containerDistroCache == nil {
		s.containerDistroCache = map[string]containerDistroCacheEntry{}
	}
	entry, found := s.containerDistroCache[name]
	if !found || entry.RootFSPath != rootfsPath {
		entry = containerDistroCacheEntry{RootFSPath: rootfsPath}
	}
	if entry.DistroName != "" {
		s.containerDistroMu.Unlock()
		return entry.DistroName
	}
	checkRootfs := !entry.RootFSChecked
	if checkRootfs {
		entry.RootFSChecked = true
		s.containerDistroCache[name] = entry
	}
	s.containerDistroMu.Unlock()

	if checkRootfs {
		if distroName := distroNameFromRootfs(rootfsPath); distroName != "" {
			s.rememberContainerDistro(name, rootfsPath, distroName)
			return distroName
		}
	}
	if !view.Running || strings.TrimSpace(s.droidspacesPath) == "" {
		return ""
	}

	s.containerDistroMu.Lock()
	entry = s.containerDistroCache[name]
	if entry.RootFSPath != rootfsPath {
		s.containerDistroMu.Unlock()
		return ""
	}
	if entry.DistroName != "" {
		s.containerDistroMu.Unlock()
		return entry.DistroName
	}
	if entry.LookupStarted {
		s.containerDistroMu.Unlock()
		return ""
	}
	entry.LookupStarted = true
	s.containerDistroCache[name] = entry
	s.containerDistroMu.Unlock()

	go s.lookupRunningContainerDistro(name, rootfsPath)
	return ""
}

func (s *Server) lookupRunningContainerDistro(name string, rootfsPath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := s.runDroidspaces(ctx, "--name", name, "run", "/bin/sh", "-lc", "cat /etc/os-release 2>/dev/null || true")
	if err != nil {
		return
	}
	if distroName := distroNameFromOSRelease(result.Output); distroName != "" {
		s.rememberContainerDistro(name, rootfsPath, distroName)
	}
}

func (s *Server) rememberContainerDistro(name string, rootfsPath string, distroName string) {
	name = strings.TrimSpace(name)
	distroName = strings.TrimSpace(distroName)
	if name == "" || distroName == "" {
		return
	}
	s.containerDistroMu.Lock()
	defer s.containerDistroMu.Unlock()
	if s.containerDistroCache == nil {
		s.containerDistroCache = map[string]containerDistroCacheEntry{}
	}
	entry, found := s.containerDistroCache[name]
	if found && entry.RootFSPath != rootfsPath {
		return
	}
	entry.RootFSPath = rootfsPath
	entry.RootFSChecked = true
	entry.DistroName = distroName
	s.containerDistroCache[name] = entry
}

func (s *Server) resetContainerDistroOnStart(name string, action string) {
	if action != "start" && action != "restart" {
		return
	}
	s.containerDistroMu.Lock()
	defer s.containerDistroMu.Unlock()
	delete(s.containerDistroCache, strings.TrimSpace(name))
}

func distroNameFromRootfs(rootfsPath string) string {
	rootfsPath = strings.TrimSpace(rootfsPath)
	if rootfsPath == "" {
		return ""
	}
	info, err := os.Stat(rootfsPath)
	if err != nil || !info.IsDir() {
		return ""
	}
	file, err := os.Open(filepath.Join(rootfsPath, "etc", "os-release"))
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil {
		return ""
	}
	return distroNameFromOSRelease(string(data))
}

func distroNameFromOSRelease(data string) string {
	values := map[string]string{}
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "PRETTY_NAME" && key != "NAME" && key != "ID" {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && ((value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'')) {
			value = value[1 : len(value)-1]
		}
		if value != "" {
			values[key] = value
		}
	}
	return firstNonEmpty(values["PRETTY_NAME"], values["NAME"], values["ID"])
}

func (view *containerView) applyContainerConfigState(values map[string]string) {
	if view == nil || len(values) == 0 {
		return
	}
	if value, ok := values["allow_userns"]; ok {
		view.AllowUserNS = kvBool(value)
	}
	if value, ok := values["run_at_boot"]; ok {
		view.RunAtBoot = kvBool(value)
	}
	if value, err := strconv.Atoi(strings.TrimSpace(values["run_at_boot_priority"])); err == nil && value > 0 {
		view.RunAtBootPriority = value
	}
	if value := strings.TrimSpace(values["rootfs_path"]); value != "" {
		view.RootFSPath = value
	}
	if value, ok := values["use_sparse_image"]; ok {
		view.UseSparseImage = kvBool(value)
	}
}

func (view *containerView) applyUsage(usage containerUsageSnapshot) {
	if usage.CPUUsage != nil {
		value := *usage.CPUUsage
		view.CPUUsage = &value
		view.CPUPercent = &value
	}
	if usage.RAMUsedKB != nil {
		value := *usage.RAMUsedKB
		view.RAMUsedKB = &value
		mb := float64(value) / 1024.0
		view.RAMUsageMB = &mb
	}
	if usage.RAMTotalKB != nil {
		value := *usage.RAMTotalKB
		view.RAMTotalKB = &value
	}
	if usage.RAMPercent != nil {
		value := *usage.RAMPercent
		view.RAMPercent = &value
		view.MemoryPercent = &value
	}
	if usage.Uptime != "" {
		view.Uptime = usage.Uptime
	}
	if usage.MemoryUsageSource != "" {
		view.MemoryUsageSource = usage.MemoryUsageSource
	}
	if usage.MemoryUsage == nil && (view.RAMUsedKB != nil || view.RAMTotalKB != nil || view.RAMPercent != nil) {
		memory := &containerMemoryUsage{}
		if view.RAMUsedKB != nil {
			memory.UsedKB = *view.RAMUsedKB
			memory.UsedBytes = *view.RAMUsedKB * 1024
		}
		if view.RAMTotalKB != nil {
			memory.TotalKB = *view.RAMTotalKB
			memory.TotalBytes = *view.RAMTotalKB * 1024
		}
		if view.RAMPercent != nil {
			value := *view.RAMPercent
			memory.Percent = &value
		}
		view.MemoryUsage = memory
	}
	if usage.MemoryUsage != nil {
		view.MemoryUsage = cloneContainerMemoryUsage(usage.MemoryUsage)
	}
	if usage.CgroupMemoryUsage != nil {
		view.CgroupMemoryUsage = cloneContainerMemoryUsage(usage.CgroupMemoryUsage)
	}
}

func (s *Server) collectContainerUsage(ctx context.Context, name string) (containerUsageSnapshot, error) {
	cgroupMemory, cgroupErr := s.collectContainerCgroupMemoryUsage(name)
	usageCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result, err := s.runDroidspaces(usageCtx, "--name", name, "usage")
	var out containerUsageSnapshot
	if err == nil {
		kv := parseKeyValueOutput(result.Output)
		if raw := kv["CPU_PERMILL"]; raw != "" {
			if permill, convErr := strconv.ParseFloat(raw, 64); convErr == nil {
				value := clampFloat(permill/10.0, 0, 100)
				out.CPUUsage = &value
			}
		}
		// RAM_USED_KB is the PID-namespace VmRSS sum used by the official
		// Android app. Unlike cgroup memory.current, it excludes file cache and
		// most kernel accounting. RAM_TOTAL_KB is host MemTotal and must never
		// be presented as a container limit.
		if raw := kv["RAM_USED_KB"]; raw != "" {
			if value, convErr := strconv.ParseInt(raw, 10, 64); convErr == nil && value >= 0 {
				out.RAMUsedKB = &value
				out.MemoryUsage = memoryUsageFromKB(value)
				out.MemoryUsageSource = "core-rss"
			}
		}
		if raw := kv["UPTIME"]; raw != "" && raw != "NONE" {
			out.Uptime = raw
		}
	}
	if cgroupMemory != nil {
		out.CgroupMemoryUsage = cgroupMemory
		if out.MemoryUsage == nil {
			// Keep a usable value when the core usage command is unavailable, but
			// mark it so clients do not mistake cgroup accounting for process RSS.
			out.MemoryUsage = cgroupMemory
			out.MemoryUsageSource = "cgroup-memory.current"
			usedKB := cgroupMemory.UsedBytes / 1024
			out.RAMUsedKB = &usedKB
			if cgroupMemory.TotalBytes > 0 {
				totalKB := cgroupMemory.TotalBytes / 1024
				out.RAMTotalKB = &totalKB
				if cgroupMemory.Percent != nil {
					percent := *cgroupMemory.Percent
					out.RAMPercent = &percent
				}
			}
		}
	} else if out.RAMUsedKB != nil && out.RAMTotalKB != nil && *out.RAMTotalKB > 0 {
		percent := clampFloat(float64(*out.RAMUsedKB)*100/float64(*out.RAMTotalKB), 0, 100)
		out.RAMPercent = &percent
	}
	if err != nil && cgroupErr != nil {
		return out, err
	}
	return out, nil
}

func memoryUsageFromKB(usedKB int64) *containerMemoryUsage {
	return &containerMemoryUsage{
		UsedKB:    usedKB,
		UsedBytes: usedKB * 1024,
	}
}

func cloneContainerMemoryUsage(source *containerMemoryUsage) *containerMemoryUsage {
	if source == nil {
		return nil
	}
	copy := *source
	if source.Percent != nil {
		value := *source.Percent
		copy.Percent = &value
	}
	if source.AnonBytes != nil {
		value := *source.AnonBytes
		copy.AnonBytes = &value
	}
	if source.FileBytes != nil {
		value := *source.FileBytes
		copy.FileBytes = &value
	}
	if source.KernelBytes != nil {
		value := *source.KernelBytes
		copy.KernelBytes = &value
	}
	return &copy
}

func (s *Server) collectContainerCgroupMemoryUsage(name string) (*containerMemoryUsage, error) {
	root := strings.TrimSpace(s.cgroupRoot)
	if root == "" {
		root = defaultContainerCgroupRoot
	}
	path, ok := safeJoinChild(root, sanitizeContainerName(name))
	if !ok {
		return nil, fmt.Errorf("invalid container cgroup path")
	}
	used, err := readCgroupBytes(filepath.Join(path, "memory.current"))
	if err != nil {
		return nil, err
	}
	memory := &containerMemoryUsage{
		UsedBytes: used,
		UsedKB:    used / 1024,
	}
	if limit, ok := readCgroupLimit(filepath.Join(path, "memory.max")); ok && limit > 0 {
		memory.TotalBytes = limit
		memory.TotalKB = limit / 1024
		percent := clampFloat(float64(used)*100/float64(limit), 0, 100)
		memory.Percent = &percent
	}
	if breakdown, statErr := readCgroupMemoryBreakdown(filepath.Join(path, "memory.stat")); statErr == nil {
		memory.AnonBytes = breakdown.AnonBytes
		memory.FileBytes = breakdown.FileBytes
		memory.KernelBytes = breakdown.KernelBytes
	}
	return memory, nil
}

type cgroupMemoryBreakdown struct {
	AnonBytes   *int64
	FileBytes   *int64
	KernelBytes *int64
}

func readCgroupMemoryBreakdown(path string) (cgroupMemoryBreakdown, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return cgroupMemoryBreakdown{}, err
	}
	values := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		value, parseErr := strconv.ParseInt(fields[1], 10, 64)
		if parseErr != nil || value < 0 {
			continue
		}
		values[fields[0]] = value
	}

	var breakdown cgroupMemoryBreakdown
	if value, ok := values["anon"]; ok {
		breakdown.AnonBytes = int64Pointer(value)
	}
	if value, ok := values["file"]; ok {
		breakdown.FileBytes = int64Pointer(value)
	}
	if value, ok := values["kernel"]; ok {
		breakdown.KernelBytes = int64Pointer(value)
		return breakdown, nil
	}

	// cgroup v2 has no single kernel counter. Combine its non-overlapping
	// kernel-accounted buckets; slab is already an aggregate, so do not also
	// add slab_reclaimable or slab_unreclaimable.
	var kernelBytes int64
	foundKernelBucket := false
	for _, key := range []string{"kernel_stack", "pagetables", "sec_pagetables", "percpu", "sock", "vmalloc", "slab"} {
		if value, ok := values[key]; ok {
			kernelBytes += value
			foundKernelBucket = true
		}
	}
	if foundKernelBucket {
		breakdown.KernelBytes = int64Pointer(kernelBytes)
	}
	return breakdown, nil
}

// int64Pointer preserves a present zero-valued memory.stat field in JSON.
func int64Pointer(value int64) *int64 {
	return &value
}

func readCgroupBytes(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(data))
	if value == "" || value == "max" {
		return 0, fmt.Errorf("cgroup value is unavailable")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid cgroup value")
	}
	return parsed, nil
}

func readCgroupLimit(path string) (int64, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(data))
	if value == "" || value == "max" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 {
		return 0, false
	}
	return parsed, true
}

func collectContainerDiskUsage(rootfsPath string) (*containerDiskUsage, error) {
	rootfsPath = strings.TrimSpace(rootfsPath)
	if rootfsPath == "" {
		return nil, fmt.Errorf("rootfs image path is empty")
	}
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, fmt.Errorf("rootfs image is not a non-empty regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 || stat.Blocks > math.MaxInt64/512 {
		return nil, fmt.Errorf("rootfs image allocation is unavailable")
	}
	used := stat.Blocks * 512
	total := info.Size()
	percent := clampFloat(float64(used)*100/float64(total), 0, 100)
	return &containerDiskUsage{
		UsedBytes:  used,
		TotalBytes: total,
		Percent:    &percent,
	}, nil
}

func (s *Server) collectContainerIP(ctx context.Context, name string) (string, error) {
	ipCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := `ip -4 addr show 2>/dev/null | awk '/inet / && $2 !~ /^127/ {split($2,a,"/"); print a[1]}' | tr '\n' ' ' || true`
	result, err := s.runDroidspaces(ipCtx, "--name", name, "run", "/bin/sh", "-lc", cmd)
	if err != nil {
		return "", err
	}
	return extractIPv4List(result.Output), nil
}

func extractIPv4List(text string) string {
	fields := strings.Fields(text)
	ips := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		field = strings.TrimSpace(strings.Trim(field, ",;"))
		ip := net.ParseIP(field)
		if ip == nil || ip.To4() == nil || strings.HasPrefix(field, "127.") || seen[field] {
			continue
		}
		seen[field] = true
		ips = append(ips, field)
	}
	return strings.Join(ips, ", ")
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
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
	if v := kv["GATEWAY_CONTAINER"]; v != "" {
		base.NetMode = "gateway"
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
	resp := newInspectResponse(base, "cli")
	resp.StaticNATIP = kv["NAT_IP"]
	resp.GatewayContainer = kv["GATEWAY_CONTAINER"]
	resp.GatewayNet = kv["GATEWAY_NET"]
	resp.GatewayBridge = kv["GATEWAY_BRIDGE"]
	resp.GatewayLanIfname = kv["GATEWAY_IFACE"]
	resp.PrivilegedMode = strings.ToLower(kv["PRIVILEGED_MODE"])
	if configPath, ok := s.containerConfigPath(target); ok {
		resp.applyConfigValues(readContainerConfigValues(configPath))
	}
	resp.CLIInfo = kv
	resp.RawOutput = result.Output
	s.enrichContainerView(ctx, &resp.containerView)
	return resp, nil
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
	log.Printf("task queued id=%s kind=%q name=%q", task.ID, task.Kind, task.Name)
	return cloneTask(task)
}

// beginContainerTask prevents conflicting lifecycle operations on one container
// without serializing unrelated containers.
func (s *Server) beginContainerTask(kind string, target string) (*taskState, func(), error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return nil, nil, fmt.Errorf("container name is required")
	}

	s.containerTaskMu.Lock()
	if existing := s.containerTasks[target]; existing != "" {
		s.containerTaskMu.Unlock()
		return nil, nil, fmt.Errorf("another operation is already running for %s (task %s)", target, existing)
	}
	task := s.newTask(kind, target)
	s.containerTasks[target] = task.ID
	s.containerTaskMu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			s.containerTaskMu.Lock()
			if s.containerTasks[target] == task.ID {
				delete(s.containerTasks, target)
			}
			s.containerTaskMu.Unlock()
		})
	}
	return task, release, nil
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

func (s *Server) listTasks() []*taskState {
	s.tasksMu.RLock()
	defer s.tasksMu.RUnlock()
	tasks := make([]*taskState, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, cloneTask(task))
	}
	sort.Slice(tasks, func(i, j int) bool {
		if tasks[i].UpdatedAt == tasks[j].UpdatedAt {
			return tasks[i].StartedAt > tasks[j].StartedAt
		}
		return tasks[i].UpdatedAt > tasks[j].UpdatedAt
	})
	return tasks
}

func (s *Server) taskSummary() taskSummary {
	summary := taskSummary{ByKind: map[string]int{}}
	s.tasksMu.RLock()
	defer s.tasksMu.RUnlock()
	for _, task := range s.tasks {
		summary.Total++
		kind := strings.TrimSpace(task.Kind)
		if kind == "" {
			kind = "other"
		}
		summary.ByKind[kind]++
		switch strings.ToLower(strings.TrimSpace(task.Status)) {
		case "pending":
			summary.Pending++
			summary.Active++
		case "running":
			summary.Running++
			summary.Active++
		case "done":
			summary.Done++
		case "cancelled", "canceled":
			summary.Cancelled++
		default:
			summary.Error++
		}
	}
	return summary
}

func (s *Server) updateTask(id string, fn func(*taskState)) {
	s.tasksMu.Lock()
	defer s.tasksMu.Unlock()
	if task, ok := s.tasks[id]; ok {
		fn(task)
		task.UpdatedAt = time.Now().Unix()
	}
}

func (s *Server) appendTaskLog(id string, line string) {
	line = ansiPattern.ReplaceAllString(strings.TrimRight(line, "\r\n"), "")
	s.updateTask(id, func(task *taskState) {
		task.Log = append(task.Log, line)
		if len(task.Log) > 400 {
			task.Log = task.Log[len(task.Log)-400:]
		}
		task.Output = strings.Join(task.Log, "\n")
	})
}

func (s *Server) failTask(id string, err error) {
	if err != nil {
		log.Printf("task failed id=%s error=%q", id, err.Error())
	}
	s.updateTask(id, func(task *taskState) {
		task.Status = "error"
		task.Error = err.Error()
	})
}

func (s *Server) completeTask(id string, path string, url string) {
	log.Printf("task completed id=%s path=%q", id, path)
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
	if task.Log != nil {
		copy.Log = append([]string(nil), task.Log...)
	}
	return &copy
}

func (s *Server) cpuUsagePercent() float64 {
	sample, err := readCPUSample()
	if err != nil || sample.Total == 0 {
		return 0
	}
	s.hostStatsMu.Lock()
	defer s.hostStatsMu.Unlock()
	previous := s.lastCPUSample
	s.lastCPUSample = sample
	if previous.Total == 0 || sample.Total <= previous.Total || sample.Idle < previous.Idle {
		return 0
	}
	totalDelta := sample.Total - previous.Total
	idleDelta := sample.Idle - previous.Idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return float64(totalDelta-idleDelta) * 100 / float64(totalDelta)
}

func readCPUSample() (cpuSample, error) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuSample{}, err
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("invalid /proc/stat cpu line")
	}
	var values []uint64
	for _, raw := range fields[1:] {
		value, convErr := strconv.ParseUint(raw, 10, 64)
		if convErr != nil {
			return cpuSample{}, convErr
		}
		values = append(values, value)
	}
	var total uint64
	for _, value := range values {
		total += value
	}
	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}
	return cpuSample{Idle: idle, Total: total}, nil
}

func readMemoryReport() memoryReport {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return memoryReport{}
	}
	values := map[string]uint64{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, convErr := strconv.ParseUint(fields[1], 10, 64)
		if convErr != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	total := values["MemTotal"]
	available := values["MemAvailable"]
	if available == 0 {
		available = values["MemFree"] + values["Buffers"] + values["Cached"]
	}
	used := uint64(0)
	if total > available {
		used = total - available
	}
	percent := 0.0
	if total > 0 {
		percent = float64(used) * 100 / float64(total)
	}
	return memoryReport{Used: used, Total: total, Percent: percent}
}

func readNetworkReport() networkReport {
	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return networkReport{}
	}
	var rx uint64
	var tx uint64
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		name, rest, _ := strings.Cut(line, ":")
		if strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 16 {
			continue
		}
		if value, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
			rx += value
		}
		if value, err := strconv.ParseUint(fields[8], 10, 64); err == nil {
			tx += value
		}
	}
	return networkReport{RxBytes: rx, TxBytes: tx, IO: fmt.Sprintf("↓ %s / ↑ %s", formatBytes(rx), formatBytes(tx))}
}

func readBatteryReport() batteryReport {
	base := filepath.Join(powerSupplyRoot, "battery")
	status := strings.TrimSpace(readFirstFile(filepath.Join(base, "status")))
	capacity, hasCapacity := readFloatFile(filepath.Join(base, "capacity"))
	roots := batteryPowerSupplyRoots()
	currentMA, hasCurrent, currentSource := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"current_now", 0.001},
		{"current_avg", 0.001},
		{"batt_current_ua", 0.001},
		{"batt_current", 1},
		{"BatteryAverageCurrent", 1},
		{"BatteryCurrent", 1},
		{"current_now_ma", 1},
	}), true)
	voltageV, hasVoltage, voltageSource := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"voltage_now", 0.000001},
		{"voltage_avg", 0.000001},
		{"voltage_ocv", 0.000001},
		{"batt_volt_uv", 0.000001},
		{"batt_vol", 0.001},
		{"BatterySenseVoltage", 0.001},
		{"voltage_now_mv", 0.001},
	}), true)
	directPowerW, hasDirectPower, directPowerSource := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"power_now", 0.000001},
		{"power_avg", 0.000001},
		{"batt_power_uw", 0.000001},
		{"batt_power", 0.001},
		{"power_now_mw", 0.001},
	}), true)
	chargeMah, hasCharge, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"charge_now", 0.001},
		{"charge_counter", 0.001},
		{"charge_now_mah", 1},
		{"batt_charge_now", 0.001},
		{"remaining_capacity", 1},
	}), true)
	fullChargeMah, hasFullCharge, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"charge_full", 0.001},
		{"charge_full_estimate", 0.001},
		{"charge_full_mah", 1},
		{"batt_full_capacity", 1},
		{"fg_fullcapnom", 1},
		{"fg_fullcaprep", 1},
	}), true)
	designChargeMah, hasDesignCharge, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"charge_full_design", 0.001},
		{"charge_design", 0.001},
		{"charge_full_design_mah", 1},
		{"battery_design_capacity", 1},
	}), true)
	energyWh, hasEnergy, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"energy_now", 0.000001},
		{"energy_counter", 0.000001},
		{"energy_now_wh", 1},
	}), true)
	fullEnergyWh, hasFullEnergy, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"energy_full", 0.000001},
		{"energy_full_wh", 1},
	}), true)
	designEnergyWh, hasDesignEnergy, _ := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
		{"energy_full_design", 0.000001},
		{"energy_design", 0.000001},
		{"energy_full_design_wh", 1},
	}), true)
	healthPercent, hasHealth := batteryHealthPercent(fullChargeMah, hasFullCharge, designChargeMah, hasDesignCharge, fullEnergyWh, hasFullEnergy, designEnergyWh, hasDesignEnergy)
	inputRoots := inputPowerSupplyRoots()
	inputOnline := inputPowerSupplyOnline(inputRoots)
	inputTelemetry := readInputPowerTelemetry(inputRoots)
	if !inputTelemetry.hasPower {
		directInputPowerW, hasDirectInputPower, directInputPowerSource := readBestBatteryValue(batteryValueCandidates(roots, []batteryValueName{
			{"input_power_now", 0.000001},
			{"input_power_avg", 0.000001},
			{"input_power_now_mw", 0.001},
			{"input_power", 0.000001},
		}), true)
		if hasDirectInputPower {
			inputTelemetry = inputPowerTelemetry{
				powerW:   directInputPowerW,
				hasPower: true,
				kind:     "measured",
				source:   directInputPowerSource,
			}
		}
	}
	tempDeciC, hasTemp := readFloatFile(filepath.Join(base, "temp"))
	inputCurrentMA := inputTelemetry.currentMA
	hasInputCurrent := inputTelemetry.hasCurrent
	inputVoltageV := inputTelemetry.voltageV
	hasInputVoltage := inputTelemetry.hasVoltage
	inputPowerW := inputTelemetry.powerW
	hasInputPower := inputTelemetry.hasPower
	inputSource := inputTelemetry.source
	if !hasCapacity && !hasCurrent && !hasVoltage && !hasDirectPower && !hasInputPower && !hasTemp && !hasCharge && !hasEnergy && status == "" {
		return batteryReport{}
	}
	powerW := 0.0
	hasPower := false
	powerSource := ""
	if hasCurrent && hasVoltage {
		powerW = currentMA * voltageV / 1000
		hasPower = true
		powerSource = "computed:" + currentSource + "+" + voltageSource
	} else if hasDirectPower {
		powerW = directPowerW
		hasPower = true
		powerSource = directPowerSource
	}
	absCurrentMA := currentMA
	if absCurrentMA < 0 {
		absCurrentMA = -absCurrentMA
	}
	absPowerW := powerW
	if absPowerW < 0 {
		absPowerW = -absPowerW
	}
	if status == "" {
		if hasCurrent && currentMA < 0 {
			status = "Discharging"
		} else if hasCurrent && currentMA > 0 {
			status = "Charging"
		} else {
			status = "Unknown"
		}
	}
	summaryPower := "power n/a"
	if hasPower {
		summaryPower = fmt.Sprintf("%.3f W", absPowerW)
	} else if hasInputPower {
		summaryPower = fmt.Sprintf("input %.3f W", inputPowerW)
	}
	report := batteryReport{
		Available:       true,
		Status:          status,
		CapacityPercent: capacity,
		CurrentMA:       currentMA,
		AbsCurrentMA:    absCurrentMA,
		VoltageV:        voltageV,
		PowerW:          powerW,
		AbsPowerW:       absPowerW,
		ChargeMah:       chargeMah,
		FullChargeMah:   fullChargeMah,
		DesignChargeMah: designChargeMah,
		EnergyWh:        energyWh,
		FullEnergyWh:    fullEnergyWh,
		DesignEnergyWh:  designEnergyWh,
		HealthPercent:   healthPercent,
		InputCurrentMA:  inputCurrentMA,
		InputVoltageV:   inputVoltageV,
		InputPowerW:     inputPowerW,
		InputPowerKind:  inputTelemetry.kind,
		InputOnline:     inputOnline,
		TemperatureC:    tempDeciC / 10,
		HasCapacity:     hasCapacity,
		HasCurrent:      hasCurrent,
		HasVoltage:      hasVoltage,
		HasPower:        hasPower,
		HasCharge:       hasCharge,
		HasFullCharge:   hasFullCharge,
		HasDesignCharge: hasDesignCharge,
		HasEnergy:       hasEnergy,
		HasFullEnergy:   hasFullEnergy,
		HasDesignEnergy: hasDesignEnergy,
		HasHealth:       hasHealth,
		HasInputCurrent: hasInputCurrent,
		HasInputVoltage: hasInputVoltage,
		HasInputPower:   hasInputPower,
		HasTemperature:  hasTemp,
		CurrentSource:   currentSource,
		VoltageSource:   voltageSource,
		PowerSource:     powerSource,
		InputSource:     inputSource,
		Summary:         fmt.Sprintf("%.0f%% %s / %s", capacity, status, summaryPower),
	}
	return normalizeBatteryReport(report, false)
}

// normalizeBatteryReport turns vendor-specific battery readings into an
// explicit power-path state. It deliberately keeps the raw values intact:
// battery drivers do not agree on current polarity, so Status has precedence
// when it is available and raw polarity is the fallback for unknown status.
func normalizeBatteryReport(report batteryReport, directPowerSupported bool) batteryReport {
	if !report.Available {
		return report
	}

	report.PowerMode = "unknown"
	report.BatteryDirection = "unknown"
	report.PowerModeSource = ""
	report.ExternalPowerActive = batteryReportHasExternalPower(report)
	report.DirectPowerActive = false
	report.SignedCurrentMA = 0
	report.SignedPowerW = 0
	report.HasSignedCurrent = false
	report.HasSignedPower = false
	report.ChargingPowerW = 0
	report.BoardPowerEstimateW = 0
	report.HasChargingPower = false
	report.HasBoardPowerEstimate = false

	if currentMA, ok := batteryReportSignedCurrentMA(report); ok {
		report.SignedCurrentMA = currentMA
		report.HasSignedCurrent = true
	}
	if powerW, ok := batteryReportSignedPowerW(report); ok {
		report.SignedPowerW = powerW
		report.HasSignedPower = true
	}

	statusDirection := batteryStatusDirection(report.Status)
	batteryActive := report.HasSignedCurrent || report.HasSignedPower
	if directPowerSupported && report.ExternalPowerActive && !batteryActive && statusDirection >= 0 {
		report.PowerMode = "direct"
		report.BatteryDirection = "idle"
		report.PowerModeSource = "configured-direct-power"
		report.DirectPowerActive = true
		report.Summary = batteryReportSummary(report)
		return report
	}

	switch {
	case statusDirection < 0:
		report.BatteryDirection = "discharging"
		report.PowerMode = "discharging"
		report.PowerModeSource = "status"
	case statusDirection > 0:
		report.BatteryDirection = "charging"
		report.PowerMode = "charging"
		report.PowerModeSource = "status"
	case report.HasSignedPower && report.SignedPowerW < -batteryStatsMinPowerW:
		report.BatteryDirection = "discharging"
		report.PowerMode = "discharging"
		report.PowerModeSource = "battery-power"
	case report.HasSignedCurrent && report.SignedCurrentMA < -batteryStatsMinCurrentMA:
		report.BatteryDirection = "discharging"
		report.PowerMode = "discharging"
		report.PowerModeSource = "battery-current"
	case report.HasSignedPower && report.SignedPowerW > batteryStatsMinPowerW:
		report.BatteryDirection = "charging"
		report.PowerMode = "charging"
		report.PowerModeSource = "battery-power"
	case report.HasSignedCurrent && report.SignedCurrentMA > batteryStatsMinCurrentMA:
		report.BatteryDirection = "charging"
		report.PowerMode = "charging"
		report.PowerModeSource = "battery-current"
	case report.ExternalPowerActive:
		report.BatteryDirection = "idle"
		report.PowerMode = "external"
		report.PowerModeSource = "input"
	case batteryStatusIdle(report.Status):
		report.BatteryDirection = "idle"
		report.PowerMode = "idle"
		report.PowerModeSource = "status"
	}
	populateBatteryPowerSplit(&report)
	report.Summary = batteryReportSummary(report)
	return report
}

func (s *Server) normalizeBatteryReport(report batteryReport) batteryReport {
	report = normalizeBatteryReport(report, s.batteryDirectPower)
	report.Enabled = true
	return report
}

func (s *Server) overviewPowerEnabledSetting() bool {
	s.batteryFeatureMu.RLock()
	defer s.batteryFeatureMu.RUnlock()
	return s.overviewPowerEnabled
}

func (s *Server) batteryMonitoringEnabledSetting() bool {
	s.batteryFeatureMu.RLock()
	defer s.batteryFeatureMu.RUnlock()
	return s.batteryMonitoringEnabled
}

// setBatteryFeatureSettings updates both independently selectable battery
// features and returns the previous monitoring state for sampler lifecycle.
func (s *Server) setBatteryFeatureSettings(overviewPowerEnabled bool, batteryMonitoringEnabled bool) bool {
	s.batteryFeatureMu.Lock()
	previousMonitoring := s.batteryMonitoringEnabled
	s.overviewPowerEnabled = overviewPowerEnabled
	s.batteryMonitoringEnabled = batteryMonitoringEnabled
	s.batteryFeatureMu.Unlock()
	return previousMonitoring
}

func (s *Server) disabledBatteryReport() batteryReport {
	interval := s.batteryStatsSampleInterval()
	writeInterval := s.batteryStatsWriteInterval()
	return batteryReport{
		Enabled:   false,
		Available: false,
		Status:    "disabled",
		Summary:   "电池监控已关闭",
		Stats: batteryStatsReport{
			MinSampleIntervalSeconds: int(interval / time.Second),
			SamplerIntervalSeconds:   int(interval / time.Second),
			WriteIntervalSeconds:     int(writeInterval / time.Second),
			DatabaseMode:             "disabled",
			Message:                  "电池监控已关闭",
		},
	}
}

func (s *Server) disabledBatteryPowerRangeReport(hours int, now time.Time) batteryPowerRangeReport {
	if hours <= 0 {
		hours = 24
	}
	return batteryPowerRangeReport{
		Enabled:     false,
		From:        now.Add(-time.Duration(hours) * time.Hour).Unix(),
		To:          now.Unix(),
		Hours:       hours,
		BatteryBins: newBatteryPowerBins(),
		InputBins:   newBatteryPowerBins(),
		Message:     "电池监控已关闭",
	}
}

func batteryReportHasExternalPower(report batteryReport) bool {
	if report.InputOnline {
		return true
	}
	if report.HasInputPower && report.InputPowerW > batteryStatsMinPowerW {
		return true
	}
	return report.HasInputCurrent && math.Abs(report.InputCurrentMA) >= batteryStatsMinCurrentMA
}

// populateBatteryPowerSplit derives the device-side consumption only when
// both a measured external input reading and a positive battery charge reading
// are available. The remainder includes power-conversion losses, so callers
// must present it as an estimate rather than a measured motherboard rail.
func populateBatteryPowerSplit(report *batteryReport) {
	if report.PowerMode != "charging" || !report.HasSignedPower || report.SignedPowerW <= batteryStatsMinPowerW {
		return
	}

	report.ChargingPowerW = report.SignedPowerW
	report.HasChargingPower = true
	if !report.HasInputPower || !strings.EqualFold(report.InputPowerKind, "measured") || report.InputPowerW <= batteryStatsMinPowerW {
		return
	}

	boardPowerW := report.InputPowerW - report.ChargingPowerW
	if boardPowerW < -batteryBoardPowerToleranceW {
		return
	}
	if boardPowerW < 0 {
		boardPowerW = 0
	}
	report.BoardPowerEstimateW = boardPowerW
	report.HasBoardPowerEstimate = true
}

func batteryReportSignedPowerW(report batteryReport) (float64, bool) {
	return batterySampleSignedPowerW(batteryStatsSample{
		Status:     report.Status,
		CurrentMA:  report.CurrentMA,
		HasCurrent: report.HasCurrent,
		VoltageV:   report.VoltageV,
		HasVoltage: report.HasVoltage,
		PowerW:     report.PowerW,
		HasPower:   report.HasPower,
	})
}

func batteryReportSignedCurrentMA(report batteryReport) (float64, bool) {
	return batterySampleSignedCurrentMA(batteryStatsSample{
		Status:     report.Status,
		CurrentMA:  report.CurrentMA,
		HasCurrent: report.HasCurrent,
	})
}

func batteryStatusIdle(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return strings.Contains(status, "not charging") || strings.Contains(status, "full") || strings.Contains(status, "idle")
}

func batteryReportSummary(report batteryReport) string {
	capacity := "--"
	if report.HasCapacity {
		capacity = fmt.Sprintf("%.0f%%", report.CapacityPercent)
	}
	power := "power n/a"
	if report.DirectPowerActive && report.HasInputPower {
		power = fmt.Sprintf("input %.3f W", report.InputPowerW)
	} else if report.HasSignedPower {
		power = fmt.Sprintf("battery %.3f W", math.Abs(report.SignedPowerW))
	} else if report.HasInputPower {
		power = fmt.Sprintf("input %.3f W", report.InputPowerW)
	}
	return fmt.Sprintf("%s %s / %s", capacity, report.PowerMode, power)
}

func (s *Server) updateBatteryStats(report batteryReport, now time.Time) batteryStatsReport {
	if !s.batteryMonitoringEnabledSetting() {
		return s.disabledBatteryReport().Stats
	}
	report = s.normalizeBatteryReport(report)
	interval := s.batteryStatsSampleInterval()
	writeInterval := s.batteryStatsWriteInterval()
	if !report.Available {
		return batteryStatsReport{
			MinSampleIntervalSeconds: int(interval / time.Second),
			SamplerIntervalSeconds:   int(interval / time.Second),
			WriteIntervalSeconds:     int(writeInterval / time.Second),
			DatabaseMode:             "daily-jsonl",
			Message:                  "设备未提供电池数据",
		}
	}
	sample := batteryStatsSampleFromReport(report, now)
	if !batteryStatsSampleUseful(sample) {
		return batteryStatsReport{
			MinSampleIntervalSeconds: int(interval / time.Second),
			SamplerIntervalSeconds:   int(interval / time.Second),
			WriteIntervalSeconds:     int(writeInterval / time.Second),
			DatabaseMode:             "daily-jsonl",
			Message:                  "需要更多有效电池数据",
		}
	}

	s.batteryStatsMu.Lock()
	defer s.batteryStatsMu.Unlock()
	// The setting can change while a sampler is reading battery data. Check it
	// again after taking the stats lock so disabling monitoring cannot admit one
	// final sample from an in-flight collection.
	if !s.batteryMonitoringEnabledSetting() {
		return s.disabledBatteryReport().Stats
	}

	storageRoot := s.batteryStatsStorageRoot()
	if s.batteryStats.path != storageRoot || !s.batteryStats.loaded || s.batteryStatsStorageChangedLocked(storageRoot) {
		s.loadBatteryStatsLocked(storageRoot, now)
	}

	if s.shouldRecordBatteryStatsSampleLocked(sample, now) {
		if s.batteryStats.hasLastSample {
			s.batteryStats.addBatteryStatsTransition(s.batteryStats.lastSample, sample)
		} else {
			s.batteryStats.seedTrackedRemaining(sample)
		}
		s.batteryStats.lastSample = sample
		s.batteryStats.hasLastSample = true
		s.batteryStats.sampleCount++
		if s.batteryStats.pendingSince <= 0 {
			s.batteryStats.pendingSince = sample.Time
		}
		s.batteryStats.pendingSamples = append(s.batteryStats.pendingSamples, sample)
		s.flushBatteryStatsIfDueLocked(storageRoot, now)
	}

	return s.batteryStats.report(sample, interval, writeInterval)
}

func (s *Server) batteryStatsStorageRoot() string {
	if s.workspace == "" {
		return ""
	}
	return filepath.Join(s.workspace, batteryStatsDirectoryName)
}

func (s *Server) batteryStatsSampleSeconds() int {
	seconds := int(atomic.LoadInt64(&s.batteryStatsSampleSecs))
	if seconds <= 0 {
		seconds = batteryStatsDefaultSampleSeconds
	}
	if seconds < batteryStatsMinSampleSeconds {
		return batteryStatsMinSampleSeconds
	}
	if seconds > batteryStatsMaxSampleSeconds {
		return batteryStatsMaxSampleSeconds
	}
	return seconds
}

func (s *Server) batteryStatsSampleInterval() time.Duration {
	return time.Duration(s.batteryStatsSampleSeconds()) * time.Second
}

func (s *Server) batteryStatsWriteMinutes() int {
	minutes := int(atomic.LoadInt64(&s.batteryStatsWriteMins))
	if minutes <= 0 {
		minutes = batteryStatsDefaultWriteMinutes
	}
	if minutes < batteryStatsMinWriteMinutes {
		return batteryStatsMinWriteMinutes
	}
	if minutes > batteryStatsMaxWriteMinutes {
		return batteryStatsMaxWriteMinutes
	}
	return minutes
}

func (s *Server) batteryStatsWriteInterval() time.Duration {
	return time.Duration(s.batteryStatsWriteMinutes()) * time.Minute
}

func (s *Server) batteryStatsRetentionDaysSetting() int {
	days := int(atomic.LoadInt64(&s.batteryStatsRetentionDays))
	if days <= 0 {
		days = batteryStatsDefaultRetentionDays
	}
	if days < batteryStatsMinRetentionDays {
		return batteryStatsMinRetentionDays
	}
	if days > batteryStatsMaxRetentionDays {
		return batteryStatsMaxRetentionDays
	}
	return days
}

func (s *Server) flushBatteryStatsIfDueLocked(path string, now time.Time) {
	if path == "" || len(s.batteryStats.pendingSamples) == 0 {
		return
	}
	lastFlush := s.batteryStats.lastFlushTime
	if lastFlush <= 0 {
		lastFlush = s.batteryStats.pendingSince
	}
	if lastFlush > 0 && now.Sub(time.Unix(lastFlush, 0)) < s.batteryStatsWriteInterval() {
		return
	}
	s.flushBatteryStatsLocked(path, now)
}

func (s *Server) flushBatteryStatsLocked(path string, now time.Time) {
	if path == "" || len(s.batteryStats.pendingSamples) == 0 {
		return
	}
	groups := make(map[string][]batteryStatsSample)
	for _, sample := range s.batteryStats.pendingSamples {
		groups[batteryStatsDailyFilePath(path, sample.Time)] = append(groups[batteryStatsDailyFilePath(path, sample.Time)], sample)
	}
	paths := make([]string, 0, len(groups))
	for dailyPath := range groups {
		paths = append(paths, dailyPath)
	}
	sort.Strings(paths)

	remaining := make([]batteryStatsSample, 0)
	for index, dailyPath := range paths {
		if err := appendBatteryStatsSamples(dailyPath, groups[dailyPath]); err != nil {
			s.batteryStats.storageError = err.Error()
			for _, unflushedPath := range paths[index:] {
				remaining = append(remaining, groups[unflushedPath]...)
			}
			s.batteryStats.pendingSamples = remaining
			if len(remaining) > 0 {
				s.batteryStats.pendingSince = remaining[0].Time
			}
			return
		}
	}
	if err := s.pruneBatteryStatsFiles(path, now); err != nil {
		s.batteryStats.storageError = err.Error()
		return
	}
	s.batteryStats.storageError = ""
	s.batteryStats.pendingSamples = nil
	s.batteryStats.pendingSince = 0
	s.batteryStats.lastFlushTime = s.batteryStats.lastSample.Time
	s.batteryStats.storageSignature = batteryStatsStorageSignature(path)
}

func (s *Server) batteryPowerRangeReport(hours int, now time.Time) (batteryPowerRangeReport, error) {
	if hours <= 0 {
		hours = 24
	}
	from := now.Add(-time.Duration(hours) * time.Hour).Unix()
	to := now.Unix()
	samples, err := s.batteryPowerSamplesSince(from, now)
	if err != nil {
		return batteryPowerRangeReport{}, err
	}
	report := batteryPowerRangeReport{
		Enabled:     true,
		From:        from,
		To:          to,
		Hours:       hours,
		BatteryBins: newBatteryPowerBins(),
		InputBins:   newBatteryPowerBins(),
	}
	for _, sample := range samples {
		if sample.Time < from || sample.Time > to {
			continue
		}
		report.SampleCount++
		out := batteryPowerRangeSample{
			Time:             sample.Time,
			Status:           sample.Status,
			PowerMode:        sample.PowerMode,
			BatteryDirection: sample.BatteryDirection,
			Capacity:         sample.CapacityPercent,
			HasCapacity:      sample.HasCapacity,
		}
		if signed, ok := batterySampleSignedPowerW(sample); ok {
			out.BatteryW = signed
			out.HasBattery = true
			report.BatterySampleCount++
			if signed < -batteryStatsMinPowerW {
				discharge := -signed
				report.DischargeSampleCount++
				report.AvgDischargeW += discharge
				if discharge > report.MaxDischargeW {
					report.MaxDischargeW = discharge
				}
				addBatteryPowerBin(report.BatteryBins, discharge)
			} else if signed > batteryStatsMinPowerW {
				report.ChargeSampleCount++
				report.AvgChargeW += signed
				if signed > report.MaxChargeW {
					report.MaxChargeW = signed
				}
			}
		}
		if inputW, ok := batterySampleInputPowerW(sample); ok {
			out.InputW = inputW
			out.HasInput = true
			report.InputSampleCount++
			report.AvgInputW += inputW
			if inputW > report.MaxInputW {
				report.MaxInputW = inputW
			}
			addBatteryPowerBin(report.InputBins, inputW)
		}
		report.RecentSamples = append(report.RecentSamples, out)
	}
	if report.DischargeSampleCount > 0 {
		report.AvgDischargeW /= float64(report.DischargeSampleCount)
	}
	if report.ChargeSampleCount > 0 {
		report.AvgChargeW /= float64(report.ChargeSampleCount)
	}
	if report.InputSampleCount > 0 {
		report.AvgInputW /= float64(report.InputSampleCount)
	}
	finalizeBatteryPowerBins(report.BatteryBins, report.DischargeSampleCount)
	finalizeBatteryPowerBins(report.InputBins, report.InputSampleCount)
	report.ChartSamples = downsampleBatteryPowerRangeSamples(report.RecentSamples, 240)
	if len(report.RecentSamples) > 120 {
		report.RecentSamples = report.RecentSamples[len(report.RecentSamples)-120:]
	}
	if report.SampleCount == 0 {
		report.Message = "查询区间内暂无样本"
	}
	return report, nil
}

func downsampleBatteryPowerRangeSamples(samples []batteryPowerRangeSample, limit int) []batteryPowerRangeSample {
	if limit <= 0 {
		return nil
	}
	if len(samples) <= limit {
		return append([]batteryPowerRangeSample(nil), samples...)
	}
	if limit == 1 {
		return []batteryPowerRangeSample{samples[len(samples)-1]}
	}
	out := make([]batteryPowerRangeSample, 0, limit)
	lastIdx := -1
	for i := 0; i < limit; i++ {
		idx := int(math.Round(float64(i*(len(samples)-1)) / float64(limit-1)))
		if idx <= lastIdx {
			idx = lastIdx + 1
		}
		if idx >= len(samples) {
			idx = len(samples) - 1
		}
		out = append(out, samples[idx])
		lastIdx = idx
	}
	return out
}

func (s *Server) batteryPowerSamplesSince(from int64, now time.Time) ([]batteryStatsSample, error) {
	storageRoot := s.batteryStatsStorageRoot()
	s.batteryStatsMu.Lock()
	if s.batteryStats.path != storageRoot || !s.batteryStats.loaded || s.batteryStatsStorageChangedLocked(storageRoot) {
		s.loadBatteryStatsLocked(storageRoot, now)
	} else if err := s.pruneBatteryStatsFiles(storageRoot, now); err != nil {
		s.batteryStatsMu.Unlock()
		return nil, err
	}
	pending := append([]batteryStatsSample(nil), s.batteryStats.pendingSamples...)
	s.batteryStatsMu.Unlock()
	samples, err := readBatteryStatsDailySamples(storageRoot, from, batteryStatsRetentionCutoff(now, s.batteryStatsRetentionDaysSetting()))
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, sample := range samples {
		seen[sample.Time] = true
	}
	for _, sample := range pending {
		if sample.Time >= from && batteryStatsSampleUseful(sample) && !seen[sample.Time] {
			samples = append(samples, sample)
		}
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].Time < samples[j].Time })
	return samples, nil
}

func newBatteryPowerBins() []batteryPowerRangeBin {
	return []batteryPowerRangeBin{
		{Label: "0-1 W", MinW: 0, MaxW: 1},
		{Label: "1-2 W", MinW: 1, MaxW: 2},
		{Label: "2-4 W", MinW: 2, MaxW: 4},
		{Label: "4-8 W", MinW: 4, MaxW: 8},
		{Label: "8-15 W", MinW: 8, MaxW: 15},
		{Label: "15 W+", MinW: 15, MaxW: 0},
	}
}

func addBatteryPowerBin(bins []batteryPowerRangeBin, watts float64) {
	for i := range bins {
		if watts >= bins[i].MinW && (bins[i].MaxW <= 0 || watts < bins[i].MaxW) {
			bins[i].Count++
			return
		}
	}
}

func finalizeBatteryPowerBins(bins []batteryPowerRangeBin, total int) {
	if total <= 0 {
		return
	}
	for i := range bins {
		bins[i].Percent = float64(bins[i].Count) * 100 / float64(total)
	}
}

type batteryStatsDailyFile struct {
	path string
	day  time.Time
}

// loadBatteryStatsLocked rebuilds summaries strictly from the retained daily
// data files. The former checkpoint is deliberately not read: it was a cache
// rather than source data, so it could resurrect samples after a user deleted
// the old JSONL file.
func (s *Server) loadBatteryStatsLocked(storageRoot string, now time.Time) {
	state := batteryStatsState{path: storageRoot, loaded: true}
	if storageRoot == "" {
		s.batteryStats = state
		return
	}
	if err := s.migrateLegacyBatteryStatsLocked(storageRoot, now); err != nil {
		state.storageError = err.Error()
	}
	if err := s.pruneBatteryStatsFiles(storageRoot, now); err != nil && state.storageError == "" {
		state.storageError = err.Error()
	}
	files, err := listBatteryStatsDailyFiles(storageRoot)
	if err != nil {
		if state.storageError == "" {
			state.storageError = err.Error()
		}
		s.batteryStats = state
		return
	}
	cutoff := batteryStatsRetentionCutoff(now, s.batteryStatsRetentionDaysSetting())
	for _, dailyFile := range files {
		if dailyFile.day.Before(cutoff) {
			continue
		}
		samples, err := readBatteryStatsSamples(dailyFile.path)
		if err != nil {
			if state.storageError == "" {
				state.storageError = err.Error()
			}
			continue
		}
		for _, sample := range samples {
			if !batteryStatsSampleUseful(sample) || batteryStatsDayKey(sample.Time) != dailyFile.day.Format("2006-01-02") {
				continue
			}
			state.recordBatteryStatsSample(sample)
		}
	}
	if state.hasLastSample && !state.hasTrackedRemaining {
		state.seedTrackedRemaining(state.lastSample)
	}
	state.storageSignature = batteryStatsStorageSignature(storageRoot)
	s.batteryStats = state
}

func (s *Server) batteryStatsStorageChangedLocked(storageRoot string) bool {
	return s.batteryStats.storageSignature != batteryStatsStorageSignature(storageRoot)
}

func (state *batteryStatsState) recordBatteryStatsSample(sample batteryStatsSample) {
	if state.hasLastSample {
		// Daily files are append-only. Ignore duplicate or clock-reversed rows so
		// a partial/manual file edit cannot inflate cumulative totals.
		if sample.Time <= state.lastSample.Time {
			return
		}
		state.addBatteryStatsTransition(state.lastSample, sample)
	} else {
		state.seedTrackedRemaining(sample)
	}
	state.lastSample = sample
	state.hasLastSample = true
	state.sampleCount++
	state.lastFlushTime = sample.Time
}

func (s *Server) migrateLegacyBatteryStatsLocked(storageRoot string, now time.Time) error {
	marker := s.batteryStatsMigrationMarkerPath()
	if marker == "" {
		return nil
	}
	if _, err := os.Stat(marker); err == nil {
		return s.retireLegacyBatteryStatsFiles()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	entries, err := os.ReadDir(storageRoot)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(entries) > 0 {
		if err := writeBatteryStatsMigrationMarker(marker); err != nil {
			return err
		}
		return s.retireLegacyBatteryStatsFiles()
	}

	legacyPath := filepath.Join(s.workspace, batteryStatsFileName)
	legacy, err := os.Open(legacyPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeBatteryStatsMigrationMarker(marker); err != nil {
			return err
		}
		return s.retireLegacyBatteryStatsFiles()
	}
	if err != nil {
		return err
	}
	defer legacy.Close()

	if err := os.MkdirAll(s.workspace, 0755); err != nil {
		return err
	}
	stagingRoot, err := os.MkdirTemp(s.workspace, ".battery-stats-migration-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)

	files := map[string]*os.File{}
	encoders := map[string]*json.Encoder{}
	closeWriters := func() error {
		var firstErr error
		for _, file := range files {
			if err := file.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}

	cutoff := batteryStatsRetentionCutoff(now, s.batteryStatsRetentionDaysSetting())
	scanner := bufio.NewScanner(legacy)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var sample batteryStatsSample
		if err := json.Unmarshal(scanner.Bytes(), &sample); err != nil || !batteryStatsSampleUseful(sample) {
			continue
		}
		if batteryStatsDayForUnix(sample.Time).Before(cutoff) {
			continue
		}
		name := batteryStatsDailyFileName(sample.Time)
		if encoders[name] == nil {
			file, err := os.OpenFile(filepath.Join(stagingRoot, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				_ = closeWriters()
				return err
			}
			files[name] = file
			encoders[name] = json.NewEncoder(file)
		}
		if err := encoders[name].Encode(sample); err != nil {
			_ = closeWriters()
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		_ = closeWriters()
		return err
	}
	if err := closeWriters(); err != nil {
		return err
	}

	if _, err := os.Stat(storageRoot); err == nil {
		// The directory was observed empty before staging began. Removing that
		// empty placeholder lets the completed daily store appear atomically.
		if err := os.Remove(storageRoot); err != nil {
			// Another process may have created a daily file while migration was
			// staging. In that case do not overwrite it; leave migration for the
			// next startup/collection pass.
			if errors.Is(err, syscall.ENOTEMPTY) {
				return nil
			}
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(stagingRoot, storageRoot); err != nil {
		return err
	}
	if err := writeBatteryStatsMigrationMarker(marker); err != nil {
		return err
	}
	return s.retireLegacyBatteryStatsFiles()
}

func (s *Server) retireLegacyBatteryStatsFiles() error {
	if s.workspace == "" {
		return nil
	}
	for _, name := range []string{batteryStatsFileName, batteryStatsDBFileName} {
		if err := os.Remove(filepath.Join(s.workspace, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Server) batteryStatsMigrationMarkerPath() string {
	if s.workspace == "" {
		return ""
	}
	return filepath.Join(s.workspace, "battery_stats_daily_v1.migrated")
}

func writeBatteryStatsMigrationMarker(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte("daily battery stats migration complete\n"), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *Server) pruneBatteryStatsFiles(storageRoot string, now time.Time) error {
	files, err := listBatteryStatsDailyFiles(storageRoot)
	if err != nil {
		return err
	}
	cutoff := batteryStatsRetentionCutoff(now, s.batteryStatsRetentionDaysSetting())
	for _, dailyFile := range files {
		if dailyFile.day.Before(cutoff) {
			if err := os.Remove(dailyFile.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func listBatteryStatsDailyFiles(storageRoot string) ([]batteryStatsDailyFile, error) {
	if storageRoot == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(storageRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]batteryStatsDailyFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		day, ok := parseBatteryStatsDailyFileName(entry.Name())
		if !ok {
			continue
		}
		files = append(files, batteryStatsDailyFile{path: filepath.Join(storageRoot, entry.Name()), day: day})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].day.Before(files[j].day) })
	return files, nil
}

// batteryStatsStorageSignature uses directory metadata and daily-file names,
// without reading every retained file on each sample. That detects the normal
// manual cleanup case even on filesystems with coarse directory timestamps: a
// deleted source file resets in-memory totals instead of leaving stale values.
func batteryStatsStorageSignature(storageRoot string) string {
	info, err := os.Stat(storageRoot)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "error:" + err.Error()
	}
	if !info.IsDir() {
		return "error:not-a-directory"
	}
	entries, err := os.ReadDir(storageRoot)
	if err != nil {
		return "error:" + err.Error()
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := parseBatteryStatsDailyFileName(entry.Name()); ok {
			names = append(names, entry.Name())
		}
	}
	return fmt.Sprintf("%d:%d:%s", info.ModTime().UnixNano(), info.Size(), strings.Join(names, ","))
}

func readBatteryStatsDailySamples(storageRoot string, from int64, cutoff time.Time) ([]batteryStatsSample, error) {
	files, err := listBatteryStatsDailyFiles(storageRoot)
	if err != nil {
		return nil, err
	}
	samples := make([]batteryStatsSample, 0, 256)
	for _, dailyFile := range files {
		if dailyFile.day.Before(cutoff) {
			continue
		}
		rows, err := readBatteryStatsSamples(dailyFile.path)
		if err != nil {
			return nil, err
		}
		dayKey := dailyFile.day.Format("2006-01-02")
		for _, sample := range rows {
			if batteryStatsSampleUseful(sample) && sample.Time >= from && batteryStatsDayKey(sample.Time) == dayKey {
				samples = append(samples, sample)
			}
		}
	}
	return samples, nil
}

func readBatteryStatsSamples(path string) ([]batteryStatsSample, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	rows := make([]batteryStatsSample, 0, 256)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var sample batteryStatsSample
		if err := json.Unmarshal(scanner.Bytes(), &sample); err == nil {
			rows = append(rows, sample)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func batteryStatsDailyFilePath(storageRoot string, timestamp int64) string {
	return filepath.Join(storageRoot, batteryStatsDailyFileName(timestamp))
}

func batteryStatsDailyFileName(timestamp int64) string {
	return batteryStatsDayKey(timestamp) + ".jsonl"
}

func batteryStatsDayKey(timestamp int64) string {
	return batteryStatsDayForUnix(timestamp).Format("2006-01-02")
}

func batteryStatsDayForUnix(timestamp int64) time.Time {
	local := time.Unix(timestamp, 0).In(time.Local)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
}

func batteryStatsRetentionCutoff(now time.Time, retentionDays int) time.Time {
	if retentionDays <= 0 {
		retentionDays = batteryStatsDefaultRetentionDays
	}
	local := now.In(time.Local)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.Local)
	return startOfToday.AddDate(0, 0, -(retentionDays - 1))
}

func parseBatteryStatsDailyFileName(name string) (time.Time, bool) {
	if len(name) != len("2006-01-02.jsonl") || !strings.HasSuffix(name, ".jsonl") {
		return time.Time{}, false
	}
	day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(name, ".jsonl"), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	return day, true
}

func (s *Server) shouldRecordBatteryStatsSampleLocked(sample batteryStatsSample, now time.Time) bool {
	if !s.batteryStats.hasLastSample {
		return true
	}
	lastTime := time.Unix(s.batteryStats.lastSample.Time, 0)
	if !now.After(lastTime) {
		return false
	}
	if now.Sub(lastTime) >= s.batteryStatsSampleInterval() {
		return true
	}
	if sample.Status != "" && sample.Status != s.batteryStats.lastSample.Status {
		return true
	}
	if sample.HasCapacity && s.batteryStats.lastSample.HasCapacity && math.Abs(sample.CapacityPercent-s.batteryStats.lastSample.CapacityPercent) >= batteryStatsMinCapacityDeltaPercent {
		return true
	}
	return false
}

func appendBatteryStatsSample(path string, sample batteryStatsSample) error {
	return appendBatteryStatsSamples(path, []batteryStatsSample{sample})
}

func appendBatteryStatsSamples(path string, samples []batteryStatsSample) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, sample := range samples {
		if err := encoder.Encode(sample); err != nil {
			return err
		}
	}
	return nil
}

func batteryStatsSampleFromReport(report batteryReport, now time.Time) batteryStatsSample {
	return batteryStatsSample{
		Time:             now.Unix(),
		Status:           report.Status,
		PowerMode:        report.PowerMode,
		BatteryDirection: report.BatteryDirection,
		CapacityPercent:  report.CapacityPercent,
		HasCapacity:      report.HasCapacity,
		CurrentMA:        report.CurrentMA,
		HasCurrent:       report.HasCurrent,
		VoltageV:         report.VoltageV,
		HasVoltage:       report.HasVoltage,
		PowerW:           report.PowerW,
		HasPower:         report.HasPower,
		InputPowerW:      report.InputPowerW,
		HasInputPower:    report.HasInputPower,
		ChargeMah:        report.ChargeMah,
		HasCharge:        report.HasCharge,
		EnergyWh:         report.EnergyWh,
		HasEnergy:        report.HasEnergy,
		FullChargeMah:    report.FullChargeMah,
		HasFullCharge:    report.HasFullCharge,
		DesignChargeMah:  report.DesignChargeMah,
		HasDesignCharge:  report.HasDesignCharge,
		FullEnergyWh:     report.FullEnergyWh,
		HasFullEnergy:    report.HasFullEnergy,
		DesignEnergyWh:   report.DesignEnergyWh,
		HasDesignEnergy:  report.HasDesignEnergy,
		HealthPercent:    report.HealthPercent,
		HasHealth:        report.HasHealth,
	}
}

func batteryStatsSampleUseful(sample batteryStatsSample) bool {
	if sample.Time <= 0 {
		return false
	}
	return sample.HasCapacity || sample.HasCurrent || sample.HasPower || sample.HasInputPower || sample.HasCharge || sample.HasEnergy || sample.HasFullCharge || sample.HasFullEnergy || sample.HasHealth
}

func (state *batteryStatsState) addBatteryStatsTransition(prev, next batteryStatsSample) {
	dt := time.Duration(next.Time-prev.Time) * time.Second
	if dt <= 0 || dt > batteryStatsMaxSampleGap {
		state.seedTrackedRemaining(next)
		return
	}
	hours := dt.Hours()
	segmentWh, hasWh := batteryStatsEnergyDeltaWh(prev, next, hours)
	state.updateTrackedRemaining(next, segmentWh, hasWh)
	if hasWh {
		if segmentWh >= 0 {
			state.chargeWh += segmentWh
		} else {
			state.dischargeWh += -segmentWh
		}
	}
	segmentMah, hasMah := batteryStatsChargeDeltaMah(prev, next, hours)
	if hasMah {
		if segmentMah >= 0 {
			state.chargeMah += segmentMah
		} else {
			state.dischargeMah += -segmentMah
		}
	}
	if hasWh && segmentWh < 0 && prev.HasCapacity && next.HasCapacity {
		drop := prev.CapacityPercent - next.CapacityPercent
		if drop >= batteryStatsMinCapacityDeltaPercent {
			usableWh := (-segmentWh) / (drop / 100)
			if usableWh > 0 && usableWh <= batteryStatsMaxEstimatedUsableWh {
				state.usableWhWeightedSum += usableWh * drop
				state.usableWhWeight += drop
			}
		}
	}
	if inputWh, ok := batteryStatsInputEnergyWh(prev, next, hours); ok {
		state.inputWh += inputWh
	}
}

func (state batteryStatsState) report(sample batteryStatsSample, interval time.Duration, writeInterval time.Duration) batteryStatsReport {
	report := batteryStatsReport{
		SampleCount:              state.sampleCount,
		MinSampleIntervalSeconds: int(interval / time.Second),
		SamplerIntervalSeconds:   int(interval / time.Second),
		WriteIntervalSeconds:     int(writeInterval / time.Second),
		PendingSampleCount:       len(state.pendingSamples),
		ChargeWh:                 state.chargeWh,
		DischargeWh:              state.dischargeWh,
		InputWh:                  state.inputWh,
		ChargeMah:                state.chargeMah,
		DischargeMah:             state.dischargeMah,
		DatabaseMode:             "daily-jsonl",
		StorageError:             state.storageError,
	}
	if state.hasLastSample {
		report.LastSampleTime = state.lastSample.Time
	}
	if state.lastFlushTime > 0 {
		report.LastWriteTime = state.lastFlushTime
	}

	fullWh, hasFullWh := batterySampleFullWh(sample)
	if !hasFullWh && state.usableWhWeight > 0 {
		fullWh = state.usableWhWeightedSum / state.usableWhWeight
		hasFullWh = true
	}
	if hasFullWh {
		report.EstimatedUsableWh = fullWh
		report.HasEstimatedUsableWh = true
	}
	if remainingWh, source, ok := state.remainingWh(sample, fullWh, hasFullWh); ok {
		report.EstimatedRemainingWh = remainingWh
		report.RemainingSource = source
		report.HasEstimatedRemainingWh = true
	}
	if health, ok := batterySampleHealthPercent(sample, fullWh, hasFullWh); ok {
		report.EstimatedHealthPercent = health
		report.HasEstimatedHealthPercent = true
	}
	if powerW, ok := batterySampleDischargePowerW(sample); ok {
		report.CurrentPowerW = powerW
		if report.HasEstimatedRemainingWh && report.EstimatedRemainingWh > 0 {
			report.RuntimeHours = report.EstimatedRemainingWh / powerW
			report.HasRuntime = true
		}
	}
	if sample.HasInputPower && sample.InputPowerW > batteryStatsMinPowerW {
		report.CurrentInputPowerW = sample.InputPowerW
	}
	if report.SampleCount < 2 {
		report.Message = "需要更多样本"
	} else if !report.HasEstimatedRemainingWh && report.InputWh <= 0 {
		report.Message = "需要电量或容量样本"
	} else if !report.HasRuntime {
		report.Message = "当前未处于电池放电"
	}
	return report
}

func (state *batteryStatsState) seedTrackedRemaining(sample batteryStatsSample) {
	if remainingWh, source, ok := batterySampleRemainingWh(sample, 0, false); ok {
		state.trackedRemainingWh = remainingWh
		state.hasTrackedRemaining = true
		state.trackedSource = source
		return
	}
	if fullWh, hasFullWh := batterySampleFullWh(sample); hasFullWh {
		if remainingWh, source, ok := batterySampleRemainingWh(sample, fullWh, true); ok {
			state.trackedRemainingWh = remainingWh
			state.hasTrackedRemaining = true
			state.trackedSource = source
		}
	}
}

func (state *batteryStatsState) updateTrackedRemaining(sample batteryStatsSample, segmentWh float64, hasSegmentWh bool) {
	if remainingWh, source, ok := batterySampleRemainingWh(sample, 0, false); ok && source != "capacity" {
		state.trackedRemainingWh = remainingWh
		state.hasTrackedRemaining = true
		state.trackedSource = source
		return
	}
	if state.hasTrackedRemaining && hasSegmentWh {
		state.trackedRemainingWh += segmentWh
		if state.trackedRemainingWh < 0 {
			state.trackedRemainingWh = 0
		}
		state.trackedSource = "database"
		return
	}
	state.seedTrackedRemaining(sample)
}

func (state batteryStatsState) remainingWh(sample batteryStatsSample, fullWh float64, hasFullWh bool) (float64, string, bool) {
	if remainingWh, source, ok := batterySampleRemainingWh(sample, 0, false); ok && source != "capacity" {
		return remainingWh, source, true
	}
	if state.hasTrackedRemaining {
		remainingWh := state.trackedRemainingWh
		if hasFullWh && remainingWh > fullWh {
			remainingWh = fullWh
		}
		return remainingWh, state.trackedSource, true
	}
	if remainingWh, source, ok := batterySampleRemainingWh(sample, fullWh, hasFullWh); ok {
		return remainingWh, source, true
	}
	return 0, "", false
}

func batteryStatsInputEnergyWh(prev, next batteryStatsSample, hours float64) (float64, bool) {
	prevInput, prevOK := batterySampleInputPowerW(prev)
	nextInput, nextOK := batterySampleInputPowerW(next)
	if prevOK && nextOK {
		return ((prevInput + nextInput) / 2) * hours, true
	}
	if nextOK {
		return nextInput * hours, true
	}
	return 0, false
}

func batteryStatsEnergyDeltaWh(prev, next batteryStatsSample, hours float64) (float64, bool) {
	if prev.HasEnergy && next.HasEnergy {
		return next.EnergyWh - prev.EnergyWh, true
	}
	if prev.HasCharge && next.HasCharge {
		if voltage, ok := averageBatteryVoltage(prev, next); ok {
			return (batteryPackChargeMAh(next.ChargeMah, voltage) - batteryPackChargeMAh(prev.ChargeMah, voltage)) * voltage / 1000, true
		}
	}
	prevPower, prevOK := batterySampleSignedPowerW(prev)
	nextPower, nextOK := batterySampleSignedPowerW(next)
	if prevOK && nextOK {
		return ((prevPower + nextPower) / 2) * hours, true
	}
	if nextOK {
		return nextPower * hours, true
	}
	return 0, false
}

func batteryStatsChargeDeltaMah(prev, next batteryStatsSample, hours float64) (float64, bool) {
	if prev.HasCharge && next.HasCharge {
		if voltage, ok := averageBatteryVoltage(prev, next); ok {
			return batteryPackChargeMAh(next.ChargeMah, voltage) - batteryPackChargeMAh(prev.ChargeMah, voltage), true
		}
		return next.ChargeMah - prev.ChargeMah, true
	}
	prevCurrent, prevOK := batterySampleSignedCurrentMA(prev)
	nextCurrent, nextOK := batterySampleSignedCurrentMA(next)
	if prevOK && nextOK {
		return ((prevCurrent + nextCurrent) / 2) * hours, true
	}
	if nextOK {
		return nextCurrent * hours, true
	}
	return 0, false
}

func batterySampleFullWh(sample batteryStatsSample) (float64, bool) {
	if sample.HasFullEnergy && sample.FullEnergyWh > 0 {
		return sample.FullEnergyWh, true
	}
	if sample.HasFullCharge && sample.FullChargeMah > 0 && sample.HasVoltage && sample.VoltageV > 0 {
		return batteryPackChargeMAh(sample.FullChargeMah, sample.VoltageV) * sample.VoltageV / 1000, true
	}
	return 0, false
}

func batterySampleRemainingWh(sample batteryStatsSample, fullWh float64, hasFullWh bool) (float64, string, bool) {
	if sample.HasEnergy && sample.EnergyWh > 0 {
		return sample.EnergyWh, "energy", true
	}
	if sample.HasCharge && sample.ChargeMah > 0 && sample.HasVoltage && sample.VoltageV > 0 {
		return batteryPackChargeMAh(sample.ChargeMah, sample.VoltageV) * sample.VoltageV / 1000, "charge", true
	}
	if hasFullWh && sample.HasCapacity {
		percent := clampFloat(sample.CapacityPercent, 0, 100)
		return fullWh * percent / 100, "capacity", true
	}
	return 0, "", false
}

func batterySampleHealthPercent(sample batteryStatsSample, fullWh float64, hasFullWh bool) (float64, bool) {
	if sample.HasHealth {
		return sample.HealthPercent, true
	}
	designWh, hasDesignWh := batterySampleDesignWh(sample)
	if hasFullWh && hasDesignWh && designWh > 0 {
		return clampFloat(fullWh*100/designWh, 0, 200), true
	}
	return 0, false
}

func batterySampleDesignWh(sample batteryStatsSample) (float64, bool) {
	if sample.HasDesignEnergy && sample.DesignEnergyWh > 0 {
		return sample.DesignEnergyWh, true
	}
	if sample.HasDesignCharge && sample.DesignChargeMah > 0 && sample.HasVoltage && sample.VoltageV > 0 {
		return batteryPackChargeMAh(sample.DesignChargeMah, sample.VoltageV) * sample.VoltageV / 1000, true
	}
	return 0, false
}

func batteryPackChargeMAh(chargeMAh float64, voltageV float64) float64 {
	seriesCells := batterySeriesCells(voltageV)
	if seriesCells <= 1 {
		return chargeMAh
	}
	return chargeMAh / float64(seriesCells)
}

func batterySeriesCells(voltageV float64) int {
	if configuredBatterySeriesCells > 0 {
		return configuredBatterySeriesCells
	}
	if voltageV <= 0 {
		return 1
	}
	const nominalLiIonVoltage = 3.84
	bestCells := 1
	bestDistance := math.MaxFloat64
	for cells := 1; cells <= 6; cells++ {
		cellVoltage := voltageV / float64(cells)
		if cellVoltage < 3.0 || cellVoltage > 4.5 {
			continue
		}
		distance := math.Abs(cellVoltage - nominalLiIonVoltage)
		if distance < bestDistance {
			bestDistance = distance
			bestCells = cells
		}
	}
	return bestCells
}

func batterySampleInputPowerW(sample batteryStatsSample) (float64, bool) {
	if !sample.HasInputPower || sample.InputPowerW <= batteryStatsMinPowerW {
		return 0, false
	}
	return sample.InputPowerW, true
}

func batterySampleDischargePowerW(sample batteryStatsSample) (float64, bool) {
	// A current direct/external path must never create a remaining-runtime
	// estimate, even when a vendor exposes a stale negative battery reading.
	switch sample.PowerMode {
	case "direct", "external", "charging", "idle":
		return 0, false
	}
	powerW, ok := batterySampleSignedPowerW(sample)
	if !ok || powerW >= -batteryStatsMinPowerW {
		return 0, false
	}
	return -powerW, true
}

func batterySampleSignedPowerW(sample batteryStatsSample) (float64, bool) {
	powerW := 0.0
	if sample.HasPower {
		powerW = sample.PowerW
	} else if sample.HasCurrent && sample.HasVoltage {
		powerW = sample.CurrentMA * sample.VoltageV / 1000
	} else {
		return 0, false
	}
	if math.Abs(powerW) < batteryStatsMinPowerW {
		return 0, false
	}
	if direction := batteryStatusDirection(sample.Status); direction != 0 {
		return float64(direction) * math.Abs(powerW), true
	}
	return powerW, true
}

func batterySampleSignedCurrentMA(sample batteryStatsSample) (float64, bool) {
	if !sample.HasCurrent || math.Abs(sample.CurrentMA) < batteryStatsMinCurrentMA {
		return 0, false
	}
	if direction := batteryStatusDirection(sample.Status); direction != 0 {
		return float64(direction) * math.Abs(sample.CurrentMA), true
	}
	return sample.CurrentMA, true
}

func averageBatteryVoltage(prev, next batteryStatsSample) (float64, bool) {
	switch {
	case prev.HasVoltage && next.HasVoltage && prev.VoltageV > 0 && next.VoltageV > 0:
		return (prev.VoltageV + next.VoltageV) / 2, true
	case next.HasVoltage && next.VoltageV > 0:
		return next.VoltageV, true
	case prev.HasVoltage && prev.VoltageV > 0:
		return prev.VoltageV, true
	default:
		return 0, false
	}
}

func batteryStatusDirection(status string) int {
	status = strings.ToLower(strings.TrimSpace(status))
	if strings.Contains(status, "discharging") {
		return -1
	}
	if strings.Contains(status, "not charging") || strings.Contains(status, "full") {
		return 0
	}
	if strings.Contains(status, "charging") {
		return 1
	}
	return 0
}

func batteryHealthPercent(fullChargeMah float64, hasFullCharge bool, designChargeMah float64, hasDesignCharge bool, fullEnergyWh float64, hasFullEnergy bool, designEnergyWh float64, hasDesignEnergy bool) (float64, bool) {
	if hasFullEnergy && hasDesignEnergy && designEnergyWh > 0 {
		return clampFloat(fullEnergyWh*100/designEnergyWh, 0, 200), true
	}
	if hasFullCharge && hasDesignCharge && designChargeMah > 0 {
		return clampFloat(fullChargeMah*100/designChargeMah, 0, 200), true
	}
	return 0, false
}

type batteryValueName struct {
	Name  string
	Scale float64
}

type batteryValueCandidate struct {
	Path  string
	Scale float64
}

type inputPowerTelemetry struct {
	currentMA     float64
	hasCurrent    bool
	currentSource string
	voltageV      float64
	hasVoltage    bool
	voltageSource string
	powerW        float64
	hasPower      bool
	kind          string
	source        string
}

func batteryPowerSupplyRoots() []string {
	roots := []string{filepath.Join(powerSupplyRoot, "battery"), filepath.Join(powerSupplyRoot, "bms")}
	entries, err := os.ReadDir(powerSupplyRoot)
	if err == nil {
		for _, entry := range entries {
			path := filepath.Join(powerSupplyRoot, entry.Name())
			name := strings.ToLower(entry.Name())
			typ := strings.ToLower(readFirstFile(filepath.Join(path, "type")))
			if typ == "battery" || strings.Contains(name, "battery") || strings.Contains(name, "bms") || strings.Contains(name, "fg") {
				roots = append(roots, path)
			}
		}
	}
	return uniqueCleanPaths(roots)
}

func inputPowerSupplyRoots() []string {
	entries, err := os.ReadDir(powerSupplyRoot)
	if err != nil {
		return nil
	}
	roots := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(powerSupplyRoot, entry.Name())
		typ := strings.ToLower(readFirstFile(filepath.Join(path, "type")))
		if strings.HasPrefix(typ, "usb") || typ == "wireless" || typ == "mains" {
			if powerSupplyOnline(path) {
				roots = append(roots, path)
			}
		}
	}
	roots = uniqueCleanPaths(roots)
	sort.SliceStable(roots, func(i, j int) bool {
		return inputPowerSupplyRootPriority(roots[i]) < inputPowerSupplyRootPriority(roots[j])
	})
	return roots
}

// inputPowerSupplyRootPriority prefers the charging controller's physical
// power-supply node. UCSI source nodes commonly expose the negotiated PD
// voltage/current pair, which is useful as a contract diagnostic but is not
// necessarily the live current flowing into the device.
func inputPowerSupplyRootPriority(root string) int {
	name := strings.ToLower(filepath.Base(root))
	typ := strings.ToLower(strings.TrimSpace(readFirstFile(filepath.Join(root, "type"))))
	switch {
	case strings.Contains(name, "ucsi"):
		return 3
	case strings.HasPrefix(name, "usb") || strings.Contains(typ, "usb_pd"):
		return 0
	case strings.HasPrefix(typ, "usb") || typ == "mains" || typ == "wireless":
		return 1
	default:
		return 2
	}
}

func inputPowerSupplyIsPDContract(root string) bool {
	return strings.Contains(strings.ToLower(filepath.Base(root)), "ucsi")
}

func readInputPowerTelemetry(roots []string) inputPowerTelemetry {
	// Never turn a UCSI contract into input energy. Android UCSI providers can
	// report the negotiated PD voltage/current pair as *_now even when the
	// physical charger controller reports a much smaller live current.
	measuredRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		if !inputPowerSupplyIsPDContract(root) {
			measuredRoots = append(measuredRoots, root)
		}
	}
	roots = measuredRoots

	nowPowerNames := []batteryValueName{
		{"power_now", 0.000001},
		{"input_power_now", 0.000001},
		{"input_power_now_mw", 0.001},
	}
	nowCurrentNames := []batteryValueName{
		{"current_now", 0.001},
		{"input_current_now", 0.001},
	}
	nowVoltageNames := []batteryValueName{
		{"voltage_now", 0.000001},
	}
	averagePowerNames := []batteryValueName{{"power_avg", 0.000001}}
	averageCurrentNames := []batteryValueName{{"current_avg", 0.001}}
	averageVoltageNames := []batteryValueName{{"voltage_avg", 0.000001}}

	readValues := func(root string, currentNames, voltageNames []batteryValueName) inputPowerTelemetry {
		currentMA, hasCurrent, currentSource := readBestBatteryValue(batteryValueCandidates([]string{root}, currentNames), true)
		voltageV, hasVoltage, voltageSource := readBestBatteryValue(batteryValueCandidates([]string{root}, voltageNames), true)
		return inputPowerTelemetry{
			currentMA: currentMA, hasCurrent: hasCurrent, currentSource: currentSource,
			voltageV: voltageV, hasVoltage: hasVoltage, voltageSource: voltageSource,
		}
	}

	for _, root := range roots {
		powerW, ok, source := readBestBatteryValue(batteryValueCandidates([]string{root}, nowPowerNames), true)
		if !ok {
			continue
		}
		telemetry := readValues(root, nowCurrentNames, nowVoltageNames)
		telemetry.powerW = powerW
		telemetry.hasPower = true
		telemetry.kind = "measured"
		telemetry.source = source
		return telemetry
	}

	var partial inputPowerTelemetry
	for _, root := range roots {
		telemetry := readValues(root, nowCurrentNames, nowVoltageNames)
		if telemetry.hasCurrent && telemetry.hasVoltage {
			telemetry.powerW = telemetry.currentMA * telemetry.voltageV / 1000
			telemetry.hasPower = true
			telemetry.kind = "measured"
			telemetry.source = "computed:" + telemetry.currentSource + "+" + telemetry.voltageSource
			return telemetry
		}
		if !partial.hasCurrent && !partial.hasVoltage && (telemetry.hasCurrent || telemetry.hasVoltage) {
			partial = telemetry
		}
	}

	for _, root := range roots {
		powerW, ok, source := readBestBatteryValue(batteryValueCandidates([]string{root}, averagePowerNames), true)
		if !ok {
			continue
		}
		telemetry := readValues(root, nowCurrentNames, nowVoltageNames)
		telemetry.powerW = powerW
		telemetry.hasPower = true
		telemetry.kind = "average"
		telemetry.source = source
		return telemetry
	}

	for _, root := range roots {
		telemetry := readValues(root, averageCurrentNames, averageVoltageNames)
		if telemetry.hasCurrent && telemetry.hasVoltage {
			telemetry.powerW = telemetry.currentMA * telemetry.voltageV / 1000
			telemetry.hasPower = true
			telemetry.kind = "average"
			telemetry.source = "computed:" + telemetry.currentSource + "+" + telemetry.voltageSource
			return telemetry
		}
	}
	return partial
}

func inputPowerSupplyOnline(roots []string) bool {
	for _, root := range roots {
		online := strings.TrimSpace(readFirstFile(filepath.Join(root, "online")))
		if online != "" && online != "0" {
			return true
		}
	}
	return false
}

func powerSupplyOnline(path string) bool {
	online := strings.TrimSpace(readFirstFile(filepath.Join(path, "online")))
	if online == "" {
		return true
	}
	return online != "0"
}

func uniqueCleanPaths(paths []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "." || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, path)
	}
	return out
}

func batteryValueCandidates(roots []string, names []batteryValueName) []batteryValueCandidate {
	candidates := make([]batteryValueCandidate, 0, len(roots)*len(names))
	for _, root := range roots {
		for _, name := range names {
			candidates = append(candidates, batteryValueCandidate{Path: filepath.Join(root, name.Name), Scale: name.Scale})
		}
	}
	return candidates
}

func readBestBatteryValue(candidates []batteryValueCandidate, preferNonZero bool) (float64, bool, string) {
	var zeroValue float64
	zeroSource := ""
	for _, candidate := range candidates {
		if candidate.Scale == 0 {
			continue
		}
		raw, ok := readFloatFile(candidate.Path)
		if !ok {
			continue
		}
		value := raw * candidate.Scale
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		if preferNonZero && value == 0 {
			if zeroSource == "" {
				zeroValue = value
				zeroSource = candidate.Path
			}
			continue
		}
		return value, true, candidate.Path
	}
	if !preferNonZero && zeroSource != "" {
		return zeroValue, true, zeroSource
	}
	return 0, false, ""
}

func readFirstFloatFile(base string, names ...string) (float64, bool, string) {
	for _, name := range names {
		path := filepath.Join(base, name)
		value, ok := readFloatFile(path)
		if ok {
			return value, true, path
		}
	}
	return 0, false, ""
}

func readFloatFile(path string) (float64, bool) {
	text := strings.TrimSpace(readFirstFile(path))
	if text == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func readFirstFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func readSystemVersion() string {
	for _, candidate := range []string{"/system/build.prop", "/vendor/build.prop"} {
		if value := readAndroidRelease(candidate); value != "" {
			return value
		}
	}
	data, err := os.ReadFile("/etc/os-release")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
			}
		}
	}
	return runtime.GOOS + "/" + runtime.GOARCH
}

func readAndroidRelease(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		props[key] = value
	}
	if release := props["ro.build.version.release"]; release != "" {
		if sdk := props["ro.build.version.sdk"]; sdk != "" {
			return "Android " + release + " (SDK " + sdk + ")"
		}
		return "Android " + release
	}
	return ""
}

func readKernelVersion() string {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

func formatBytes(value uint64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	n := float64(value)
	unit := 0
	for n >= 1024 && unit < len(units)-1 {
		n /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.1f %s", n, units[unit])
}

type pathReport struct {
	Key           string `json:"key"`
	Path          string `json:"path"`
	Exists        bool   `json:"exists"`
	IsDir         bool   `json:"isDir"`
	Size          int64  `json:"size,omitempty"`
	Modified      int64  `json:"modified,omitempty"`
	DiskTotal     uint64 `json:"diskTotal,omitempty"`
	DiskFree      uint64 `json:"diskFree,omitempty"`
	DiskAvailable uint64 `json:"diskAvailable,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (s *Server) pathReport(key string, path string) pathReport {
	report := pathReport{Key: key, Path: path}
	path = strings.TrimSpace(path)
	if path == "" {
		report.Error = "not configured"
		return report
	}
	info, err := os.Stat(path)
	statPath := path
	if err != nil {
		report.Error = err.Error()
		statPath = filepath.Dir(path)
	} else {
		report.Exists = true
		report.IsDir = info.IsDir()
		report.Size = info.Size()
		report.Modified = info.ModTime().Unix()
		if !info.IsDir() {
			statPath = filepath.Dir(path)
		}
	}
	var fsStat syscall.Statfs_t
	if statErr := syscall.Statfs(statPath, &fsStat); statErr == nil {
		blockSize := uint64(fsStat.Bsize)
		report.DiskTotal = fsStat.Blocks * blockSize
		report.DiskFree = fsStat.Bfree * blockSize
		report.DiskAvailable = fsStat.Bavail * blockSize
	} else if report.Error == "" {
		report.Error = statErr.Error()
	}
	return report
}

func cloudInitEnabled(req createContainerRequest) bool {
	return req.CloudInitEnabled != nil && *req.CloudInitEnabled
}

func enableCloudInitForAsset(req *createContainerRequest, asset rootfs.Asset) {
	if req == nil || req.CloudInitEnabled != nil || rootfsAssetStorageVariant(asset) != "cloud" {
		return
	}
	enabled := true
	req.CloudInitEnabled = &enabled
}

func enableCloudInitForLocalTemplate(req *createContainerRequest, templatePath string) {
	if req == nil || req.CloudInitEnabled != nil {
		return
	}
	if isLinuxContainersCloudTemplate(templatePath) {
		enabled := true
		req.CloudInitEnabled = &enabled
	}
}

func isLinuxContainersCloudTemplate(templatePath string) bool {
	path := "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Clean(templatePath)), "/") + "/"
	for _, directory := range []string{rootfsLinuxContainersDirectory, rootfsLinuxContainersPreviousDirectory, rootfsLinuxContainersLegacyDir} {
		if strings.Contains(path, "/"+directory+"/cloud/") {
			return true
		}
	}
	return false
}

func isLinuxContainersRootfsAsset(asset rootfs.Asset) bool {
	sourceName := strings.TrimSpace(asset.SourceRepoName)
	if config.IsLinuxContainersRepositoryName(sourceName) || strings.Contains(strings.ToLower(sourceName), "lxc-image") || strings.Contains(strings.ToLower(sourceName), "linux containers") || strings.EqualFold(strings.TrimSpace(asset.Author), "Linux Containers") {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(asset.DownloadURL))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Hostname(), "images.linuxcontainers.org") ||
		(strings.EqualFold(parsed.Hostname(), "mirror.nju.edu.cn") && strings.HasPrefix(strings.TrimRight(parsed.EscapedPath(), "/"), "/lxc-images/images/"))
}

func (s *Server) applyCloudInitToRootfs(ctx context.Context, rootfsPath string, req createContainerRequest) error {
	rootfsPath = strings.TrimSpace(rootfsPath)
	if rootfsPath == "" {
		return fmt.Errorf("cloud-init rootfs path is empty")
	}
	if strings.HasSuffix(strings.ToLower(rootfsPath), ".img") {
		mountPoint := filepath.Join(filepath.Dir(rootfsPath), ".cloud-init-"+newUUID())
		if err := os.MkdirAll(mountPoint, 0755); err != nil {
			return err
		}
		defer os.RemoveAll(mountPoint)
		_ = exec.CommandContext(ctx, "chcon", "u:object_r:vold_data_file:s0", rootfsPath).Run()
		if err := s.mountRootfsImage(ctx, rootfsPath, mountPoint); err != nil {
			return fmt.Errorf("mount rootfs image for cloud-init: %w", err)
		}
		defer s.umountRootfsImage(context.Background(), mountPoint)
		return writeCloudInitNoCloudSeed(mountPoint, req)
	}
	return writeCloudInitNoCloudSeed(rootfsPath, req)
}

// prepareCloudInitNATNetworkConfig turns the NAT reservation made during
// container creation into a NoCloud network-config. The official Droidspaces
// core continues to provide the NAT bridge and DHCP reservation; cloud-init
// configures eth0 directly so a cloud image does not need to initiate DHCP.
// An explicit network-config remains authoritative.
func prepareCloudInitNATNetworkConfig(req *createContainerRequest) (bool, error) {
	if req == nil || strings.ToLower(strings.TrimSpace(req.NetMode)) != "nat" || strings.TrimSpace(req.CloudInitNetwork) != "" {
		return false, nil
	}

	staticIP := strings.TrimSpace(req.StaticNATIP)
	if err := validateStaticNATIP(staticIP); err != nil {
		return false, fmt.Errorf("static NAT address for cloud-init: %w", err)
	}
	dnsServers := cloudInitDNSServers(req.DNSServers)

	lines := []string{
		"version: 2",
		"ethernets:",
		"  eth0:",
		"    dhcp4: false",
		"    addresses:",
		"      - " + strconv.Quote(fmt.Sprintf("%s/%d", staticIP, cloudInitNATPrefix)),
		"    routes:",
		"      - to: 0.0.0.0/0",
		"        via: " + strconv.Quote(cloudInitNATGateway),
	}
	if len(dnsServers) > 0 {
		lines = append(lines, "    nameservers:", "      addresses:")
		for _, server := range dnsServers {
			lines = append(lines, "        - "+strconv.Quote(server))
		}
	}
	req.CloudInitNetwork = strings.Join(lines, "\n") + "\n"
	req.CloudInitNATStatic = true
	return true, nil
}

func cloudInitDNSServers(value string) []string {
	parts := strings.FieldsFunc(strings.TrimSpace(value), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	})
	servers := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, part := range parts {
		ip := net.ParseIP(strings.TrimSpace(part))
		if ip == nil || ip.To4() == nil {
			continue
		}
		server := ip.To4().String()
		if !seen[server] {
			seen[server] = true
			servers = append(servers, server)
		}
	}
	if len(servers) == 0 {
		return []string{"1.1.1.1", "8.8.8.8"}
	}
	return servers
}

func writeCloudInitNoCloudSeed(rootfsDir string, req createContainerRequest) error {
	info, err := os.Stat(rootfsDir)
	if err != nil {
		return fmt.Errorf("cloud-init rootfs is not accessible: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("cloud-init rootfs must be a directory or rootfs image")
	}

	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = sanitizeContainerName(req.Name)
	}
	if hostname == "" {
		return fmt.Errorf("cloud-init hostname is empty")
	}
	if err := validateCloudInitDocument("cloudInitUserData", req.CloudInitUserData); err != nil {
		return err
	}
	if err := validateCloudInitDocument("cloudInitNetworkConfig", req.CloudInitNetwork); err != nil {
		return err
	}

	// Linux Containers cloud images intentionally ship with cloud-init disabled
	// for generic container use. A NoCloud seed is meaningful only after that
	// marker is removed for this explicitly cloud-init-enabled container.
	disabledMarker := filepath.Join(rootfsDir, "etc", "cloud", "cloud-init.disabled")
	if err := os.Remove(disabledMarker); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("enable cloud-init: %w", err)
	}

	seedDir := filepath.Join(rootfsDir, "var", "lib", "cloud", "seed", "nocloud")
	for _, path := range []string{
		filepath.Join(rootfsDir, "var", "lib", "cloud", "instance"),
		filepath.Join(rootfsDir, "var", "lib", "cloud", "instances"),
		filepath.Join(rootfsDir, "var", "lib", "cloud", "data"),
		filepath.Join(rootfsDir, "var", "lib", "cloud", "sem"),
		seedDir,
	} {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("reset cloud-init state: %w", err)
		}
	}
	if err := os.MkdirAll(seedDir, 0755); err != nil {
		return err
	}

	userData := strings.TrimSpace(req.CloudInitUserData)
	if userData == "" {
		userData = "#cloud-config\n" +
			"hostname: " + strconv.Quote(hostname) + "\n" +
			"manage_etc_hosts: true\n" +
			"preserve_hostname: false\n"
	}
	if !strings.HasSuffix(userData, "\n") {
		userData += "\n"
	}
	metaData := "instance-id: " + strconv.Quote("droidspaces-"+sanitizeContainerName(req.Name)+"-"+newUUID()) + "\n" +
		"local-hostname: " + strconv.Quote(hostname) + "\n"
	if err := os.WriteFile(filepath.Join(seedDir, "user-data"), []byte(userData), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(seedDir, "meta-data"), []byte(metaData), 0644); err != nil {
		return err
	}

	cloudConfig := "datasource_list: [ NoCloud, None ]\n"
	networkConfig := strings.TrimSpace(req.CloudInitNetwork)
	if networkConfig == "" {
		// Droidspaces owns the container network setup by default. A custom
		// NoCloud network-config opts into cloud-init network rendering instead.
		cloudConfig += "network:\n  config: disabled\n"
	} else {
		if !strings.HasSuffix(networkConfig, "\n") {
			networkConfig += "\n"
		}
		if req.CloudInitNATStatic {
			// The post-extraction setup supplies 10-eth-dhcp.network. Force
			// cloud-init's networkd renderer so its 10-cloud-init-eth0.network
			// is selected first and the static NAT rule persists across reboots.
			cloudConfig += "system_info:\n  network:\n    renderers: [networkd]\n"
		}
		if err := os.WriteFile(filepath.Join(seedDir, "network-config"), []byte(networkConfig), 0600); err != nil {
			return err
		}
	}
	configDir := filepath.Join(rootfsDir, "etc", "cloud", "cloud.cfg.d")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(configDir, "99-droidspaces-nocloud.cfg"), []byte(cloudConfig), 0644)
}

func validateCloudInitDocument(name string, value string) error {
	if len([]byte(value)) > maxCloudInitDocumentBytes {
		return fmt.Errorf("%s must not exceed %d KiB", name, maxCloudInitDocumentBytes/1024)
	}
	if strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s must not contain NUL bytes", name)
	}
	return nil
}

func (s *Server) prepareRootfsForContainer(ctx context.Context, req createContainerRequest, source string, rootfsPath string, containerDir string) (string, error) {
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return "", fmt.Errorf("rootfsPath is not accessible: %v", err)
	}
	lower := strings.ToLower(rootfsPath)
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
	if !info.IsDir() && !isRootfsArchive(lower) {
		return "", fmt.Errorf("rootfs template must be a directory, .img, .tar.gz, .tgz, or .tar.xz")
	}
	if rootfsStorageMode(req) == "directory" {
		if source == "direct" && info.IsDir() {
			return rootfsPath, nil
		}
		dest := filepath.Join(containerDir, "rootfs")
		if info.IsDir() {
			if err := s.copyRootfsDirectory(ctx, rootfsPath, dest); err != nil {
				return "", err
			}
		} else if err := s.extractRootfsArchive(ctx, rootfsPath, dest); err != nil {
			return "", err
		}
		return dest, nil
	}
	dest := filepath.Join(containerDir, "rootfs.img")
	if err := s.createRootfsImage(ctx, req, rootfsPath, dest, info.IsDir()); err != nil {
		return "", err
	}
	return dest, nil
}

func (s *Server) createRootfsImage(ctx context.Context, req createContainerRequest, source string, dest string, sourceIsDir bool) error {
	if os.Getenv("WEBUI_ROOTFS_IMG_MOCK") == "1" {
		return s.createRootfsImageMock(ctx, source, dest, sourceIsDir)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	_ = os.Remove(dest)
	sizeGB, err := rootfsImageSizeGB(req)
	if err != nil {
		return err
	}
	if err := s.truncateRootfsImage(ctx, dest, sizeGB); err != nil {
		_ = os.Remove(dest)
		return err
	}
	if err := s.formatRootfsImage(ctx, dest); err != nil {
		_ = os.Remove(dest)
		return err
	}
	mountPoint := filepath.Join(filepath.Dir(dest), "rootfs")
	if err := os.RemoveAll(mountPoint); err != nil {
		_ = os.Remove(dest)
		return err
	}
	if err := os.MkdirAll(mountPoint, 0755); err != nil {
		_ = os.Remove(dest)
		return err
	}
	mounted := false
	defer func() {
		if mounted {
			_ = s.umountRootfsImage(context.Background(), mountPoint)
		}
		_ = os.RemoveAll(mountPoint)
	}()
	if err := s.mountRootfsImage(ctx, dest, mountPoint); err != nil {
		_ = os.Remove(dest)
		return err
	}
	mounted = true
	if sourceIsDir {
		if err := s.copyRootfsDirectoryInto(ctx, source, mountPoint); err != nil {
			_ = os.Remove(dest)
			return err
		}
	} else if err := s.extractRootfsArchiveInto(ctx, source, mountPoint); err != nil {
		_ = os.Remove(dest)
		return err
	}
	if err := s.umountRootfsImage(ctx, mountPoint); err != nil {
		_ = os.Remove(dest)
		return err
	}
	mounted = false
	_ = exec.CommandContext(ctx, "chcon", "u:object_r:vold_data_file:s0", dest).Run()
	if err := s.fsckRootfsImage(ctx, dest); err != nil {
		_ = os.Remove(dest)
		return err
	}
	return nil
}

func rootfsStorageMode(req createContainerRequest) string {
	if req.UseSparseImage != nil {
		if *req.UseSparseImage {
			return "image"
		}
		return "directory"
	}
	mode := strings.ToLower(strings.TrimSpace(req.RootFSStorageMode))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(req.StorageMode))
	}
	if mode == "directory" || mode == "dir" || mode == "rootfs" {
		return "directory"
	}
	return "image"
}

func rootfsImageSizeGB(req createContainerRequest) (int, error) {
	size := req.RootFSImageSizeGB
	if size == 0 {
		size = req.ImageSizeGB
	}
	if size == 0 {
		size = defaultRootfsImageSizeGB
	}
	if size < minRootfsImageSizeGB || size > maxRootfsImageSizeGB {
		return 0, fmt.Errorf("rootfs image size must be between %d and %d GB", minRootfsImageSizeGB, maxRootfsImageSizeGB)
	}
	return size, nil
}

func (s *Server) createRootfsImageMock(ctx context.Context, source string, dest string, sourceIsDir bool) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	if sourceIsDir {
		return s.archiveDirectory(ctx, source, dest, "")
	}
	if isRootfsArchive(source) {
		return s.copyRootfsFile(ctx, source, dest)
	}
	return fmt.Errorf("unsupported mock rootfs source: %s", source)
}

func (s *Server) truncateRootfsImage(ctx context.Context, dest string, sizeGB int) error {
	cmdText := fmt.Sprintf("truncate -s %dG %s || %s truncate -s %dG %s", sizeGB, shellQuote(dest), shellQuote(s.busyboxPath()), sizeGB, shellQuote(dest))
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("create rootfs image failed: %v\n%s", err, string(out))
	}
	return nil
}

func (s *Server) formatRootfsImage(ctx context.Context, dest string) error {
	cmdText := fmt.Sprintf("mkfs.ext4 -F -E lazy_itable_init=0,lazy_journal_init=0 -L droidspaces-rootfs %s || mke2fs -t ext4 -F -E lazy_itable_init=0,lazy_journal_init=0 -L droidspaces-rootfs %s", shellQuote(dest), shellQuote(dest))
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("format rootfs image failed: %v\n%s", err, string(out))
	}
	_ = exec.CommandContext(ctx, "tune2fs", "-m", "0", dest).Run()
	return nil
}

func (s *Server) mountRootfsImage(ctx context.Context, imagePath string, mountPoint string) error {
	busybox := shellQuote(s.busyboxPath())
	cmdText := fmt.Sprintf("%s mount -t ext4 -o loop,rw,nodelalloc,noatime,nodiratime,init_itable=0 %s %s || mount -t ext4 -o loop,rw,nodelalloc,noatime,nodiratime,init_itable=0 %s %s", busybox, shellQuote(imagePath), shellQuote(mountPoint), shellQuote(imagePath), shellQuote(mountPoint))
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount rootfs image failed: %v\n%s", err, string(out))
	}
	return nil
}

func (s *Server) umountRootfsImage(ctx context.Context, mountPoint string) error {
	busybox := shellQuote(s.busyboxPath())
	cmdText := fmt.Sprintf("sync; %s umount -l %s || umount -l %s", busybox, shellQuote(mountPoint), shellQuote(mountPoint))
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("unmount rootfs image failed: %v\n%s", err, string(out))
	}
	return nil
}

func (s *Server) fsckRootfsImage(ctx context.Context, imagePath string) error {
	cmd := exec.CommandContext(ctx, "e2fsck", "-fy", imagePath)
	cmd.Env = s.terminalEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() < 4 {
		return nil
	}
	return fmt.Errorf("verify rootfs image failed: %v\n%s", err, string(out))
}

func (s *Server) localRootfsItems() ([]localRootfsItem, error) {
	seen := map[string]bool{}
	items := make([]localRootfsItem, 0)

	if s.templateImageRoot != "" {
		templateSources := s.localTemplateRootfsSources()
		sourceDirectories := make(map[string]bool, len(templateSources))
		for _, source := range templateSources {
			sourceDirectories[source.directory] = true
		}

		legacyItems, err := s.scanLocalRootfsDir(s.templateImageRoot, "", "旧模板目录", sourceDirectories, seen)
		if err != nil {
			return nil, err
		}
		items = append(items, legacyItems...)

		for _, source := range templateSources {
			root := filepath.Join(s.templateImageRoot, source.directory)
			var sourceItems []localRootfsItem
			if source.variantDirectories {
				sourceItems, err = s.scanLocalRootfsVariantDirs(root, source.kindOverride, source.label, seen)
			} else {
				sourceItems, err = s.scanLocalRootfsDir(root, source.kindOverride, source.label, nil, seen)
			}
			if err != nil {
				return nil, err
			}
			items = append(items, sourceItems...)
		}
	}

	if s.imageRoot != "" && !sameRootfsStoragePath(s.imageRoot, s.templateImageRoot) {
		coreSkip := map[string]bool{rootfsExportsDirectory: true}
		if name, ok := rootfsDirectChildName(s.imageRoot, s.templateImageRoot); ok {
			coreSkip[name] = true
		}
		coreItems, err := s.scanLocalRootfsDir(s.imageRoot, "", "Core 镜像目录", coreSkip, seen)
		if err != nil {
			return nil, err
		}
		items = append(items, coreItems...)

		coreExports, err := s.scanLocalRootfsDir(filepath.Join(s.imageRoot, rootfsExportsDirectory), "backup", "备份导出", nil, seen)
		if err != nil {
			return nil, err
		}
		items = append(items, coreExports...)
	}

	return items, nil
}

func (s *Server) localTemplateRootfsSources() []localRootfsStorageSource {
	sources := []localRootfsStorageSource{
		{directory: rootfsDroidspacesOfficialDirectory, label: "Droidspaces Official"},
		{directory: rootfsLinuxContainersDirectory, label: config.LinuxContainersRepositoryName, variantDirectories: true},
		{directory: rootfsLinuxContainersPreviousDirectory, label: "lxc-image（旧目录）", variantDirectories: true},
		{directory: rootfsLinuxContainersLegacyDir, label: "lxc-image（旧目录）", variantDirectories: true},
		{directory: rootfsUploadsDirectory, label: "本地上传"},
		{directory: rootfsExportsDirectory, label: "备份导出", kindOverride: "backup"},
	}
	seen := make(map[string]bool, len(sources))
	for _, source := range sources {
		seen[source.directory] = true
	}
	_, repositories, _ := s.rootfsConfigurationSnapshot()
	for _, repository := range repositories {
		source := rootfsTemplateStorageSourceForRepository(repository)
		if !seen[source.directory] {
			sources = append(sources, source)
			seen[source.directory] = true
		}
	}

	// Custom repository directories remain discoverable after their repository
	// configuration has been removed, so previously downloaded templates stay
	// usable instead of being mistaken for one legacy directory template.
	entries, err := os.ReadDir(s.templateImageRoot)
	if err == nil {
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), rootfsRepositoryDirectoryPrefix) || seen[entry.Name()] {
				continue
			}
			sources = append(sources, localRootfsStorageSource{directory: entry.Name(), label: "自定义镜像仓库"})
			seen[entry.Name()] = true
		}
	}
	return sources
}

func (s *Server) rootfsTemplateStorageSourceForAsset(asset rootfs.Asset) localRootfsStorageSource {
	_, repositories, _ := s.rootfsConfigurationSnapshot()
	return rootfsTemplateStorageSourceForAssetFromRepositories(asset, repositories)
}

func rootfsTemplateStorageSourceForAssetFromRepositories(asset rootfs.Asset, repositories []config.RootfsRepository) localRootfsStorageSource {
	repository := config.RootfsRepository{Name: strings.TrimSpace(asset.SourceRepoName)}
	for _, candidate := range repositories {
		if strings.EqualFold(strings.TrimSpace(candidate.Name), repository.Name) {
			repository = candidate
			break
		}
	}
	if repository.URL == "" {
		repository.URL = asset.DownloadURL
	}
	return rootfsTemplateStorageSourceForRepository(repository)
}

// rootfsTemplateStorageDirectoryForAsset keeps lxc-image variants
// separate. Images with the same release and build can exist as default and
// cloud variants, so flattening them would overwrite a usable template.
func (s *Server) rootfsTemplateStorageDirectoryForAsset(asset rootfs.Asset) string {
	source := s.rootfsTemplateStorageSourceForAsset(asset)
	return rootfsTemplateStorageDirectoryForAssetWithSource(asset, source)
}

func rootfsTemplateStorageDirectoryForAssetWithSource(asset rootfs.Asset, source localRootfsStorageSource) string {
	if source.directory != rootfsLinuxContainersDirectory {
		return source.directory
	}
	return filepath.Join(source.directory, rootfsAssetStorageVariant(asset))
}

func rootfsAssetStorageVariant(asset rootfs.Asset) string {
	return rootfsStorageVariantDirectory(asset.Variant)
}

func rootfsStorageVariantDirectory(value string) string {
	value = strings.Trim(rootfsStorageComponentUnsafe.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
	if value == "" {
		return "default"
	}
	return value
}

func rootfsTemplateStorageSourceForRepository(repository config.RootfsRepository) localRootfsStorageSource {
	if isDroidspacesOfficialRootfsRepository(repository) {
		return localRootfsStorageSource{directory: rootfsDroidspacesOfficialDirectory, label: "Droidspaces Official"}
	}
	if isLinuxContainersRootfsRepository(repository) {
		return localRootfsStorageSource{directory: rootfsLinuxContainersDirectory, label: config.LinuxContainersRepositoryName}
	}

	label := strings.TrimSpace(repository.Name)
	if label == "" {
		if parsed, err := url.Parse(strings.TrimSpace(repository.URL)); err == nil {
			label = parsed.Hostname()
		}
	}
	if label == "" {
		label = "自定义镜像仓库"
	}
	return localRootfsStorageSource{
		directory: rootfsCustomRepositoryDirectory(repository),
		label:     label,
	}
}

func isDroidspacesOfficialRootfsRepository(repository config.RootfsRepository) bool {
	identity := rootfsRepositoryURLIdentity(repository.URL)
	if identity != "" {
		return identity == rootfsRepositoryURLIdentity(config.OfficialRootfsRepositoryURL)
	}
	return strings.EqualFold(strings.TrimSpace(repository.Name), "Droidspaces Official")
}

func isLinuxContainersRootfsRepository(repository config.RootfsRepository) bool {
	parsed, err := url.Parse(strings.TrimSpace(repository.URL))
	if err == nil && strings.EqualFold(parsed.Hostname(), "images.linuxcontainers.org") {
		return true
	}
	if config.IsLinuxContainersNJURepositoryURL(repository.URL) {
		return true
	}
	return config.IsLinuxContainersRepositoryName(repository.Name)
}

func rootfsCustomRepositoryDirectory(repository config.RootfsRepository) string {
	identity := rootfsRepositoryURLIdentity(repository.URL)
	if identity == "" {
		identity = strings.ToLower(strings.TrimSpace(repository.Name))
	}
	host := "source"
	if parsed, err := url.Parse(strings.TrimSpace(repository.URL)); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	component := rootfsStorageComponentUnsafe.ReplaceAllString(strings.ToLower(host), "-")
	component = strings.Trim(component, "-")
	if component == "" {
		component = "source"
	}
	if len(component) > 48 {
		component = component[:48]
	}
	sum := sha256.Sum256([]byte(identity))
	return rootfsRepositoryDirectoryPrefix + component + "-" + hex.EncodeToString(sum[:4])
}

func rootfsRepositoryURLIdentity(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" {
		return strings.ToLower(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.ToLower(parsed.String())
}

func sameRootfsStoragePath(first string, second string) bool {
	return first != "" && second != "" && filepath.Clean(first) == filepath.Clean(second)
}

func rootfsDirectChildName(parent string, child string) (string, bool) {
	if parent == "" || child == "" {
		return "", false
	}
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || strings.Contains(relative, string(os.PathSeparator)) {
		return "", false
	}
	return relative, true
}

func (s *Server) scanLocalRootfsDir(root string, kindOverride string, source string, skipNames map[string]bool, seen map[string]bool) ([]localRootfsItem, error) {
	return s.scanLocalRootfsDirWithVariant(root, kindOverride, source, skipNames, seen, "")
}

func (s *Server) scanLocalRootfsVariantDirs(root string, kindOverride string, source string, seen map[string]bool) ([]localRootfsItem, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	variantDirectories := make(map[string]bool)
	variants := make([]string, 0)
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || rootfsStorageVariantDirectory(name) != name {
			continue
		}
		variantDirectories[name] = true
		variants = append(variants, name)
	}

	items, err := s.scanLocalRootfsDirWithVariant(root, kindOverride, source, variantDirectories, seen, "")
	if err != nil {
		return nil, err
	}
	for _, variant := range variants {
		variantItems, err := s.scanLocalRootfsDirWithVariant(filepath.Join(root, variant), kindOverride, source, nil, seen, variant)
		if err != nil {
			return nil, err
		}
		items = append(items, variantItems...)
	}
	return items, nil
}

func (s *Server) scanLocalRootfsDirWithVariant(root string, kindOverride string, source string, skipNames map[string]bool, seen map[string]bool, variant string) ([]localRootfsItem, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var items []localRootfsItem
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || strings.HasSuffix(entry.Name(), ".part") || skipNames[entry.Name()] {
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
		cleanPath := filepath.Clean(path)
		if seen[cleanPath] {
			continue
		}
		seen[cleanPath] = true
		itemSource := source
		if source == "旧模板目录" {
			itemSource = legacyRootfsItemSource(entry.Name())
		}
		items = append(items, localRootfsItem{Name: entry.Name(), Path: path, Kind: kind, Size: info.Size(), Modified: info.ModTime().Unix(), Source: itemSource, Variant: variant})
	}
	return items, nil
}

func legacyRootfsItemSource(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "linux-containers"):
		return "lxc-image（旧目录）"
	case strings.Contains(lower, "droidspaces"):
		return "Droidspaces Official（旧目录）"
	default:
		return "旧模板目录"
	}
}

func (s *Server) createRootfsArchive(ctx context.Context, rootfsPath string, dest string, taskID string) error {
	info, err := os.Stat(rootfsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	total := info.Size()
	if info.IsDir() {
		if size, sizeErr := directorySize(rootfsPath); sizeErr == nil && size > 0 {
			total = size
		}
	}
	if total > 0 {
		s.updateTask(taskID, func(task *taskState) {
			task.Total = total
		})
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
	return s.copyRootfsDirectoryInto(ctx, source, dest)
}

func (s *Server) copyRootfsDirectoryInto(ctx context.Context, source string, dest string) error {
	busybox := shellQuote(s.busyboxPath())
	cmdText := fmt.Sprintf("cd %s && %s tar -cpf - . | (cd %s && %s tar -xpf -)", shellQuote(source), busybox, shellQuote(dest), busybox)
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
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
	return s.extractRootfsArchiveInto(ctx, archive, dest)
}

func (s *Server) extractRootfsArchiveInto(ctx context.Context, archive string, dest string) error {
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
	cmd := exec.CommandContext(ctx, s.shellPath(), "-c", cmdText)
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
	cmd := exec.CommandContext(ctx, s.shellPath(), scriptPath, rootfsDir)
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

func directorySize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	return total, err
}

func (s *Server) watchArchiveProgress(ctx context.Context, dest string, taskID string) func() {
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.updateArchiveProgress(dest, taskID)
			case <-done:
				s.updateArchiveProgress(dest, taskID)
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		close(done)
		<-finished
	}
}

func (s *Server) updateArchiveProgress(dest string, taskID string) {
	info, err := os.Stat(dest)
	if err != nil || info.Size() <= 0 {
		return
	}
	s.updateTask(taskID, func(task *taskState) {
		task.Downloaded = info.Size()
		if task.Total <= 0 {
			task.Total = info.Size()
		}
		if task.Total > 0 {
			percent := int(info.Size() * 100 / task.Total)
			if percent > 99 {
				percent = 99
			}
			if percent > task.Percent {
				task.Percent = percent
			}
		}
	})
}

func (s *Server) archiveDirectory(ctx context.Context, rootfsDir string, dest string, taskID string) error {
	busybox := s.busyboxPath()
	cmd := exec.CommandContext(ctx, busybox, "tar", "-czf", dest, "-C", rootfsDir, ".")
	cmd.Env = s.terminalEnv()
	stopProgress := s.watchArchiveProgress(ctx, dest, taskID)
	out, err := cmd.CombinedOutput()
	stopProgress()
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

func (s *Server) shellPath() string {
	for _, candidate := range []string{"/system/bin/sh", "/bin/sh", "/usr/bin/sh"} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return "sh"
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

func (s *Server) localRootfsFileAllowed(path string) bool {
	clean := filepath.Clean(path)
	items, err := s.localRootfsItems()
	if err != nil {
		return false
	}
	for _, item := range items {
		if filepath.Clean(item.Path) == clean && item.Kind != "directory" && !isSymlink(item.Path) {
			return true
		}
	}
	return false
}

func isSymlink(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode()&os.ModeSymlink != 0
}

func (s *Server) validateLocalRootfsFileForDelete(path string) error {
	if path == "" || hasConfigUnsafeChars(path) || !filepath.IsAbs(path) {
		return fmt.Errorf("invalid path")
	}
	if !s.localRootfsFileAllowed(path) {
		return os.ErrPermission
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return os.ErrNotExist
		}
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("directories cannot be deleted from this endpoint")
	}
	lower := strings.ToLower(path)
	if !isRootfsArchive(lower) && !strings.HasSuffix(lower, ".img") {
		return fmt.Errorf("only rootfs image or archive files can be deleted")
	}
	return nil
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

func (s *Server) runDroidspacesLogged(ctx context.Context, taskID string, args ...string) (cliCommandResult, error) {
	s.appendTaskLog(taskID, "$ "+s.droidspacesPath+" "+strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, s.droidspacesPath, args...)
	cmd.Env = append(s.terminalEnv(), "TERM=dumb")
	if s.workspace != "" {
		cmd.Dir = s.workspace
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return cliCommandResult{Args: append([]string{}, args...), ExitCode: 1, Output: err.Error()}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return cliCommandResult{Args: append([]string{}, args...), ExitCode: 1, Output: err.Error()}, err
	}
	if err := cmd.Start(); err != nil {
		return cliCommandResult{Args: append([]string{}, args...), ExitCode: 1, Output: err.Error()}, err
	}

	var outputMu sync.Mutex
	var output strings.Builder
	readPipe := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 4096), 1024*1024)
		for scanner.Scan() {
			line := ansiPattern.ReplaceAllString(scanner.Text(), "")
			s.appendTaskLog(taskID, line)
			outputMu.Lock()
			output.WriteString(line)
			output.WriteByte('\n')
			outputMu.Unlock()
		}
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); readPipe(stdout) }()
	go func() { defer wg.Done(); readPipe(stderr) }()
	err = cmd.Wait()
	wg.Wait()

	outputMu.Lock()
	text := output.String()
	outputMu.Unlock()
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

func (s *Server) runDroidspaces(ctx context.Context, args ...string) (cliCommandResult, error) {
	cmd := exec.CommandContext(ctx, s.droidspacesPath, args...)
	cmd.Env = append(s.terminalEnv(), "TERM=dumb")
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

func (s *Server) runtimeCoreVersion(ctx context.Context) string {
	s.coreVersionMu.Lock()
	defer s.coreVersionMu.Unlock()

	if !s.coreVersionCheckedAt.IsZero() && time.Since(s.coreVersionCheckedAt) < coreVersionCacheTTL {
		return s.detectedCoreVersion
	}

	result, err := s.runDroidspaces(ctx, "version")
	version := "unavailable"
	if err == nil {
		if detected := coreVersionFromOutput(result.Output); detected != "" {
			version = detected
		}
	}
	s.detectedCoreVersion = version
	s.coreVersionCheckedAt = time.Now()
	return version
}

func coreVersionFromOutput(output string) string {
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if coreVersionPattern.MatchString(line) {
			return line
		}
	}
	return ""
}

func (s *Server) invalidateCoreVersionCache() {
	s.coreVersionMu.Lock()
	s.detectedCoreVersion = ""
	s.coreVersionCheckedAt = time.Time{}
	s.coreVersionMu.Unlock()
}

func newCoreUpdateHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	platformhttp.ConfigureAndroidTransport(transport)
	return &http.Client{
		Timeout:   coreUpdateHTTPTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 4 {
				return fmt.Errorf("too many redirects while downloading the core release")
			}
			if !isTrustedCoreUpdateURL(req.URL) {
				return fmt.Errorf("untrusted redirect target %q", req.URL.Host)
			}
			return nil
		},
	}
}

func (s *Server) coreUpdateClient() *http.Client {
	if s.coreUpdateHTTPClient != nil {
		return s.coreUpdateHTTPClient
	}
	return newCoreUpdateHTTPClient()
}

func (s *Server) fetchLatestCoreRelease(ctx context.Context) (githubCoreRelease, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, officialCoreReleaseAPIURL, nil)
	if err != nil {
		return githubCoreRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "Droidspaces-WebUI")

	response, err := s.coreUpdateClient().Do(request)
	if err != nil {
		return githubCoreRelease{}, fmt.Errorf("fetch official Droidspaces release: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && !isOfficialCoreReleaseAPIURL(response.Request.URL.String()) {
		return githubCoreRelease{}, fmt.Errorf("official release API redirected to an untrusted endpoint")
	}
	if response.StatusCode != http.StatusOK {
		return githubCoreRelease{}, fmt.Errorf("official release API returned HTTP %d", response.StatusCode)
	}

	var release githubCoreRelease
	decoder := json.NewDecoder(io.LimitReader(response.Body, coreUpdateMaxMetadataBytes))
	if err := decoder.Decode(&release); err != nil {
		return githubCoreRelease{}, fmt.Errorf("decode official release metadata: %w", err)
	}
	if strings.TrimSpace(release.TagName) == "" || len(release.Assets) == 0 {
		return githubCoreRelease{}, fmt.Errorf("official release metadata is incomplete")
	}
	return release, nil
}

func isOfficialCoreReleaseAPIURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" &&
		strings.EqualFold(parsed.Hostname(), "api.github.com") && parsed.Path == "/repos/ravindu644/Droidspaces-OSS/releases/latest"
}

func isOfficialCoreReleaseAssetURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https" && parsed.User == nil && parsed.Port() == "" &&
		strings.EqualFold(parsed.Hostname(), "github.com") && strings.HasPrefix(parsed.Path, officialCoreReleaseDownloadPath)
}

func isTrustedCoreUpdateURL(parsed *url.URL) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.github.com":
		return parsed.Path == "/repos/ravindu644/Droidspaces-OSS/releases/latest"
	case "github.com":
		return strings.HasPrefix(parsed.Path, officialCoreReleaseDownloadPath)
	case "release-assets.githubusercontent.com", "objects.githubusercontent.com":
		return true
	default:
		return false
	}
}

func selectCoreUpdateAsset(release githubCoreRelease, architecture string) (githubCoreReleaseAsset, error) {
	var selected githubCoreReleaseAsset
	for _, asset := range release.Assets {
		if !isUniversalCoreArchiveName(asset.Name) {
			continue
		}
		if selected.Name != "" {
			return githubCoreReleaseAsset{}, fmt.Errorf("official release has multiple universal core archives")
		}
		if !isOfficialCoreReleaseAssetURL(asset.BrowserDownloadURL) {
			return githubCoreReleaseAsset{}, fmt.Errorf("official release archive URL is not trusted")
		}
		if _, err := parseGitHubSHA256Digest(asset.Digest); err != nil {
			return githubCoreReleaseAsset{}, fmt.Errorf("official release archive digest: %w", err)
		}
		if asset.Size <= 0 || asset.Size > coreUpdateMaxArchiveBytes {
			return githubCoreReleaseAsset{}, fmt.Errorf("official release archive has an invalid size")
		}
		selected = asset
	}
	if selected.Name == "" {
		return githubCoreReleaseAsset{}, fmt.Errorf("official release has no universal .tar.gz core archive for %s", architecture)
	}
	return selected, nil
}

func isUniversalCoreArchiveName(name string) bool {
	if name == "" || filepath.Base(name) != name || strings.Contains(name, `\`) {
		return false
	}
	lower := strings.ToLower(name)
	if !strings.HasSuffix(lower, ".tar.gz") {
		return false
	}
	base := strings.TrimSuffix(lower, ".tar.gz")
	return strings.HasPrefix(base, "droidspaces-v") || strings.HasPrefix(base, "droidspaces-universal-v")
}

func parseGitHubSHA256Digest(raw string) ([]byte, error) {
	algorithm, encoded, ok := strings.Cut(strings.TrimSpace(raw), ":")
	if !ok || !strings.EqualFold(algorithm, "sha256") {
		return nil, fmt.Errorf("must use sha256")
	}
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size {
		return nil, fmt.Errorf("is malformed")
	}
	return decoded, nil
}

func coreUpdateArchitecture(goarch string) (string, error) {
	switch goarch {
	case "arm64":
		return "aarch64", nil
	case "arm":
		return "armhf", nil
	case "amd64":
		return "x86_64", nil
	case "386":
		return "x86", nil
	case "riscv64":
		return "riscv64", nil
	default:
		return "", fmt.Errorf("unsupported core update architecture %q", goarch)
	}
}

var coreVersionPattern = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:[-+].*)?$`)

func coreUpdateAvailable(current string, latest string) (bool, bool) {
	currentParts, currentOK := parseCoreVersion(current)
	latestParts, latestOK := parseCoreVersion(latest)
	if !currentOK || !latestOK {
		return false, false
	}
	for index := range currentParts {
		if currentParts[index] == latestParts[index] {
			continue
		}
		return currentParts[index] < latestParts[index], true
	}
	return false, true
}

func parseCoreVersion(raw string) ([3]int64, bool) {
	match := coreVersionPattern.FindStringSubmatch(strings.TrimSpace(raw))
	if len(match) != 4 {
		return [3]int64{}, false
	}
	var values [3]int64
	for index := range values {
		value, err := strconv.ParseInt(match[index+1], 10, 64)
		if err != nil || value < 0 {
			return [3]int64{}, false
		}
		values[index] = value
	}
	return values, true
}

func (s *Server) runCoreUpdateTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	target, err := s.coreUpdateTarget()
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	s.appendTaskLog(taskID, "Fetching the latest official Droidspaces release metadata...")
	release, err := s.fetchLatestCoreRelease(ctx)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	architecture, err := coreUpdateArchitecture(runtime.GOARCH)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	asset, err := selectCoreUpdateAsset(release, architecture)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	s.updateTask(taskID, func(task *taskState) {
		task.Total = asset.Size
		task.Percent = 5
	})
	s.appendTaskLog(taskID, fmt.Sprintf("Downloading %s for %s...", asset.Name, architecture))
	archivePath, err := s.downloadCoreUpdateArchive(ctx, filepath.Dir(target.path), asset, taskID)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	defer os.Remove(archivePath)

	staged, err := os.CreateTemp(filepath.Dir(target.path), ".droidspaces-core-*")
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	stagedPath := staged.Name()
	defer os.Remove(stagedPath)
	s.appendTaskLog(taskID, "Validating archive layout and extracting the selected core binary...")
	if err := extractCoreBinaryFromArchive(archivePath, architecture, staged); err != nil {
		_ = staged.Close()
		s.failTask(taskID, err)
		return
	}
	if err := staged.Sync(); err != nil {
		_ = staged.Close()
		s.failTask(taskID, err)
		return
	}
	if err := staged.Close(); err != nil {
		s.failTask(taskID, err)
		return
	}
	if err := os.Chmod(stagedPath, target.mode.Perm()); err != nil {
		s.failTask(taskID, err)
		return
	}
	if config.IsAndroid() {
		if err := applyAndroidDaemonSELinuxLabel(ctx, stagedPath); err != nil {
			s.failTask(taskID, err)
			return
		}
	} else if err := applySELinuxLabel(stagedPath, target.selinuxLabel); err != nil {
		s.failTask(taskID, fmt.Errorf("preserve Droidspaces SELinux label: %w", err))
		return
	}

	s.updateTask(taskID, func(task *taskState) { task.Percent = 85 })
	s.appendTaskLog(taskID, "Atomically replacing the configured Droidspaces binary...")
	backupPath, err := replaceCoreBinary(target.path, stagedPath)
	if err != nil {
		s.failTask(taskID, err)
		return
	}
	s.invalidateCoreVersionCache()
	s.appendTaskLog(taskID, "Previous core preserved at "+backupPath)
	if notified, signalErr := s.signalCoreDaemonRefresh(); signalErr != nil {
		s.appendTaskLog(taskID, "Core updated, but daemon refresh signal was not sent: "+signalErr.Error())
	} else if notified {
		s.appendTaskLog(taskID, "Sent SIGUSR2 to droidspacesd to refresh the core.")
	} else {
		s.appendTaskLog(taskID, "No droidspacesd.pid found; the new core will be used on the next daemon start.")
	}
	s.completeTask(taskID, target.path, "")
}

func (s *Server) coreUpdateTarget() (coreUpdateTarget, error) {
	configuredPath := strings.TrimSpace(s.droidspacesPath)
	if configuredPath == "" || hasConfigUnsafeChars(configuredPath) {
		return coreUpdateTarget{}, fmt.Errorf("configured Droidspaces path is invalid")
	}
	targetPath, err := filepath.Abs(configuredPath)
	if err != nil {
		return coreUpdateTarget{}, err
	}
	info, err := os.Lstat(targetPath)
	if err != nil {
		return coreUpdateTarget{}, fmt.Errorf("inspect configured Droidspaces binary: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return coreUpdateTarget{}, fmt.Errorf("configured Droidspaces path must be a regular file")
	}
	if info.Mode().Perm()&0111 == 0 {
		return coreUpdateTarget{}, fmt.Errorf("configured Droidspaces binary is not executable")
	}
	parent, err := os.Stat(filepath.Dir(targetPath))
	if err != nil || !parent.IsDir() {
		return coreUpdateTarget{}, fmt.Errorf("configured Droidspaces directory is unavailable")
	}
	label, err := readSELinuxLabel(targetPath)
	if err != nil {
		return coreUpdateTarget{}, fmt.Errorf("read Droidspaces SELinux label: %w", err)
	}
	return coreUpdateTarget{path: targetPath, mode: info.Mode(), selinuxLabel: label}, nil
}

func (s *Server) downloadCoreUpdateArchive(ctx context.Context, destinationDir string, asset githubCoreReleaseAsset, taskID string) (string, error) {
	expectedDigest, err := parseGitHubSHA256Digest(asset.Digest)
	if err != nil {
		return "", err
	}
	if !isOfficialCoreReleaseAssetURL(asset.BrowserDownloadURL) {
		return "", fmt.Errorf("official release archive URL is not trusted")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.BrowserDownloadURL, nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("User-Agent", "Droidspaces-WebUI")
	response, err := s.coreUpdateClient().Do(request)
	if err != nil {
		return "", fmt.Errorf("download official core archive: %w", err)
	}
	defer response.Body.Close()
	if response.Request != nil && !isTrustedCoreUpdateURL(response.Request.URL) {
		return "", fmt.Errorf("official release archive redirected to an untrusted endpoint")
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("official release archive returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > coreUpdateMaxArchiveBytes {
		return "", fmt.Errorf("official release archive exceeds the size limit")
	}

	temporary, err := os.CreateTemp(destinationDir, ".droidspaces-release-*.tar.gz")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	keepTemporary := false
	defer func() {
		if !keepTemporary {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()

	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	var written int64
	for {
		read, readErr := response.Body.Read(buffer)
		if read > 0 {
			written += int64(read)
			if written > coreUpdateMaxArchiveBytes {
				return "", fmt.Errorf("official release archive exceeds the size limit")
			}
			if _, err := temporary.Write(buffer[:read]); err != nil {
				return "", err
			}
			if _, err := hash.Write(buffer[:read]); err != nil {
				return "", err
			}
			s.updateTask(taskID, func(task *taskState) {
				task.Downloaded = written
				task.Total = asset.Size
				if asset.Size > 0 {
					task.Percent = 5 + int(written*65/asset.Size)
					if task.Percent > 70 {
						task.Percent = 70
					}
				}
			})
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if written != asset.Size {
		return "", fmt.Errorf("official release archive size mismatch")
	}
	if !bytes.Equal(hash.Sum(nil), expectedDigest) {
		return "", fmt.Errorf("official release archive SHA-256 digest mismatch")
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	keepTemporary = true
	return temporaryPath, nil
}

func extractCoreBinaryFromArchive(archivePath string, architecture string, output *os.File) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	reader, err := gzip.NewReader(archive)
	if err != nil {
		return fmt.Errorf("open core archive gzip stream: %w", err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	if err := output.Truncate(0); err != nil {
		return err
	}
	if _, err := output.Seek(0, io.SeekStart); err != nil {
		return err
	}

	entries := 0
	found := false
	var unpacked int64
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read core archive: %w", err)
		}
		entries++
		if entries > coreUpdateMaxArchiveEntries {
			return fmt.Errorf("core archive has too many entries")
		}
		cleanName, err := safeCoreArchivePath(header.Name)
		if err != nil {
			return err
		}
		if header.Size < 0 || header.Size > coreUpdateMaxBinaryBytes {
			return fmt.Errorf("core archive entry %q exceeds the size limit", cleanName)
		}
		unpacked += header.Size
		if unpacked > coreUpdateMaxArchiveBytes {
			return fmt.Errorf("core archive exceeds the unpacked size limit")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return fmt.Errorf("core archive contains unsupported entry %q", cleanName)
		}
		if !isCoreBinaryArchiveEntry(cleanName, architecture) {
			continue
		}
		if found {
			return fmt.Errorf("core archive contains multiple %s binaries", architecture)
		}
		written, copyErr := io.CopyN(output, tarReader, header.Size)
		if copyErr != nil || written != header.Size {
			if copyErr == nil {
				copyErr = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("extract core binary: %w", copyErr)
		}
		found = true
	}
	if !found {
		return fmt.Errorf("core archive does not contain a %s/droidspaces binary", architecture)
	}
	return nil
}

func safeCoreArchivePath(name string) (string, error) {
	if name == "" || strings.Contains(name, `\`) || pathpkg.IsAbs(name) {
		return "", fmt.Errorf("core archive contains an unsafe path")
	}
	trimmedName := strings.TrimSuffix(name, "/")
	if trimmedName == "" {
		return "", fmt.Errorf("core archive contains an unsafe path %q", name)
	}
	for _, part := range strings.Split(trimmedName, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("core archive contains an unsafe path %q", name)
		}
	}
	cleanName := pathpkg.Clean(trimmedName)
	if cleanName == "." || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
		return "", fmt.Errorf("core archive contains an unsafe path %q", name)
	}
	return cleanName, nil
}

func isCoreBinaryArchiveEntry(name string, architecture string) bool {
	parts := strings.Split(name, "/")
	return len(parts) >= 3 && parts[len(parts)-2] == architecture && parts[len(parts)-1] == "droidspaces"
}

func replaceCoreBinary(targetPath string, stagedPath string) (string, error) {
	backupPath := targetPath + ".previous"
	staleBackupPath := ""
	if info, err := os.Lstat(backupPath); err == nil {
		if info.IsDir() {
			return "", fmt.Errorf("Droidspaces backup path is a directory")
		}
		staleBackupPath = backupPath + ".stale-" + newUUID()
		if err := os.Rename(backupPath, staleBackupPath); err != nil {
			return "", fmt.Errorf("stage existing Droidspaces backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect Droidspaces backup: %w", err)
	}
	restoreBackup := func() {
		if staleBackupPath != "" {
			_ = os.Remove(backupPath)
			_ = os.Rename(staleBackupPath, backupPath)
		}
	}

	if err := os.Link(targetPath, backupPath); err == nil {
		if err := os.Rename(stagedPath, targetPath); err != nil {
			_ = os.Remove(backupPath)
			restoreBackup()
			return "", fmt.Errorf("replace Droidspaces binary: %w", err)
		}
	} else {
		if _, backupErr := os.Lstat(backupPath); !errors.Is(backupErr, os.ErrNotExist) {
			restoreBackup()
			return "", fmt.Errorf("create Droidspaces backup: %w", err)
		}
		if renameErr := os.Rename(targetPath, backupPath); renameErr != nil {
			restoreBackup()
			return "", fmt.Errorf("create Droidspaces backup: %w", renameErr)
		}
		if renameErr := os.Rename(stagedPath, targetPath); renameErr != nil {
			rollbackErr := os.Rename(backupPath, targetPath)
			restoreBackup()
			if rollbackErr != nil {
				return "", fmt.Errorf("replace Droidspaces binary: %v; rollback failed: %v", renameErr, rollbackErr)
			}
			return "", fmt.Errorf("replace Droidspaces binary: %w", renameErr)
		}
	}
	if staleBackupPath != "" {
		_ = os.Remove(staleBackupPath)
	}
	return backupPath, nil
}

func readSELinuxLabel(path string) ([]byte, error) {
	buffer := make([]byte, 4096)
	length, err := syscall.Getxattr(path, "security.selinux", buffer)
	if err != nil {
		if errors.Is(err, syscall.ENODATA) || errors.Is(err, syscall.ENOTSUP) || errors.Is(err, syscall.EOPNOTSUPP) || errors.Is(err, syscall.ENOSYS) {
			return nil, nil
		}
		return nil, err
	}
	return append([]byte(nil), buffer[:length]...), nil
}

func applySELinuxLabel(path string, label []byte) error {
	if len(label) == 0 {
		return nil
	}
	return syscall.Setxattr(path, "security.selinux", label, 0)
}

func applyAndroidDaemonSELinuxLabel(ctx context.Context, path string) error {
	if !config.IsAndroid() {
		return nil
	}
	labelCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	const label = "u:object_r:droidspacesd_exec:s0"
	command := exec.CommandContext(labelCtx, "/system/bin/chcon", label, path)
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return fmt.Errorf("set Android Droidspaces SELinux label: %s", message)
		}
		return fmt.Errorf("set Android Droidspaces SELinux label: %w", err)
	}
	return nil
}

func (s *Server) signalCoreDaemonRefresh() (bool, error) {
	pidPath := filepath.Join(s.workspace, "droidspacesd.pid")
	info, err := os.Lstat(pidPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("droidspacesd pid path is not a regular file")
	}
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return false, err
	}
	fields := strings.Fields(string(data))
	if len(fields) != 1 {
		return false, fmt.Errorf("droidspacesd pid file is invalid")
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return false, fmt.Errorf("droidspacesd pid file is invalid")
	}
	commandLine, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false, fmt.Errorf("read droidspacesd command line: %w", err)
	}
	if !isConfiguredDroidspacesDaemon(commandLine, s.droidspacesPath) {
		return false, fmt.Errorf("droidspacesd pid file does not reference the configured daemon")
	}
	if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
		return false, err
	}
	return true, nil
}

func isConfiguredDroidspacesDaemon(commandLine []byte, droidspacesPath string) bool {
	parts := bytes.Split(bytes.TrimRight(commandLine, "\x00"), []byte{0})
	if len(parts) < 2 || len(parts[0]) == 0 {
		return false
	}
	if filepath.Base(string(parts[0])) != filepath.Base(strings.TrimSpace(droidspacesPath)) {
		return false
	}
	for _, part := range parts[1:] {
		if string(part) == "daemon" {
			return true
		}
	}
	return false
}

func (s *Server) containerConfigContent(name, hostname, rootfsPath, netMode string, req createContainerRequest) string {
	var b strings.Builder
	b.WriteString("# Droidspaces Container Configuration\n")
	b.WriteString("# Generated by Droidspaces WebUI\n\n")
	writeConfigLine(&b, "name", name)
	writeConfigLine(&b, "hostname", hostname)
	writeConfigLine(&b, "rootfs_path", rootfsPath)
	if strings.HasSuffix(strings.ToLower(rootfsPath), ".img") {
		writeConfigLine(&b, "use_sparse_image", "1")
		if sizeGB, err := rootfsImageSizeGB(req); err == nil {
			writeConfigLine(&b, "sparse_image_size_gb", strconv.Itoa(sizeGB))
		}
	} else {
		writeConfigLine(&b, "use_sparse_image", "0")
	}
	writeConfigLine(&b, "net_mode", netMode)
	writeConfigLine(&b, "disable_ipv6", boolFlag(req.DisableIPv6 || netModeForcesDisableIPv6(netMode)))
	writeConfigLine(&b, "enable_android_storage", boolFlag(req.AndroidStorage))
	writeConfigLine(&b, "enable_hw_access", boolFlag(req.HWAccess))
	writeConfigLine(&b, "enable_gpu_mode", boolFlag(req.GPUMode))
	writeConfigLine(&b, "enable_termux_x11", boolFlag(req.TermuxX11))
	if req.TermuxX11 && strings.TrimSpace(req.Tx11ExtraFlags) != "" {
		writeConfigLine(&b, "tx11_extra_flags", strings.TrimSpace(req.Tx11ExtraFlags))
	}
	writeConfigLine(&b, "enable_virgl", boolFlag(req.VirGL))
	if req.VirGL && strings.TrimSpace(req.VirGLExtraFlags) != "" {
		writeConfigLine(&b, "virgl_extra_flags", strings.TrimSpace(req.VirGLExtraFlags))
	}
	writeConfigLine(&b, "enable_pulseaudio", boolFlag(req.PulseAudio))
	writeConfigLine(&b, "selinux_permissive", boolFlag(req.SELinuxPermissive))
	privileged, _ := normalizePrivilegedMode(req.PrivilegedMode)
	writeConfigLine(&b, "allow_userns", boolFlag(req.AllowUserNS || privilegedDisablesDeadlock(privileged)))
	writeConfigLine(&b, "volatile_mode", boolFlag(req.VolatileMode))
	writeConfigLine(&b, "run_at_boot", boolFlag(req.RunAtBoot))
	if req.RunAtBoot && req.RunAtBootPriority > 0 {
		writeConfigLine(&b, "run_at_boot_priority", strconv.Itoa(req.RunAtBootPriority))
	}
	writeConfigLine(&b, "force_cgroupv1", boolFlag(req.ForceCgroupV1))
	if value, err := normalizeMemoryLimit(req.MemoryLimit); err != nil {
		// validateCreateContainerConfig reports invalid values before this path.
	} else if value != "" {
		writeConfigLine(&b, "memory_limit", value)
	}
	if quota, period, err := normalizeCPULimit(req.CPUs); err != nil {
		// validateCreateContainerConfig reports invalid values before this path.
	} else if quota != "" {
		writeConfigLine(&b, "cpu_quota", quota)
		writeConfigLine(&b, "cpu_period", period)
	}
	if value, err := normalizePidsLimit(req.PidsLimit); err != nil {
		// validateCreateContainerConfig reports invalid values before this path.
	} else if value != "" {
		writeConfigLine(&b, "pids_limit", value)
	}
	writeConfigLine(&b, "block_nested_ns", boolFlag(req.BlockNestedNS && !privilegedDisablesDeadlock(privileged)))
	if privileged != "" {
		writeConfigLine(&b, "privileged", privileged)
	}
	if strings.TrimSpace(req.DNSServers) != "" {
		writeConfigLine(&b, "dns_servers", strings.TrimSpace(req.DNSServers))
	}
	if netMode == "nat" {
		if strings.TrimSpace(req.StaticNATIP) != "" {
			writeConfigLine(&b, "static_nat_ip", strings.TrimSpace(req.StaticNATIP))
		}
		if strings.TrimSpace(req.PortForwards) != "" {
			writeConfigLine(&b, "port_forwards", strings.TrimSpace(req.PortForwards))
		}
	}
	if netMode == "gateway" {
		writeConfigLine(&b, "gateway_container", strings.TrimSpace(req.GatewayContainer))
		if strings.TrimSpace(req.GatewayNet) != "" {
			writeConfigLine(&b, "gateway_net", strings.TrimSpace(req.GatewayNet))
		}
		if strings.TrimSpace(req.GatewayLanIfname) != "" {
			writeConfigLine(&b, "gateway_lan_ifname", strings.TrimSpace(req.GatewayLanIfname))
		}
		if strings.TrimSpace(req.GatewayBridge) != "" {
			writeConfigLine(&b, "gateway_bridge", strings.TrimSpace(req.GatewayBridge))
		}
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

func (s *Server) persistWebConfig(update func(map[string]any)) error {
	if s.configPath == "" {
		return nil
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()

	data := map[string]any{}
	if raw, err := os.ReadFile(s.configPath); err == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return fmt.Errorf("parse config %s: %w", s.configPath, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read config %s: %w", s.configPath, err)
	}
	update(data)
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.MkdirAll(filepath.Dir(s.configPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(s.configPath, out, 0600)
}

func normalizeRootfsRepositories(repos []config.RootfsRepository) ([]config.RootfsRepository, error) {
	out := make([]config.RootfsRepository, 0, len(repos))
	seen := map[string]bool{}
	for _, repo := range repos {
		name := strings.TrimSpace(repo.Name)
		rawURL := strings.TrimSpace(repo.URL)
		if rawURL == "" {
			continue
		}
		parsed, err := url.Parse(rawURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return nil, fmt.Errorf("invalid repository url %q", rawURL)
		}
		if name == "" {
			name = parsed.Host
		}
		key := strings.ToLower(rawURL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, config.RootfsRepository{Name: name, URL: rawURL})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("at least one repository is required")
	}
	return config.NormalizeLinuxContainersRepositories(out), nil
}

func sanitizeRootfsUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, " ", "-")
	name = regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, ".-")
	return name
}

type portForwardConflictError struct {
	message string
}

func (e portForwardConflictError) Error() string {
	return e.message
}

func writePortForwardValidationError(w http.ResponseWriter, err error) {
	var conflict portForwardConflictError
	if errors.As(err, &conflict) {
		writeJSON(w, http.StatusConflict, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
}

func (s *Server) reserveCreateNATIP(target string, req *createContainerRequest) (func(), error) {
	if req == nil || strings.ToLower(strings.TrimSpace(req.NetMode)) != "nat" {
		return func() {}, nil
	}
	s.natIPMu.Lock()
	defer s.natIPMu.Unlock()
	if s.natIPReservations == nil {
		s.natIPReservations = map[string]string{}
	}
	if strings.TrimSpace(req.StaticNATIP) == "" {
		ip, err := s.nextNATIPForDefaultThirdOctetLocked(target)
		if err != nil {
			return nil, err
		}
		req.StaticNATIP = ip
	}
	if err := validateStaticNATIP(req.StaticNATIP); err != nil {
		return nil, err
	}
	if err := s.ensureNATIPAvailableLocked(target, strings.TrimSpace(req.StaticNATIP)); err != nil {
		return nil, err
	}
	s.natIPReservations[target] = strings.TrimSpace(req.StaticNATIP)
	return func() {
		s.natIPMu.Lock()
		delete(s.natIPReservations, target)
		s.natIPMu.Unlock()
	}, nil
}

func (s *Server) reserveUpdateNATIP(target string, netMode string, updates map[string]string) (func(), error) {
	if strings.ToLower(strings.TrimSpace(netMode)) != "nat" {
		return func() {}, nil
	}
	ip, ok := updates["static_nat_ip"]
	if !ok || strings.TrimSpace(ip) == "" {
		return func() {}, nil
	}
	ip = strings.TrimSpace(ip)
	if err := validateStaticNATIP(ip); err != nil {
		return nil, err
	}
	s.natIPMu.Lock()
	defer s.natIPMu.Unlock()
	if s.natIPReservations == nil {
		s.natIPReservations = map[string]string{}
	}
	if err := s.ensureNATIPAvailableLocked(target, ip); err != nil {
		return nil, err
	}
	s.natIPReservations[target] = ip
	return func() {
		s.natIPMu.Lock()
		delete(s.natIPReservations, target)
		s.natIPMu.Unlock()
	}, nil
}

func (s *Server) nextNATIPForDefaultThirdOctetLocked(target string) (string, error) {
	third := s.defaultNATThirdOctet
	if third <= 0 {
		third = config.DefaultNATThirdOctet
	}
	if third < 1 || third > 254 {
		return "", fmt.Errorf("defaultNatThirdOctet must be between 1 and 254")
	}
	used := map[int]bool{}
	s.collectUsedNATFourthOctetsLocked(target, third, used)
	next := 1
	for value := range used {
		if value >= next {
			next = value + 1
		}
	}
	if next <= 254 {
		return fmt.Sprintf("172.28.%d.%d", third, next), nil
	}
	return "", fmt.Errorf("NAT third octet %d has no free fourth octet", third)
}

func (s *Server) collectUsedNATFourthOctetsLocked(target string, third int, used map[int]bool) {
	snap, err := workspace.ReadSnapshot(s.workspace, true)
	if err == nil {
		for _, container := range snap.Containers {
			if container.Name == target || container.UUID == target {
				continue
			}
			if o3, o4, ok := parseStaticNATIPParts(container.NATIP); ok && o3 == third {
				used[o4] = true
			}
		}
	}
	for owner, ip := range s.natIPReservations {
		if owner == target {
			continue
		}
		if o3, o4, ok := parseStaticNATIPParts(ip); ok && o3 == third {
			used[o4] = true
		}
	}
}

func (s *Server) ensureNATIPAvailableLocked(target string, ip string) error {
	if ip == "" {
		return nil
	}
	snap, err := workspace.ReadSnapshot(s.workspace, true)
	if err != nil {
		return err
	}
	for _, container := range snap.Containers {
		if container.Name == target || container.UUID == target {
			continue
		}
		if strings.TrimSpace(container.NATIP) == ip {
			return fmt.Errorf("staticNatIp %s is already assigned to %s", ip, container.Name)
		}
	}
	for owner, reserved := range s.natIPReservations {
		if owner == target {
			continue
		}
		if strings.TrimSpace(reserved) == ip {
			return fmt.Errorf("staticNatIp %s is already reserved by %s", ip, owner)
		}
	}
	return nil
}

func (s *Server) reservePortForwards(target string, spec string, netMode string) (func(), error) {
	if strings.ToLower(strings.TrimSpace(netMode)) != "nat" {
		if strings.TrimSpace(spec) != "" {
			return nil, fmt.Errorf("portForwards is only valid when netMode is nat")
		}
		return func() {}, nil
	}
	ports, err := parsePortForwardsStrict(spec)
	if err != nil {
		return nil, err
	}
	if len(ports) == 0 {
		return func() {}, nil
	}
	s.portForwardMu.Lock()
	defer s.portForwardMu.Unlock()
	if s.portForwardReservations == nil {
		s.portForwardReservations = map[string][]socketd.Port{}
	}
	if err := s.ensurePortForwardsAvailableLocked(target, ports); err != nil {
		return nil, err
	}
	s.portForwardReservations[target] = ports
	return func() {
		s.portForwardMu.Lock()
		delete(s.portForwardReservations, target)
		s.portForwardMu.Unlock()
	}, nil
}

func (s *Server) ensurePortForwardsAvailableLocked(target string, requested []socketd.Port) error {
	if err := ensureNoPortForwardOverlap("request", requested); err != nil {
		return err
	}
	snap, err := workspace.ReadSnapshot(s.workspace, true)
	if err != nil {
		return err
	}
	for _, container := range snap.Containers {
		if container.Name == target || container.UUID == target {
			continue
		}
		if err := checkHostPortConflict(container.Name, requested, container.Ports); err != nil {
			return err
		}
	}
	for owner, reserved := range s.portForwardReservations {
		if owner == target {
			continue
		}
		if err := checkHostPortConflict(owner, requested, reserved); err != nil {
			return err
		}
	}
	return nil
}

func parsePortForwardsStrict(value string) ([]socketd.Port, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	items := strings.Split(value, ",")
	ports := make([]socketd.Port, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		proto := "tcp"
		if left, right, ok := strings.Cut(item, "/"); ok {
			item = strings.TrimSpace(left)
			var err error
			proto, err = normalizePortForwardProto(right)
			if err != nil {
				return nil, err
			}
		}
		if item == "" {
			return nil, fmt.Errorf("invalid portForwards entry %q", raw)
		}
		hostSide := item
		containerSide := item
		if left, right, ok := strings.Cut(item, ":"); ok {
			hostSide = strings.TrimSpace(left)
			containerSide = strings.TrimSpace(right)
		}
		hostStart, hostEnd, hostOK := parsePortRange(hostSide)
		containerStart, containerEnd, containerOK := parsePortRange(containerSide)
		if !hostOK {
			return nil, fmt.Errorf("invalid host port range %q", hostSide)
		}
		if !containerOK {
			return nil, fmt.Errorf("invalid container port range %q", containerSide)
		}
		if portRangeWidth(hostStart, hostEnd) != portRangeWidth(containerStart, containerEnd) {
			return nil, fmt.Errorf("portForwards range width mismatch for %q", raw)
		}
		if len(ports) >= 32 {
			return nil, fmt.Errorf("too many portForwards; max 32")
		}
		ports = append(ports, socketd.Port{HostPort: hostStart, HostPortEnd: hostEnd, ContainerPort: containerStart, ContainerPortEnd: containerEnd, Protocol: proto})
	}
	if err := ensureNoPortForwardOverlap("request", ports); err != nil {
		return nil, err
	}
	return ports, nil
}

func normalizePortForwardProto(value string) (string, error) {
	proto := strings.ToLower(strings.TrimSpace(value))
	if proto == "" {
		return "tcp", nil
	}
	switch proto {
	case "tcp", "udp":
		return proto, nil
	default:
		return "", fmt.Errorf("unsupported portForwards protocol %q", value)
	}
}

func ensureNoPortForwardOverlap(owner string, ports []socketd.Port) error {
	for i := 0; i < len(ports); i++ {
		for j := i + 1; j < len(ports); j++ {
			if portForwardProto(ports[i]) != portForwardProto(ports[j]) {
				continue
			}
			if portForwardHostOverlap(ports[i], ports[j]) {
				return fmt.Errorf("portForwards host port %s/%s overlaps another rule in %s", describeHostPortRange(ports[i]), portForwardProto(ports[i]), owner)
			}
			if portForwardContainerOverlap(ports[i], ports[j]) {
				return fmt.Errorf("portForwards container port %s/%s overlaps another rule in %s", describeContainerPortRange(ports[i]), portForwardProto(ports[i]), owner)
			}
		}
	}
	return nil
}

func checkHostPortConflict(owner string, requested []socketd.Port, existing []socketd.Port) error {
	for _, req := range requested {
		for _, ex := range existing {
			if portForwardProto(req) != portForwardProto(ex) {
				continue
			}
			if portForwardHostOverlap(req, ex) {
				return portForwardConflictError{message: fmt.Sprintf("host port %s/%s conflicts with container %s using %s/%s", describeHostPortRange(req), portForwardProto(req), owner, describeHostPortRange(ex), portForwardProto(ex))}
			}
		}
	}
	return nil
}

func portForwardProto(port socketd.Port) string {
	proto := strings.ToLower(strings.TrimSpace(port.Protocol))
	if proto == "" {
		return "tcp"
	}
	return proto
}

func portForwardHostOverlap(a socketd.Port, b socketd.Port) bool {
	aStart, aEnd := hostPortRange(a)
	bStart, bEnd := hostPortRange(b)
	return aStart <= bEnd && bStart <= aEnd
}

func portForwardContainerOverlap(a socketd.Port, b socketd.Port) bool {
	aStart, aEnd := containerPortRange(a)
	bStart, bEnd := containerPortRange(b)
	return aStart <= bEnd && bStart <= aEnd
}

func hostPortRange(port socketd.Port) (uint16, uint16) {
	end := port.HostPortEnd
	if end == 0 {
		end = port.HostPort
	}
	return port.HostPort, end
}

func containerPortRange(port socketd.Port) (uint16, uint16) {
	end := port.ContainerPortEnd
	if end == 0 {
		end = port.ContainerPort
	}
	return port.ContainerPort, end
}

func portRangeWidth(start uint16, end uint16) int {
	if end == 0 {
		end = start
	}
	return int(end - start)
}

func describeHostPortRange(port socketd.Port) string {
	start, end := hostPortRange(port)
	return describePortRange(start, end)
}

func describeContainerPortRange(port socketd.Port) string {
	start, end := containerPortRange(port)
	return describePortRange(start, end)
}

func describePortRange(start uint16, end uint16) string {
	if end == 0 || end == start {
		return strconv.Itoa(int(start))
	}
	return fmt.Sprintf("%d-%d", start, end)
}

func (s *Server) validateCreateContainerConfig(name string, netMode string, req createContainerRequest) error {
	if req.RunAtBootPriority < 0 || req.RunAtBootPriority > 10000 {
		return fmt.Errorf("runAtBootPriority must be between 0 and 10000")
	}
	if netMode == "nat" && strings.TrimSpace(req.StaticNATIP) != "" {
		if err := validateStaticNATIP(req.StaticNATIP); err != nil {
			return err
		}
	}
	if netMode == "gateway" {
		if err := s.validateGatewayConfig(name, req.GatewayContainer, req.GatewayNet, req.GatewayLanIfname, req.GatewayBridge); err != nil {
			return err
		}
	}
	if _, err := normalizePrivilegedMode(req.PrivilegedMode); err != nil {
		return err
	}
	if _, err := normalizeMemoryLimit(req.MemoryLimit); err != nil {
		return err
	}
	if _, _, err := normalizeCPULimit(req.CPUs); err != nil {
		return err
	}
	if _, err := normalizePidsLimit(req.PidsLimit); err != nil {
		return err
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func validateStaticNATIP(value string) error {
	ip := net.ParseIP(strings.TrimSpace(value))
	if ip == nil || ip.To4() == nil || strings.Contains(value, "/") {
		return fmt.Errorf("staticNatIp must be a plain IPv4 address")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 4 || parts[0] != "172" || parts[1] != "28" {
		return fmt.Errorf("staticNatIp must be inside 172.28.0.0/16")
	}
	third, err3 := strconv.Atoi(parts[2])
	fourth, err4 := strconv.Atoi(parts[3])
	if err3 != nil || third < 1 || third > 254 {
		return fmt.Errorf("staticNatIp must be 172.28.X.Y with X in 1..254")
	}
	if err4 != nil || fourth < 1 || fourth > 254 {
		return fmt.Errorf("staticNatIp must be 172.28.X.Y with Y in 1..254")
	}
	return nil
}

func parseStaticNATIPParts(value string) (int, int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 4 || parts[0] != "172" || parts[1] != "28" {
		return 0, 0, false
	}
	third, err3 := strconv.Atoi(parts[2])
	fourth, err4 := strconv.Atoi(parts[3])
	if err3 != nil || err4 != nil {
		return 0, 0, false
	}
	if third < 1 || third > 254 || fourth < 1 || fourth > 254 {
		return 0, 0, false
	}
	return third, fourth, true
}

func normalizePrivilegedMode(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "none" {
		return "", nil
	}
	allowed := map[string]bool{
		"full":           true,
		"nomask":         true,
		"nocaps":         true,
		"noseccomp":      true,
		"shared":         true,
		"unfiltered-dev": true,
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' })
	seen := map[string]bool{}
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !allowed[part] {
			return "", fmt.Errorf("unsupported privileged tag %q", part)
		}
		if part == "full" {
			return "full", nil
		}
		if seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return strings.Join(out, ","), nil
}

func privilegedDisablesDeadlock(value string) bool {
	value = strings.ToLower(value)
	return value == "full" || strings.Contains(","+value+",", ",noseccomp,")
}

func normalizeMemoryLimit(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "unlimited") || strings.EqualFold(value, "none") || value == "0" {
		return "", nil
	}
	bytes, err := parseSizeBytes(value)
	if err != nil {
		return "", fmt.Errorf("memoryLimit must be a positive size, for example 512M or 2G")
	}
	if bytes < 4*1024*1024 {
		return "", fmt.Errorf("memoryLimit must be at least 4 MiB")
	}
	return strconv.FormatInt(bytes, 10), nil
}

func normalizeCPULimit(value string) (string, string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "0" || value == "unlimited" || value == "none" {
		return "", "", nil
	}
	cpus, err := strconv.ParseFloat(value, 64)
	if err != nil || cpus <= 0 {
		return "", "", fmt.Errorf("cpus must be a positive number, for example 0.5 or 2")
	}
	if cpus > 1024 {
		return "", "", fmt.Errorf("cpus is too large")
	}
	period := int64(100000)
	quota := int64(cpus * float64(period))
	if quota < 1000 {
		quota = 1000
	}
	return strconv.FormatInt(quota, 10), strconv.FormatInt(period, 10), nil
}

func normalizePidsLimit(value string) (string, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" || value == "0" || value == "unlimited" || value == "none" {
		return "", nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("pidsLimit must be a positive integer")
	}
	if n > 4194304 {
		return "", fmt.Errorf("pidsLimit is too large")
	}
	return strconv.FormatInt(n, 10), nil
}

func parseSizeBytes(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty size")
	}
	re := regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)([kmgt]?i?b?|bytes?)?$`)
	m := re.FindStringSubmatch(value)
	if m == nil {
		return 0, fmt.Errorf("invalid size")
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid size")
	}
	unit := strings.ToLower(m[2])
	unit = strings.TrimSuffix(unit, "bytes")
	unit = strings.TrimSuffix(unit, "byte")
	unit = strings.TrimSuffix(unit, "b")
	unit = strings.TrimSuffix(unit, "i")
	mult := float64(1)
	switch unit {
	case "":
		mult = 1
	case "k":
		mult = 1024
	case "m":
		mult = 1024 * 1024
	case "g":
		mult = 1024 * 1024 * 1024
	case "t":
		mult = 1024 * 1024 * 1024 * 1024
	default:
		return 0, fmt.Errorf("invalid size unit")
	}
	bytes := n * mult
	if bytes > float64(1<<62) {
		return 0, fmt.Errorf("size too large")
	}
	return int64(bytes), nil
}

func parseConfigInt64(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func formatCPUs(quota int64, period int64) string {
	if quota <= 0 {
		return ""
	}
	if period <= 0 {
		period = 100000
	}
	cpus := float64(quota) / float64(period)
	text := strconv.FormatFloat(cpus, 'f', 2, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	return text
}

const gatewayIFNameMax = 15

var gatewayNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func effectiveGatewayNet(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "lan"
	}
	return value
}

func effectiveGatewayIface(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "eth1"
	}
	return value
}

func effectiveGatewayBridge(netName string, bridge string) string {
	bridge = strings.TrimSpace(bridge)
	if bridge != "" {
		return bridge
	}
	clean := make([]rune, 0, 9)
	for _, r := range effectiveGatewayNet(netName) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			clean = append(clean, r)
			if len(clean) == 9 {
				break
			}
		}
	}
	if len(clean) == 0 {
		return "ds-lan"
	}
	return "ds-" + string(clean)
}

func validateGatewayNameField(field string, value string, maxLen int) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if maxLen > 0 && len(value) > maxLen {
		return fmt.Errorf("%s is too long; max %d characters", field, maxLen)
	}
	if !gatewayNamePattern.MatchString(value) {
		return fmt.Errorf("%s can only contain letters, digits, _ or -", field)
	}
	return nil
}

func (s *Server) validateGatewayConfig(selfName string, gatewayContainer string, netName string, iface string, bridge string) error {
	selfName = strings.TrimSpace(selfName)
	gatewayContainer = strings.TrimSpace(gatewayContainer)
	if gatewayContainer == "" {
		return fmt.Errorf("gatewayContainer is required for gateway mode")
	}
	if gatewayContainer == selfName {
		return fmt.Errorf("a container cannot use itself as gateway")
	}
	if err := validateGatewayNameField("gatewayNet", netName, 0); err != nil {
		return err
	}
	if err := validateGatewayNameField("gatewayLanIfname", iface, gatewayIFNameMax); err != nil {
		return err
	}
	if err := validateGatewayNameField("gatewayBridge", bridge, gatewayIFNameMax); err != nil {
		return err
	}

	snap, err := workspace.ReadSnapshot(s.workspace, true)
	if err != nil {
		return err
	}
	installed := map[string]bool{}
	for _, c := range snap.Containers {
		installed[c.Name] = true
	}
	if !installed[gatewayContainer] {
		return fmt.Errorf("gateway container %q is not installed", gatewayContainer)
	}

	myBridge := effectiveGatewayBridge(netName, bridge)
	myNet := effectiveGatewayNet(netName)
	myIface := effectiveGatewayIface(iface)
	for _, c := range snap.Containers {
		if c.Name == selfName || c.Name == "" {
			continue
		}
		configPath, ok := s.containerConfigPath(c.Name)
		if !ok {
			continue
		}
		values := readContainerConfigValues(configPath)
		mode := strings.TrimSpace(values["net_mode"])
		if mode == "" {
			mode = c.NetMode
		}
		if mode != "gateway" && mode != "delegated-gateway" {
			continue
		}
		otherGateway := strings.TrimSpace(values["gateway_container"])
		if otherGateway == "" {
			continue
		}
		otherNet := effectiveGatewayNet(values["gateway_net"])
		otherIface := effectiveGatewayIface(values["gateway_lan_ifname"])
		otherBridge := effectiveGatewayBridge(values["gateway_net"], values["gateway_bridge"])
		if otherBridge == myBridge && (otherGateway != gatewayContainer || otherNet != myNet) {
			return fmt.Errorf("gatewayBridge %s is already used by %q; choose a different gatewayNet or bridge", myBridge, c.Name)
		}
		if otherGateway == gatewayContainer && otherBridge != myBridge && otherIface == myIface {
			return fmt.Errorf("gatewayLanIfname %s is already used by another segment (%q)", myIface, c.Name)
		}
	}
	return nil
}

func (s *Server) validateGatewayUpdate(target string, requestedNetMode string, updates map[string]string) error {
	configPath, ok := s.containerConfigPath(target)
	values := map[string]string{}
	if ok {
		values = readContainerConfigValues(configPath)
	}
	for key, value := range updates {
		values[key] = value
	}
	mode := strings.TrimSpace(requestedNetMode)
	if mode == "" {
		mode = strings.TrimSpace(values["net_mode"])
	}
	if mode == "" || mode == "unknown" {
		mode = "host"
	}
	if mode != "gateway" && mode != "delegated-gateway" {
		return nil
	}
	return s.validateGatewayConfig(target, values["gateway_container"], values["gateway_net"], values["gateway_lan_ifname"], values["gateway_bridge"])
}

const requirementsTermuxSetup = `pkg update
pkg install tsu toybox e2fsprogs
# Droidspaces core still requires a root provider and Android kernel namespace/cgroup support.`

const kernelConfigNonGKI = `CONFIG_NAMESPACES=y
CONFIG_UTS_NS=y
CONFIG_IPC_NS=y
CONFIG_PID_NS=y
CONFIG_NET_NS=y
CONFIG_USER_NS=y
CONFIG_CGROUPS=y
CONFIG_CGROUP_FREEZER=y
CONFIG_CGROUP_DEVICE=y
CONFIG_CGROUP_CPUACCT=y
CONFIG_CGROUP_SCHED=y
CONFIG_CPUSETS=y
CONFIG_MEMCG=y
CONFIG_DEVTMPFS=y
CONFIG_VETH=y
CONFIG_BRIDGE=y
CONFIG_NETFILTER=y
CONFIG_NF_NAT=y
CONFIG_IP_NF_IPTABLES=y
CONFIG_IP_NF_NAT=y
CONFIG_OVERLAY_FS=y
CONFIG_SECCOMP=y
CONFIG_SECCOMP_FILTER=y`

const kernelConfigGKI = `CONFIG_ANDROID=y
CONFIG_NAMESPACES=y
CONFIG_NET_NS=y
CONFIG_PID_NS=y
CONFIG_IPC_NS=y
CONFIG_UTS_NS=y
CONFIG_USER_NS=y
CONFIG_CGROUPS=y
CONFIG_CGROUP_BPF=y
CONFIG_CGROUP_FREEZER=y
CONFIG_CGROUP_PIDS=y
CONFIG_MEMCG=y
CONFIG_VETH=y
CONFIG_BRIDGE=y
CONFIG_NF_TABLES=y
CONFIG_NETFILTER_XT_TARGET_MASQUERADE=y
CONFIG_OVERLAY_FS=y
CONFIG_SECCOMP=y
CONFIG_SECCOMP_FILTER=y`

func (s *Server) handleDiagnosticsRequirements(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{
			"termuxSetup":     requirementsTermuxSetup,
			"nonGKIConfig":    kernelConfigNonGKI,
			"gkiConfig":       kernelConfigGKI,
			"checkCommand":    s.droidspacesPath + " check",
			"droidspacesPath": s.droidspacesPath,
		})
	case http.MethodPost:
		task := s.newTask("requirements-check", "droidspaces check")
		s.updateTask(task.ID, func(t *taskState) { t.Status = "running" })
		go s.runRequirementsTask(task.ID)
		task, _ = s.getTask(task.ID)
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) runRequirementsTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := s.runDroidspacesLogged(ctx, taskID, "check")
	if err != nil {
		s.failTask(taskID, fmt.Errorf("requirements check failed: %v\n%s", err, result.Output))
		return
	}
	s.completeTask(taskID, "", "")
}

func (s *Server) handleDiagnosticsBugreport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	task := s.newTask("bugreport", "diagnostics")
	s.updateTask(task.ID, func(t *taskState) { t.Status = "running" })
	go s.runBugreportTask(task.ID)
	task, _ = s.getTask(task.ID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "taskId": task.ID, "task": task})
}

func (s *Server) runBugreportTask(taskID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	outDir := filepath.Join(s.workspace, "Bugreports")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		s.failTask(taskID, err)
		return
	}
	dest := filepath.Join(outDir, "droidspaces-bugreport-"+time.Now().Format("20060102-150405")+".txt")
	var b strings.Builder
	writeSection := func(title string, body string) {
		b.WriteString("\n## ")
		b.WriteString(title)
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(body))
		b.WriteString("\n")
	}
	s.appendTaskLog(taskID, "Collecting WebUI and Droidspaces diagnostics...")
	writeSection("WebUI", fmt.Sprintf("time=%s\nmode=%s\nworkspace=%s\ndroidspaces=%s\ncore=%s\ngo=%s/%s %s", time.Now().Format(time.RFC3339), s.mode, s.workspace, s.droidspacesPath, s.corePath, runtime.GOOS, runtime.GOARCH, runtime.Version()))
	if data, err := os.ReadFile("/proc/version"); err == nil {
		writeSection("Kernel", string(data))
	}
	for _, item := range []struct {
		title string
		args  []string
	}{
		{"Droidspaces Version", []string{"--version"}},
		{"Droidspaces Check", []string{"check"}},
		{"Droidspaces Show", []string{"show"}},
	} {
		s.appendTaskLog(taskID, "$ "+s.droidspacesPath+" "+strings.Join(item.args, " "))
		result, err := s.runDroidspaces(ctx, item.args...)
		if err != nil {
			writeSection(item.title, fmt.Sprintf("%v\n%s", err, result.Output))
		} else {
			writeSection(item.title, result.Output)
		}
	}
	if err := os.WriteFile(dest, []byte(strings.TrimLeft(b.String(), "\n")), 0644); err != nil {
		s.failTask(taskID, err)
		return
	}
	s.appendTaskLog(taskID, "Bugreport written: "+dest)
	s.completeTask(taskID, dest, "/api/downloads/"+taskID)
}

func (s *Server) handleBackendDiagnostics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"socketdEnabled": s.socketdEnabled,
		"socketName":     "droidspaces-socketd-backend",
		"errors":         s.backendDiagnosticLog(),
	})
}

// handleWebUILog returns a bounded tail of the WebUI's own process log. The
// deployment launcher writes stdout and stderr to this fixed workspace path.
func (s *Server) handleWebUILog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
		return
	}

	tail, err := webUILogTailLimit(r.URL.Query().Get("tail"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiError{Error: err.Error()})
		return
	}

	response, err := s.readWebUILogTail(tail)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, errUnsafeWebUILog) {
			status = http.StatusForbidden
		}
		writeJSON(w, status, apiError{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, response)
}

var errUnsafeWebUILog = errors.New("webui log is not a regular file")

func webUILogTailLimit(raw string) (int, error) {
	if raw == "" {
		return defaultWebUILogTailLines, nil
	}
	tail, err := strconv.Atoi(raw)
	if err != nil || tail < 1 || tail > maxWebUILogTailLines {
		return 0, fmt.Errorf("tail must be between 1 and %d lines", maxWebUILogTailLines)
	}
	return tail, nil
}

func (s *Server) webUILogPath() string {
	return filepath.Join(s.workspace, "Logs", "webui.log")
}

func (s *Server) readWebUILogTail(tail int) (webUILogResponse, error) {
	response := webUILogResponse{
		Path:  filepath.ToSlash(filepath.Join("Logs", "webui.log")),
		Tail:  tail,
		Lines: []string{},
	}
	path := s.webUILogPath()
	beforeOpen, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return response, nil
	}
	if err != nil {
		return response, fmt.Errorf("inspect webui log: %w", err)
	}
	if !beforeOpen.Mode().IsRegular() {
		return response, errUnsafeWebUILog
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return response, nil
	}
	if err != nil {
		return response, fmt.Errorf("open webui log: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return response, fmt.Errorf("stat webui log: %w", err)
	}
	if !info.Mode().IsRegular() || !os.SameFile(beforeOpen, info) {
		return response, errUnsafeWebUILog
	}

	response.Exists = true
	response.SizeBytes = info.Size()
	response.ModifiedAt = info.ModTime().Unix()
	start := int64(0)
	if info.Size() > maxWebUILogReadBytes {
		start = info.Size() - maxWebUILogReadBytes
		response.Truncated = true
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return response, fmt.Errorf("seek webui log: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxWebUILogReadBytes))
	if err != nil {
		return response, fmt.Errorf("read webui log: %w", err)
	}
	if start > 0 {
		// The first chunk begins part-way through a line. Drop it so the UI
		// does not present a misleading partial log entry.
		if newline := bytes.IndexByte(data, '\n'); newline >= 0 {
			data = data[newline+1:]
		} else {
			data = nil
		}
	}

	lines := splitLogLines(data)
	if len(lines) > tail {
		lines = lines[len(lines)-tail:]
		response.Truncated = true
	}
	response.ReturnedLines = len(lines)
	response.Lines = lines
	return response, nil
}

func splitLogLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	parts := bytes.Split(data, []byte{'\n'})
	if len(parts) > 0 && len(parts[len(parts)-1]) == 0 {
		parts = parts[:len(parts)-1]
	}
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		lines = append(lines, strings.TrimSuffix(string(part), "\r"))
	}
	return lines
}

func (s *Server) handleDiagnosticsSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.diagnosticsSettings())
	case http.MethodPut, http.MethodPost:
		var req diagnosticsSettingsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, apiError{Error: "invalid json body"})
			return
		}
		if req.DaemonMode != nil {
			if err := s.setDaemonMode(*req.DaemonMode); err != nil {
				writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
				return
			}
		}
		if req.SymlinkEnabled != nil {
			if err := s.setSymlinkEnabled(*req.SymlinkEnabled); err != nil {
				writeJSON(w, http.StatusBadGateway, apiError{Error: err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, s.diagnosticsSettings())
	default:
		writeJSON(w, http.StatusMethodNotAllowed, apiError{Error: "method not allowed"})
	}
}

func (s *Server) diagnosticsSettings() map[string]any {
	daemonFile := s.daemonModeFile()
	_, daemonErr := os.Stat(daemonFile)
	link := "/data/adb/modules/droidspaces/system/bin/droidspaces"
	linkTarget := ""
	linkEnabled := false
	if target, err := os.Readlink(link); err == nil {
		linkEnabled = true
		linkTarget = target
	}
	return map[string]any{
		"daemonMode":      daemonErr == nil,
		"daemonModeFile":  daemonFile,
		"symlinkEnabled":  linkEnabled,
		"symlinkPath":     link,
		"symlinkTarget":   linkTarget,
		"droidspacesPath": s.droidspacesPath,
		"workspace":       s.workspace,
	}
}

func (s *Server) daemonModeFile() string {
	if s.workspace != "" {
		return filepath.Join(s.workspace, ".daemon_mode")
	}
	return "/data/local/Droidspaces/.daemon_mode"
}

func (s *Server) setDaemonMode(enabled bool) error {
	path := s.daemonModeFile()
	if enabled {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte("1\n"), 0644)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (s *Server) setSymlinkEnabled(enabled bool) error {
	binDir := "/data/adb/modules/droidspaces/system/bin"
	link := filepath.Join(binDir, "droidspaces")
	if enabled {
		if err := os.MkdirAll(binDir, 0755); err != nil {
			return err
		}
		_ = os.Remove(link)
		if err := os.Symlink(s.droidspacesPath, link); err != nil {
			return err
		}
		return os.Chmod(link, 0755)
	}
	if err := os.Remove(link); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func readContainerConfigValues(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		key, value, _ := strings.Cut(line, "=")
		out[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return out
}

func (resp *inspectResponse) applyConfigValues(values map[string]string) {
	if len(values) == 0 {
		return
	}
	resp.ConfigValues = values
	resp.containerView.applyContainerConfigState(values)
	if v := values["net_mode"]; v != "" {
		resp.NetMode = v
	}
	if v := values["hostname"]; v != "" {
		resp.Hostname = v
	}
	if v := values["rootfs_path"]; v != "" {
		resp.RootFSPath = v
	}
	if v := values["custom_init"]; v != "" {
		resp.CustomInit = v
	}
	if v := values["dns_servers"]; v != "" {
		resp.DNSServers = v
	}
	if v := values["static_nat_ip"]; v != "" {
		resp.StaticNATIP = v
		if resp.NATIP == "" {
			resp.NATIP = v
		}
	}
	if v := firstNonEmpty(values["nat_upstream_ifnames"], values["nat_upstream_ifname"], values["nat_upstream_ifaces"], values["nat_upstream_iface"], values["upstream_interfaces"]); v != "" {
		resp.NATUpstreamIfnames = v
	}
	resp.GatewayContainer = values["gateway_container"]
	resp.GatewayNet = values["gateway_net"]
	resp.GatewayLanIfname = values["gateway_lan_ifname"]
	resp.GatewayBridge = values["gateway_bridge"]
	resp.PrivilegedMode = values["privileged"]
	resp.Tx11ExtraFlags = values["tx11_extra_flags"]
	resp.VirGLExtraFlags = values["virgl_extra_flags"]
	if v, ok := values["disable_ipv6"]; ok {
		resp.DisableIPv6 = kvBool(v)
	}
	if v, ok := values["enable_android_storage"]; ok {
		resp.AndroidStorage = kvBool(v)
	}
	if v, ok := values["enable_hw_access"]; ok {
		resp.HWAccess = kvBool(v)
	}
	if v, ok := values["enable_gpu_mode"]; ok {
		resp.GPUMode = kvBool(v)
	}
	if v, ok := values["enable_termux_x11"]; ok {
		resp.TermuxX11 = kvBool(v)
	}
	if v, ok := values["enable_virgl"]; ok {
		resp.VirGL = kvBool(v)
	}
	if v, ok := values["enable_pulseaudio"]; ok {
		resp.PulseAudio = kvBool(v)
	}
	if v, ok := values["selinux_permissive"]; ok {
		resp.SELinuxPermissive = kvBool(v)
	}
	if v, ok := values["volatile_mode"]; ok {
		resp.VolatileMode = kvBool(v)
	}
	if v, ok := values["force_cgroupv1"]; ok {
		resp.ForceCgroupV1 = kvBool(v)
	}
	if v, ok := values["block_nested_ns"]; ok {
		resp.BlockNestedNS = kvBool(v)
	}
	if v := parseConfigInt64(values["memory_limit"]); v > 0 {
		resp.MemoryLimit = v
		resp.MemoryLimitText = formatBytes(uint64(v))
	}
	quota := parseConfigInt64(values["cpu_quota"])
	period := parseConfigInt64(values["cpu_period"])
	if quota > 0 {
		resp.CPUQuota = quota
		if period > 0 {
			resp.CPUPeriod = period
		}
		resp.CPUsText = formatCPUs(resp.CPUQuota, resp.CPUPeriod)
	}
	if v := parseConfigInt64(values["pids_limit"]); v > 0 {
		resp.PidsLimit = v
	}
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

func (s *Server) removeContainerLogs(name string) ([]string, error) {
	candidates := []string{name, sanitizeContainerName(name)}
	logRoots := []string{filepath.Join(s.workspace, "Logs"), filepath.Join(s.workspace, "logs")}
	removed := []string{}
	seen := map[string]bool{}
	for _, root := range logRoots {
		for _, candidate := range candidates {
			if candidate == "" || strings.ContainsAny(candidate, `/\\`) {
				continue
			}
			path, ok := safeJoinChild(root, candidate)
			if !ok || seen[path] {
				continue
			}
			seen[path] = true
			info, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return removed, err
			}
			if !info.IsDir() {
				continue
			}
			if err := os.RemoveAll(path); err != nil {
				return removed, err
			}
			removed = append(removed, path)
		}
	}
	return removed, nil
}

func safeJoinChild(root string, child string) (string, bool) {
	base := filepath.Clean(root)
	path := filepath.Clean(filepath.Join(base, child))
	if path == base || !strings.HasPrefix(path, base+string(os.PathSeparator)) {
		return "", false
	}
	return path, true
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

func netModeForcesDisableIPv6(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "nat", "none":
		return true
	default:
		return false
	}
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

func (s *Server) recordBackendDiagnostic(source string, err error) {
	if err == nil {
		return
	}
	message := strings.TrimSpace(err.Error())
	if message == "" || message == "socketd disabled" {
		return
	}
	entry := backendDiagnosticEntry{
		Time:    time.Now().Unix(),
		Source:  source,
		Message: message,
		Hint:    backendErrorHint(err),
	}
	s.backendDiagnosticsMu.Lock()
	if len(s.backendDiagnostics) > 0 {
		prev := &s.backendDiagnostics[0]
		if prev.Source == entry.Source && prev.Message == entry.Message {
			prev.Time = entry.Time
			prev.Hint = entry.Hint
			s.backendDiagnosticsMu.Unlock()
			return
		}
	}
	s.backendDiagnostics = append([]backendDiagnosticEntry{entry}, s.backendDiagnostics...)
	if len(s.backendDiagnostics) > 40 {
		s.backendDiagnostics = s.backendDiagnostics[:40]
	}
	s.backendDiagnosticsMu.Unlock()
	log.Printf("backend diagnostic source=%q error=%q", entry.Source, entry.Message)
}

func (s *Server) backendDiagnosticLog() []backendDiagnosticEntry {
	s.backendDiagnosticsMu.Lock()
	defer s.backendDiagnosticsMu.Unlock()
	out := make([]backendDiagnosticEntry, len(s.backendDiagnostics))
	copy(out, s.backendDiagnostics)
	return out
}

func backendErrorHint(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "connection refused"):
		return "socketd 抽象 Unix socket 存在但没有服务接受连接；通常是 Droidspaces socketd 后端未启动、已崩溃，或 WebUI 与核心版本不匹配。"
	case strings.Contains(text, "no such file") || strings.Contains(text, "not found"):
		return "WebUI 找不到 socketd 抽象 Unix socket；确认 Droidspaces 后端已启动且启用了 socketd。"
	case strings.Contains(text, "permission denied"):
		return "当前 WebUI 进程没有权限连接 socketd；确认它与 Droidspaces 后端运行在兼容的用户/SELinux 上下文中。"
	case strings.Contains(text, "i/o timeout") || strings.Contains(text, "deadline exceeded"):
		return "连接 socketd 超时；后端可能卡住或负载过高。"
	default:
		return ""
	}
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
