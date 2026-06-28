package rootfs

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
)

type Asset struct {
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

type rawAsset struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Architecture string `json:"architecture"`
	DownloadURL  string `json:"download_url"`
	SizeBytes    int64  `json:"size_bytes"`
	BuildDate    string `json:"build_date"`
	Author       string `json:"author"`
}

type Client struct {
	HTTPClient *http.Client
}

func NewClient(skipTLSVerify ...bool) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if len(skipTLSVerify) > 0 && skipTLSVerify[0] {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &Client{HTTPClient: &http.Client{Timeout: 30 * time.Second, Transport: transport}}
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
		repos = []config.RootfsRepository{{Name: "Droidspaces Official", URL: config.OfficialRootfsRepositoryURL}}
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

	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
	if err != nil {
		return nil, err
	}
	return Parse(data, repo.Name, arch)
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
			BuildDate:      item.BuildDate,
			Author:         author,
			SourceRepoName: repoName,
		}
		asset.UniqueFilename = UniqueFilename(asset)
		assets = append(assets, asset)
	}
	return assets, nil
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

func (c *Client) Download(ctx context.Context, asset Asset, templateRoot string) (string, error) {
	return c.DownloadWithProgress(ctx, asset, templateRoot, nil)
}

func (c *Client) DownloadWithProgress(ctx context.Context, asset Asset, templateRoot string, progress ProgressFunc) (string, error) {
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", err
	}
	total := resp.ContentLength
	if asset.SizeBytes > 0 {
		total = asset.SizeBytes
	}
	if progress != nil {
		progress(0, total)
	}
	_, copyErr := io.Copy(out, &progressReader{reader: resp.Body, total: total, progress: progress})
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return "", closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if progress != nil {
		if info, statErr := os.Stat(dest); statErr == nil {
			progress(info.Size(), total)
		}
	}
	return dest, nil
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
