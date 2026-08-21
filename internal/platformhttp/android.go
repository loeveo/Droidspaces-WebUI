package platformhttp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
)

var androidDNSServers = []string{"1.1.1.1:53", "8.8.8.8:53"}

// ConfigureAndroidTransport supplies DNS and trust-store support missing from
// statically linked Go programs on Android. It is a no-op on other platforms.
func ConfigureAndroidTransport(transport *http.Transport) {
	if transport == nil || !config.IsAndroid() {
		return
	}
	transport.DialContext = androidDialContext()
	if roots := androidSystemCertPool(); roots != nil {
		cfg := cloneTLSConfig(transport.TLSClientConfig)
		cfg.MinVersion = tls.VersionTLS12
		cfg.RootCAs = roots
		transport.TLSClientConfig = cfg
	}
}

func cloneTLSConfig(existing *tls.Config) *tls.Config {
	if existing == nil {
		return &tls.Config{}
	}
	return existing.Clone()
}

func androidSystemCertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	loaded := 0
	for _, directory := range []string{"/system/etc/security/cacerts", "/apex/com.android.conscrypt/cacerts"} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			data, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil || len(data) == 0 || len(data) > 1<<20 {
				continue
			}
			if pool.AppendCertsFromPEM(data) {
				loaded++
			}
		}
	}
	if loaded == 0 {
		return nil
	}
	return pool
}

func androidDialContext() func(context.Context, string, string) (net.Conn, error) {
	standardDialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	fallbackDialer := &net.Dialer{
		Timeout:   20 * time.Second,
		KeepAlive: 30 * time.Second,
		Resolver: &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network string, _ string) (net.Conn, error) {
				var lastErr error
				for _, server := range androidDNSServers {
					conn, err := standardDialer.DialContext(ctx, network, server)
					if err == nil {
						return conn, nil
					}
					lastErr = err
				}
				if lastErr == nil {
					lastErr = fmt.Errorf("no Android DNS fallback server configured")
				}
				return nil, lastErr
			},
		},
	}
	return func(ctx context.Context, network string, address string) (net.Conn, error) {
		conn, err := standardDialer.DialContext(ctx, network, address)
		if err == nil || !isDNSLookupError(err) {
			return conn, err
		}
		return fallbackDialer.DialContext(ctx, network, address)
	}
}

func isDNSLookupError(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}
