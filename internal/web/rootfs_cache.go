package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/rootfs"
)

const (
	rootfsListCacheTTL       = 24 * time.Hour
	rootfsListCacheVersion   = 1
	rootfsListCacheDirectory = "cache/rootfs"
)

// rootfsListCacheMetadata is deliberately additive to the existing rootfs
// list response. Clients that do not know about it continue to use assets and
// errors unchanged.
type rootfsListCacheMetadata struct {
	CachedAt time.Time `json:"cachedAt,omitempty"`
	Stale    bool      `json:"stale"`
}

type rootfsListCacheEntry struct {
	Version     int            `json:"version"`
	Fingerprint string         `json:"fingerprint"`
	CachedAt    time.Time      `json:"cachedAt"`
	Assets      []rootfs.Asset `json:"assets"`
}

type rootfsListCacheResult struct {
	Assets            []rootfs.Asset
	Errors            []string
	Repositories      []config.RootfsRepository
	TemplateImageRoot string
	Cache             rootfsListCacheMetadata
}

// cachedRootfsList serializes cache reads, metadata refreshes, and cache
// writes. This prevents concurrent requests from issuing duplicate catalog
// downloads after the same cache entry expires and keeps disk writes atomic.
func (s *Server) cachedRootfsList(ctx context.Context, requestedArch string, forceRefresh bool) rootfsListCacheResult {
	s.rootfsCacheMu.Lock()
	defer s.rootfsCacheMu.Unlock()

	arch := strings.TrimSpace(requestedArch)
	if arch == "" {
		arch = rootfs.DeviceArch()
	}
	repositories := append([]config.RootfsRepository(nil), s.rootfsRepos...)
	if len(repositories) == 0 {
		repositories = config.DefaultRootfsRepositories()
	}
	result := rootfsListCacheResult{
		Repositories:      repositories,
		TemplateImageRoot: s.templateImageRoot,
	}

	fingerprint := rootfsListCacheFingerprint(arch, repositories)
	cachePath := rootfsListCachePath(s.workspace, fingerprint)
	cached, _ := readRootfsListCache(cachePath, fingerprint)
	now := time.Now().UTC()
	if !forceRefresh && rootfsListCacheFresh(cached, now) {
		result.Assets = cached.Assets
		result.Cache = rootfsListCacheMetadata{CachedAt: cached.CachedAt}
		return result
	}

	assets, fetchErrors := s.rootfsClient.FetchAll(ctx, repositories, arch)
	if len(fetchErrors) == 0 {
		cachedAt := time.Now().UTC()
		entry := rootfsListCacheEntry{
			Version:     rootfsListCacheVersion,
			Fingerprint: fingerprint,
			CachedAt:    cachedAt,
			Assets:      assets,
		}
		// A cache write failure must not turn a successful upstream response into
		// an error. The next request simply fetches metadata again.
		_ = writeRootfsListCache(cachePath, entry)
		result.Assets = assets
		result.Cache = rootfsListCacheMetadata{CachedAt: cachedAt}
		return result
	}

	if cached != nil {
		result.Assets = cached.Assets
		result.Errors = append(fetchErrors, fmt.Sprintf("using stale cached rootfs list from %s", cached.CachedAt.Format(time.RFC3339)))
		result.Cache = rootfsListCacheMetadata{CachedAt: cached.CachedAt, Stale: true}
		return result
	}

	result.Assets = assets
	result.Errors = fetchErrors
	return result
}

func rootfsListCacheFingerprint(arch string, repositories []config.RootfsRepository) string {
	type repositoryIdentity struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	identity := struct {
		Version      int                  `json:"version"`
		Architecture string               `json:"architecture"`
		Repositories []repositoryIdentity `json:"repositories"`
	}{
		Version:      rootfsListCacheVersion,
		Architecture: strings.TrimSpace(arch),
		Repositories: make([]repositoryIdentity, 0, len(repositories)),
	}
	for _, repository := range repositories {
		identity.Repositories = append(identity.Repositories, repositoryIdentity{
			Name: strings.TrimSpace(repository.Name),
			URL:  strings.TrimSpace(repository.URL),
		})
	}
	encoded, _ := json.Marshal(identity)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func rootfsListCachePath(workspacePath string, fingerprint string) string {
	return filepath.Join(workspacePath, rootfsListCacheDirectory, "rootfs-list-"+fingerprint+".json")
}

func rootfsListCacheFresh(entry *rootfsListCacheEntry, now time.Time) bool {
	if entry == nil || entry.CachedAt.IsZero() || now.Before(entry.CachedAt) {
		return false
	}
	return now.Sub(entry.CachedAt) < rootfsListCacheTTL
}

func readRootfsListCache(path string, fingerprint string) (*rootfsListCacheEntry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entry rootfsListCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	if entry.Version != rootfsListCacheVersion || entry.Fingerprint != fingerprint || entry.CachedAt.IsZero() {
		return nil, fmt.Errorf("invalid rootfs list cache entry")
	}
	return &entry, nil
}

func writeRootfsListCache(path string, entry rootfsListCacheEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".rootfs-list-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
