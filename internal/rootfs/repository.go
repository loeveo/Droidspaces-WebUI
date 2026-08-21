package rootfs

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/platformhttp"
)

type Asset struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Architecture   string `json:"architecture"`
	DownloadURL    string `json:"downloadUrl"`
	SizeBytes      int64  `json:"sizeBytes"`
	SHA256         string `json:"sha256,omitempty"`
	Variant        string `json:"variant,omitempty"`
	BuildDate      string `json:"buildDate"`
	Author         string `json:"author"`
	SourceRepoName string `json:"sourceRepoName"`
	UniqueFilename string `json:"uniqueFilename"`
}

type rawAsset struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Architecture string `json:"architecture"`
	DownloadURL  string `json:"download_url"`
	SizeBytes    int64  `json:"size_bytes"`
	SHA256       string `json:"sha256"`
	Variant      string `json:"variant"`
	BuildDate    string `json:"build_date"`
	Author       string `json:"author"`
}

type linuxContainersCatalog struct {
	Products map[string]linuxContainersProduct `json:"products"`
}

type linuxContainersProduct struct {
	Aliases      string                            `json:"aliases"`
	Architecture string                            `json:"arch"`
	OS           string                            `json:"os"`
	Release      string                            `json:"release"`
	ReleaseTitle string                            `json:"release_title"`
	Variant      string                            `json:"variant"`
	Versions     map[string]linuxContainersVersion `json:"versions"`
}

type linuxContainersVersion struct {
	Items map[string]linuxContainersItem `json:"items"`
}

type linuxContainersItem struct {
	FileType string `json:"ftype"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

// linuxContainersCodenameVersions turns catalog codenames into scan-friendly
// version labels without dropping the codename users need for package sources.
var linuxContainersCodenameVersions = map[string]map[string]string{
	"debian": {
		"forky":    "14",
		"trixie":   "13",
		"bookworm": "12",
		"bullseye": "11",
		"buster":   "10",
		"stretch":  "9",
		"jessie":   "8",
	},
	"ubuntu": {
		"resolute": "26.04",
		"questing": "25.10",
		"plucky":   "25.04",
		"oracular": "24.10",
		"noble":    "24.04",
		"mantic":   "23.10",
		"lunar":    "23.04",
		"kinetic":  "22.10",
		"jammy":    "22.04",
		"impish":   "21.10",
		"hirsute":  "21.04",
		"groovy":   "20.10",
		"focal":    "20.04",
		"eoan":     "19.10",
		"disco":    "19.04",
		"cosmic":   "18.10",
		"bionic":   "18.04",
	},
	"devuan": {
		"excalibur": "6",
		"daedalus":  "5",
		"chimaera":  "4",
		"beowulf":   "3",
		"ascii":     "2",
		"jessie":    "1",
	},
	"mint": {
		"ulyssa":   "20.1",
		"uma":      "20.2",
		"una":      "20.3",
		"vanessa":  "21",
		"vera":     "21.1",
		"victoria": "21.2",
		"virginia": "21.3",
		"wilma":    "22",
		"xia":      "22.1",
		"zara":     "22.2",
		"zena":     "22.3",
	},
}

type Client struct {
	HTTPClient *http.Client
}

const (
	downloadMaxAttempts   = 4
	downloadRetryBaseWait = 500 * time.Millisecond
	maxRepositoryBodySize = 20 << 20
)

func NewClient(skipTLSVerify ...bool) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	platformhttp.ConfigureAndroidTransport(transport)
	if len(skipTLSVerify) > 0 && skipTLSVerify[0] {
		tlsConfig := transport.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		tlsConfig.InsecureSkipVerify = true
		transport.TLSClientConfig = tlsConfig
	}
	// Downloads can take much longer than the metadata request timeout. Callers
	// provide a context with the appropriate lifetime, so do not impose a
	// client-wide deadline here.
	return &Client{HTTPClient: &http.Client{Transport: transport}}
}

func DeviceArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "arm":
		return "armhf"
	case "amd64":
		return "x86_64"
	case "386":
		return "x86"
	default:
		return "aarch64"
	}
}

func (c *Client) FetchAll(ctx context.Context, repos []config.RootfsRepository, arch string) ([]Asset, []string) {
	if arch == "" {
		arch = DeviceArch()
	}
	if len(repos) == 0 {
		repos = config.DefaultRootfsRepositories()
	}

	var out []Asset
	var errs []string
	for _, repo := range repos {
		assets, err := c.Fetch(ctx, repo, arch)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", repo.Name, err))
			continue
		}
		out = append(out, assets...)
	}
	return out, errs
}

func (c *Client) Fetch(ctx context.Context, repo config.RootfsRepository, arch string) ([]Asset, error) {
	if repo.Name == "" {
		repo.Name = repo.URL
	}
	if isLinuxContainersRepository(repo.URL) {
		return c.fetchLinuxContainers(ctx, repo, arch)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repo.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := readRepositoryBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return Parse(data, repo.Name, arch)
}

func isLinuxContainersRepository(repositoryURL string) bool {
	if isLinuxContainersNJUMirror(repositoryURL) {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(repositoryURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	return strings.HasSuffix(path, "/streams/v1/images.json") ||
		(strings.EqualFold(parsed.Hostname(), "images.linuxcontainers.org") && (path == "" || path == "/"))
}

func (c *Client) fetchLinuxContainers(ctx context.Context, repo config.RootfsRepository, arch string) ([]Asset, error) {
	catalogURL, imageBaseURL, err := linuxContainersURLs(repo.URL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := readRepositoryBody(resp.Body)
	if err != nil {
		return nil, err
	}
	if !isLinuxContainersNJUMirror(repo.URL) {
		return ParseLinuxContainers(data, repo.Name, arch, imageBaseURL)
	}

	availablePaths, err := c.fetchLinuxContainersNJUPaths(ctx, imageBaseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch Nanjing University image index: %w", err)
	}
	return ParseLinuxContainersWithAvailablePaths(data, repo.Name, arch, imageBaseURL, availablePaths)
}

// fetchLinuxContainersNJUPaths reads the mirror's image index so a delayed
// mirror never exposes a catalog version that would fail during download.
func (c *Client) fetchLinuxContainersNJUPaths(ctx context.Context, imageBaseURL *url.URL) (map[string]bool, error) {
	if imageBaseURL == nil {
		return nil, errors.New("Linux Containers image base URL is required")
	}
	indexURL := *imageBaseURL
	indexURL.Path = strings.TrimSuffix(indexURL.Path, "/") + "/"
	indexURL.RawPath = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/html")
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := readRepositoryBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseLinuxContainersImageIndex(data), nil
}

var linuxContainersImageIndexRow = regexp.MustCompile(`(?is)<tr>\s*<td>\s*([^<]+?)\s*</td>\s*<td>\s*([^<]+?)\s*</td>\s*<td>\s*([^<]+?)\s*</td>\s*<td>\s*([^<]+?)\s*</td>\s*<td>\s*<a\s+[^>]*href=["'][^"']*/images/([^"']+)/["']`)

func parseLinuxContainersImageIndex(data []byte) map[string]bool {
	paths := make(map[string]bool)
	for _, match := range linuxContainersImageIndexRow.FindAllSubmatch(data, -1) {
		if len(match) != 6 {
			continue
		}
		path, err := url.PathUnescape(string(match[5]))
		if err != nil {
			continue
		}
		path = strings.Trim(strings.TrimSpace(path), "/")
		if path != "" {
			paths["images/"+path+"/rootfs.tar.xz"] = true
		}
	}
	return paths
}

func linuxContainersURLs(repositoryURL string) (*url.URL, *url.URL, error) {
	if isLinuxContainersNJUMirror(repositoryURL) {
		catalogURL, err := url.Parse(config.LinuxContainersRepositoryURL + "streams/v1/images.json")
		if err != nil {
			return nil, nil, fmt.Errorf("parse Linux Containers catalog URL: %w", err)
		}
		imageBaseURL, err := linuxContainersImageBaseURL(repositoryURL)
		if err != nil {
			return nil, nil, err
		}
		return catalogURL, imageBaseURL, nil
	}

	return linuxContainersCatalogAndImageURLs(repositoryURL)
}

func linuxContainersCatalogAndImageURLs(repositoryURL string) (*url.URL, *url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(repositoryURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, nil, fmt.Errorf("invalid Linux Containers repository URL %q", repositoryURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, nil, fmt.Errorf("unsupported Linux Containers repository scheme %q", parsed.Scheme)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""

	const catalogPath = "streams/v1/images.json"
	path := parsed.EscapedPath()
	imageBasePath := path
	if strings.HasSuffix(path, "/"+catalogPath) {
		imageBasePath = strings.TrimSuffix(path, catalogPath)
	} else {
		if !strings.HasSuffix(imageBasePath, "/") {
			imageBasePath += "/"
		}
		path = imageBasePath + catalogPath
	}
	catalogURL := *parsed
	catalogURL.Path = path
	catalogURL.RawPath = ""

	imageBaseURL := *parsed
	imageBaseURL.Path = imageBasePath
	imageBaseURL.RawPath = ""
	return &catalogURL, &imageBaseURL, nil
}

func linuxContainersImageBaseURL(repositoryURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(repositoryURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("invalid Linux Containers repository URL %q", repositoryURL)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("unsupported Linux Containers repository scheme %q", parsed.Scheme)
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}
	parsed.RawPath = ""
	return parsed, nil
}

func isLinuxContainersNJUMirror(repositoryURL string) bool {
	return config.IsLinuxContainersNJURepositoryURL(repositoryURL)
}

func readRepositoryBody(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxRepositoryBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRepositoryBodySize {
		return nil, fmt.Errorf("repository metadata exceeds %d MiB", maxRepositoryBodySize>>20)
	}
	return data, nil
}

func Parse(data []byte, repoName string, arch string) ([]Asset, error) {
	var raw []rawAsset
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	assets := make([]Asset, 0, len(raw))
	for _, item := range raw {
		if arch != "" && item.Architecture != arch {
			continue
		}
		author := item.Author
		if author == "" {
			author = repoName
		}
		asset := Asset{
			Name:           item.Name,
			Description:    item.Description,
			Architecture:   item.Architecture,
			DownloadURL:    item.DownloadURL,
			SizeBytes:      item.SizeBytes,
			SHA256:         strings.TrimSpace(item.SHA256),
			Variant:        strings.TrimSpace(item.Variant),
			BuildDate:      item.BuildDate,
			Author:         author,
			SourceRepoName: repoName,
		}
		asset.UniqueFilename = UniqueFilename(asset)
		assets = append(assets, asset)
	}
	return assets, nil
}

// ParseLinuxContainers converts a SimpleStreams images.json catalog into the
// same asset list used by the existing background downloader. Only the latest
// root.tar.xz for each product is exposed: Incus metadata, squashfs images and
// VM disks are not valid Droidspaces rootfs templates.
func ParseLinuxContainers(data []byte, repoName string, arch string, imageBaseURL *url.URL) ([]Asset, error) {
	return parseLinuxContainers(data, repoName, arch, imageBaseURL, nil)
}

// ParseLinuxContainersWithAvailablePaths filters a catalog against an image
// server index. It is used by mirrors that can lag behind the upstream catalog.
func ParseLinuxContainersWithAvailablePaths(data []byte, repoName string, arch string, imageBaseURL *url.URL, availablePaths map[string]bool) ([]Asset, error) {
	return parseLinuxContainers(data, repoName, arch, imageBaseURL, availablePaths)
}

func parseLinuxContainers(data []byte, repoName string, arch string, imageBaseURL *url.URL, availablePaths map[string]bool) ([]Asset, error) {
	if imageBaseURL == nil {
		return nil, fmt.Errorf("Linux Containers image base URL is required")
	}
	var catalog linuxContainersCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}

	wantedArchitecture := linuxContainersArchitecture(arch)
	assets := make([]Asset, 0, len(catalog.Products))
	for _, product := range catalog.Products {
		if wantedArchitecture != "" && !strings.EqualFold(product.Architecture, wantedArchitecture) {
			continue
		}
		versionName, item, ok := latestLinuxContainersRootfsForPaths(product, availablePaths)
		if !ok {
			continue
		}
		downloadURL, err := linuxContainersDownloadURL(imageBaseURL, item.Path)
		if err != nil {
			continue
		}

		name := linuxContainersDisplayName(product)
		if name == "" {
			name = "Linux Containers template"
		}
		variant := strings.TrimSpace(product.Variant)
		description := "Linux Containers 官方镜像"
		if variant != "" {
			description += " · " + variant
		}
		if aliases := strings.TrimSpace(product.Aliases); aliases != "" {
			description += " · " + aliases
		}
		displayArchitecture := strings.TrimSpace(arch)
		if displayArchitecture == "" {
			displayArchitecture = rootfsArchitecture(product.Architecture)
		}
		asset := Asset{
			Name:           name,
			Description:    description,
			Architecture:   displayArchitecture,
			DownloadURL:    downloadURL,
			SizeBytes:      item.Size,
			SHA256:         strings.TrimSpace(item.SHA256),
			Variant:        variant,
			BuildDate:      versionName,
			Author:         "Linux Containers",
			SourceRepoName: repoName,
		}
		asset.UniqueFilename = UniqueFilename(asset)
		assets = append(assets, asset)
	}
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].Name != assets[j].Name {
			return assets[i].Name < assets[j].Name
		}
		return assets[i].DownloadURL < assets[j].DownloadURL
	})
	return assets, nil
}

func linuxContainersReleaseTitle(product linuxContainersProduct) string {
	release := strings.TrimSpace(product.Release)
	releaseKey := strings.ToLower(release)
	osKey := linuxContainersOSKey(product.OS)
	if versions := linuxContainersCodenameVersions[osKey]; versions != nil {
		if version, ok := versions[releaseKey]; ok {
			title := strings.TrimSpace(product.ReleaseTitle)
			if title != "" && !strings.EqualFold(title, release) && !strings.Contains(strings.ToLower(title), releaseKey) {
				return title + " (" + releaseKey + ")"
			}
			return version + " (" + releaseKey + ")"
		}
		if releaseKey != "" && strings.TrimSpace(product.ReleaseTitle) == "" {
			return "(" + releaseKey + ")"
		}
	}
	if releaseTitle := strings.TrimSpace(product.ReleaseTitle); releaseTitle != "" {
		return releaseTitle
	}
	if version, stream, ok := strings.Cut(releaseKey, "-"); ok && stream == "stream" && osKey == "centos" && version != "" {
		return "Stream " + version
	}
	switch releaseKey {
	case "current", "edge", "snapshot", "tumbleweed", "sisyphus":
		return strings.ToUpper(releaseKey[:1]) + releaseKey[1:]
	default:
		return release
	}
}

func linuxContainersDisplayName(product linuxContainersProduct) string {
	osName := linuxContainersOSName(product.OS)
	releaseTitle := linuxContainersReleaseTitle(product)
	return strings.TrimSpace(strings.Join([]string{osName, releaseTitle}, " "))
}

func linuxContainersOSKey(value string) string {
	key := strings.ToLower(strings.TrimSpace(value))
	key = strings.NewReplacer(" ", "", "-", "", "_", "", "/", "").Replace(key)
	return key
}

func linuxContainersOSName(value string) string {
	switch linuxContainersOSKey(value) {
	case "almalinux":
		return "AlmaLinux"
	case "alt":
		return "ALT Linux"
	case "amazonlinux":
		return "Amazon Linux"
	case "arch", "archlinux":
		return "Arch Linux"
	case "busybox":
		return "BusyBox"
	case "centos":
		return "CentOS"
	case "debian":
		return "Debian"
	case "devuan":
		return "Devuan"
	case "freebsd":
		return "FreeBSD"
	case "kali", "kalilinux":
		return "Kali Linux"
	case "mint", "linuxmint":
		return "Linux Mint"
	case "nixos":
		return "NixOS"
	case "openeuler":
		return "openEuler"
	case "opensuse":
		return "openSUSE"
	case "openwrt":
		return "OpenWrt"
	case "oracle", "oraclelinux":
		return "Oracle Linux"
	case "plamo":
		return "Plamo"
	case "rockylinux":
		return "Rocky Linux"
	case "voidlinux":
		return "Void Linux"
	default:
		return strings.TrimSpace(value)
	}
}

func linuxContainersArchitecture(architecture string) string {
	switch strings.ToLower(strings.TrimSpace(architecture)) {
	case "aarch64", "arm64":
		return "arm64"
	case "x86_64", "amd64":
		return "amd64"
	case "x86", "i386":
		return "i386"
	default:
		return strings.TrimSpace(architecture)
	}
}

func rootfsArchitecture(architecture string) string {
	switch strings.ToLower(strings.TrimSpace(architecture)) {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	case "i386":
		return "x86"
	default:
		return strings.TrimSpace(architecture)
	}
}

func latestLinuxContainersRootfs(product linuxContainersProduct) (string, linuxContainersItem, bool) {
	return latestLinuxContainersRootfsForPaths(product, nil)
}

func latestLinuxContainersRootfsForPaths(product linuxContainersProduct, availablePaths map[string]bool) (string, linuxContainersItem, bool) {
	var latestVersion string
	var latest linuxContainersItem
	for versionName, version := range product.Versions {
		item, found := version.Items["root.tar.xz"]
		if !found || !strings.EqualFold(strings.TrimSpace(item.FileType), "root.tar.xz") || item.Size <= 0 {
			continue
		}
		if availablePaths != nil && !availablePaths[strings.Trim(strings.TrimSpace(item.Path), "/")] {
			continue
		}
		if latestVersion == "" || versionName > latestVersion {
			latestVersion = versionName
			latest = item
		}
	}
	return latestVersion, latest, latestVersion != ""
}

func linuxContainersDownloadURL(imageBaseURL *url.URL, path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("invalid Linux Containers rootfs path %q", path)
	}
	relative, err := url.Parse(path)
	if err != nil || relative.IsAbs() || relative.Host != "" || strings.Contains(relative.Path, "../") || strings.HasPrefix(relative.Path, "..") {
		return "", fmt.Errorf("invalid Linux Containers rootfs path %q", path)
	}
	resolved := imageBaseURL.ResolveReference(relative)
	if !strings.EqualFold(resolved.Scheme, imageBaseURL.Scheme) || !strings.EqualFold(resolved.Host, imageBaseURL.Host) {
		return "", fmt.Errorf("Linux Containers rootfs path resolves outside the configured source")
	}
	if !strings.HasSuffix(strings.ToLower(resolved.Path), ".tar.xz") {
		return "", fmt.Errorf("Linux Containers rootfs is not a .tar.xz archive")
	}
	return resolved.String(), nil
}

func UniqueFilename(asset Asset) string {
	ext := ".tar.xz"
	if strings.HasSuffix(asset.DownloadURL, ".tar.gz") {
		ext = ".tar.gz"
	} else if strings.HasSuffix(asset.DownloadURL, ".tar.xz") {
		ext = ".tar.xz"
	}
	base := sanitize(asset.Name + " " + asset.Author)
	if base == "" {
		base = "rootfs"
	}
	sum := sha256.Sum256([]byte(asset.DownloadURL))
	short := hex.EncodeToString(sum[:4])
	parts := []string{base}
	if asset.Architecture != "" {
		parts = append(parts, sanitize(asset.Architecture))
	}
	if asset.BuildDate != "" {
		parts = append(parts, sanitize(asset.BuildDate))
	}
	parts = append(parts, short)
	return strings.Join(parts, "-") + ext
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)
var repeatedDash = regexp.MustCompile(`-{2,}`)

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	value = unsafeChars.ReplaceAllString(value, "-")
	value = repeatedDash.ReplaceAllString(value, "-")
	return strings.Trim(value, "-")
}

type ProgressFunc func(downloaded int64, total int64)

// DownloadLogFunc receives human-readable download lifecycle messages. It is
// intentionally separate from ProgressFunc so callers can persist task logs
// without inferring retry state from byte counters.
type DownloadLogFunc func(message string)

func (c *Client) Download(ctx context.Context, asset Asset, templateRoot string) (string, error) {
	return c.DownloadWithProgress(ctx, asset, templateRoot, nil)
}

func (c *Client) DownloadWithProgress(ctx context.Context, asset Asset, templateRoot string, progress ProgressFunc) (string, error) {
	return c.DownloadWithProgressAndLog(ctx, asset, templateRoot, progress, nil)
}

// DownloadWithProgressAndLog downloads an asset into templateRoot. Interrupted
// transfers are retried with HTTP range requests while retaining the .part file
// for subsequent attempts and process restarts.
func (c *Client) DownloadWithProgressAndLog(ctx context.Context, asset Asset, templateRoot string, progress ProgressFunc, log DownloadLogFunc) (string, error) {
	if templateRoot == "" {
		return "", fmt.Errorf("template image root is empty")
	}
	if asset.DownloadURL == "" {
		return "", fmt.Errorf("download URL is empty")
	}
	filename := asset.UniqueFilename
	if filename == "" {
		filename = UniqueFilename(asset)
	}
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return "", fmt.Errorf("invalid filename")
	}
	if err := os.MkdirAll(templateRoot, 0755); err != nil {
		return "", err
	}
	dest := filepath.Join(templateRoot, filename)
	tmp := dest + ".part"

	if c == nil || c.HTTPClient == nil {
		return "", fmt.Errorf("rootfs HTTP client is not configured")
	}

	for attempt := 1; attempt <= downloadMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		offset, err := partialSize(tmp)
		if err != nil {
			return "", err
		}
		if asset.SizeBytes > 0 && offset > asset.SizeBytes {
			logDownload(log, "Discarding partial download larger than the expected asset size")
			if err := truncatePartial(tmp); err != nil {
				return "", err
			}
			offset = 0
		}
		if asset.SizeBytes > 0 && offset == asset.SizeBytes {
			logDownload(log, "Found a complete partial download; finalizing it")
			return finalizeDownload(tmp, dest, asset.SizeBytes, progress)
		}

		if offset > 0 {
			logDownload(log, fmt.Sprintf("Resuming download at %d bytes", offset))
		}
		logDownload(log, fmt.Sprintf("Download attempt %d/%d", attempt, downloadMaxAttempts))

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
		if err != nil {
			return "", err
		}
		// Archive size and byte ranges must refer to the uncompressed response.
		req.Header.Set("Accept-Encoding", "identity")
		// A fresh connection avoids carrying a corrupted TLS stream into a retry.
		req.Close = true
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			if retry, retryErr := shouldRetryDownload(ctx, err); retry && attempt < downloadMaxAttempts {
				logDownload(log, fmt.Sprintf("Download request failed: %v; retrying", err))
				if err := waitForDownloadRetry(ctx, attempt); err != nil {
					return "", err
				}
				continue
			} else if retryErr != nil {
				return "", retryErr
			}
			return "", err
		}

		appendToPartial := offset > 0
		responseOffset := offset
		responseTotal := asset.SizeBytes
		responseRangeStart, rangeTotal, hasRange := parseContentRange(resp.Header.Get("Content-Range"))

		switch {
		case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0:
			_ = resp.Body.Close()
			if rangeTotal > 0 && offset == rangeTotal {
				logDownload(log, "Server confirmed the partial download is complete; finalizing it")
				return finalizeDownload(tmp, dest, rangeTotal, progress)
			}
			if asset.SizeBytes > 0 && offset == asset.SizeBytes {
				logDownload(log, "Server rejected a completed range; finalizing the verified partial download")
				return finalizeDownload(tmp, dest, asset.SizeBytes, progress)
			}
			logDownload(log, "Server rejected the saved byte range; restarting the partial download")
			if err := truncatePartial(tmp); err != nil {
				return "", err
			}
			if attempt == downloadMaxAttempts {
				return "", fmt.Errorf("HTTP %d after restarting an invalid byte range", resp.StatusCode)
			}
			if err := waitForDownloadRetry(ctx, attempt); err != nil {
				return "", err
			}
			continue
		case resp.StatusCode == http.StatusPartialContent:
			if !hasRange || responseRangeStart != offset {
				_ = resp.Body.Close()
				err := fmt.Errorf("unexpected Content-Range %q for offset %d", resp.Header.Get("Content-Range"), offset)
				if attempt < downloadMaxAttempts {
					logDownload(log, fmt.Sprintf("%v; retrying", err))
					if err := waitForDownloadRetry(ctx, attempt); err != nil {
						return "", err
					}
					continue
				}
				return "", err
			}
			if responseTotal <= 0 {
				responseTotal = rangeTotal
			}
		case resp.StatusCode == http.StatusOK:
			if offset > 0 {
				logDownload(log, "Server does not support byte ranges; restarting download")
				appendToPartial = false
				responseOffset = 0
			}
		default:
			statusErr := fmt.Errorf("HTTP %d", resp.StatusCode)
			_ = resp.Body.Close()
			if isRetryableHTTPStatus(resp.StatusCode) && attempt < downloadMaxAttempts {
				logDownload(log, fmt.Sprintf("Download server returned %v; retrying", statusErr))
				if err := waitForDownloadRetry(ctx, attempt); err != nil {
					return "", err
				}
				continue
			}
			return "", statusErr
		}

		if responseTotal <= 0 && resp.ContentLength >= 0 {
			responseTotal = responseOffset + resp.ContentLength
		}
		if progress != nil {
			progress(responseOffset, responseTotal)
		}

		out, err := openPartial(tmp, appendToPartial)
		if err != nil {
			_ = resp.Body.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, &progressReader{
			reader:   resp.Body,
			done:     responseOffset,
			total:    responseTotal,
			progress: progress,
		})
		closeErr := out.Close()
		bodyCloseErr := resp.Body.Close()
		if copyErr == nil && closeErr == nil && bodyCloseErr == nil {
			if _, err := validatePartialSize(tmp, responseTotal); err == nil {
				logDownload(log, "Download complete")
				return finalizeDownload(tmp, dest, responseTotal, progress)
			} else {
				copyErr = err
			}
		} else if copyErr == nil {
			if closeErr != nil {
				copyErr = closeErr
			} else {
				copyErr = bodyCloseErr
			}
		}

		if retry, retryErr := shouldRetryDownload(ctx, copyErr); retry && attempt < downloadMaxAttempts {
			logDownload(log, fmt.Sprintf("Download interrupted: %v; retrying with the saved partial file", copyErr))
			if err := waitForDownloadRetry(ctx, attempt); err != nil {
				return "", err
			}
			continue
		} else if retryErr != nil {
			return "", retryErr
		}
		return "", copyErr
	}

	return "", fmt.Errorf("download failed after %d attempts", downloadMaxAttempts)
}

func partialSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if info.IsDir() {
		return 0, fmt.Errorf("partial download path is a directory")
	}
	return info.Size(), nil
}

func truncatePartial(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	return file.Close()
}

func openPartial(path string, appendToPartial bool) (*os.File, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if appendToPartial {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0644)
}

func finalizeDownload(tmp string, dest string, total int64, progress ProgressFunc) (string, error) {
	size, err := validatePartialSize(tmp, total)
	if err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dest); err != nil {
		return "", err
	}
	if progress != nil {
		progress(size, total)
	}
	return dest, nil
}

func validatePartialSize(path string, expected int64) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	if expected > 0 && info.Size() != expected {
		return info.Size(), fmt.Errorf("download size mismatch: got %d bytes, expected %d", info.Size(), expected)
	}
	return info.Size(), nil
}

func parseContentRange(value string) (start int64, total int64, ok bool) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bytes") {
		return 0, 0, false
	}
	rangeAndTotal := strings.SplitN(parts[1], "/", 2)
	if len(rangeAndTotal) != 2 {
		return 0, 0, false
	}
	if rangeAndTotal[0] == "*" {
		if rangeAndTotal[1] == "*" {
			return 0, 0, false
		}
		parsedTotal, err := strconv.ParseInt(rangeAndTotal[1], 10, 64)
		if err != nil || parsedTotal < 0 {
			return 0, 0, false
		}
		return 0, parsedTotal, true
	}
	startAndEnd := strings.SplitN(rangeAndTotal[0], "-", 2)
	if len(startAndEnd) != 2 {
		return 0, 0, false
	}
	parsedStart, err := strconv.ParseInt(startAndEnd[0], 10, 64)
	if err != nil || parsedStart < 0 {
		return 0, 0, false
	}
	if _, err := strconv.ParseInt(startAndEnd[1], 10, 64); err != nil {
		return 0, 0, false
	}
	if rangeAndTotal[1] == "*" {
		return parsedStart, 0, true
	}
	parsedTotal, err := strconv.ParseInt(rangeAndTotal[1], 10, 64)
	if err != nil || parsedTotal < 0 {
		return 0, 0, false
	}
	return parsedStart, parsedTotal, true
}

func isRetryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func shouldRetryDownload(ctx context.Context, err error) (bool, error) {
	if err == nil {
		return false, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false, err
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return true, nil
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) {
		return true, nil
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"bad record mac",
		"tls:",
		"connection reset",
		"connection aborted",
		"broken pipe",
		"download size mismatch",
		"unexpected eof",
		"server closed idle connection",
	} {
		if strings.Contains(message, marker) {
			return true, nil
		}
	}
	return false, nil
}

func waitForDownloadRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * downloadRetryBaseWait
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func logDownload(log DownloadLogFunc, message string) {
	if log != nil {
		log(message)
	}
}

type progressReader struct {
	reader   io.Reader
	done     int64
	total    int64
	progress ProgressFunc
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.done += int64(n)
		if r.progress != nil {
			r.progress(r.done, r.total)
		}
	}
	return n, err
}
