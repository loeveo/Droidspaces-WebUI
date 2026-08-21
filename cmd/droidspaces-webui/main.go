package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/web"
)

var webVersion = "dev"

func main() {
	configPath := flag.String("config", "", "path to the WebUI JSON config file")
	writeDefaultConfig := flag.Bool("write-default-config", false, "write a default config file and exit")
	listen := flag.String("listen", "", "HTTP listen address, overrides mode/host/port")
	mode := flag.String("mode", "", "listen mode: local or public")
	host := flag.String("host", "", "HTTP listen host")
	port := flag.Int("port", 0, "HTTP listen port")
	droidspaces := flag.String("droidspaces", "", "path to the droidspaces binary")
	token := flag.String("auth-token", "", "optional bearer token for API access")
	workspace := flag.String("workspace", "", "Droidspaces workspace path")
	corePath := flag.String("core-path", "", "Droidspaces core directory")
	imageRoot := flag.String("image-root", "", "root directory for container images")
	templateRoot := flag.String("template-image-root", "", "directory for template images")
	socketdEnabled := flag.Bool("socketd-enabled", true, "enable socketd backend")
	flag.Parse()

	var socketdOverride *bool
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "socketd-enabled" {
			socketdOverride = socketdEnabled
		}
	})

	if *writeDefaultConfig {
		if err := config.WriteDefault(*configPath); err != nil {
			log.Fatal(err)
		}
		path := *configPath
		if path == "" {
			path = config.DefaultPath()
		}
		fmt.Printf("Wrote default config: %s\n", path)
		return
	}

	cfg, usedConfigPath, err := config.Load(*configPath, config.CLIOverrides{
		Listen:          *listen,
		DroidspacesPath: *droidspaces,
		AuthToken:       *token,
		Workspace:       *workspace,
		Mode:            *mode,
		Host:            *host,
		Port:            *port,
		CorePath:        *corePath,
		ImageRoot:       *imageRoot,
		TemplateRoot:    *templateRoot,
		SocketdEnabled:  socketdOverride,
	})
	if err != nil {
		log.Fatal(err)
	}

	server, err := web.NewServer(web.Options{
		DroidspacesPath:          cfg.DroidspacesPath,
		WebVersion:               webVersion,
		SupportedCoreVersion:     web.DefaultSupportedCoreVersion,
		AuthToken:                cfg.AuthToken,
		Workspace:                cfg.Workspace,
		ConfigPath:               usedConfigPath,
		Mode:                     cfg.Mode,
		Host:                     cfg.Host,
		Port:                     cfg.Port,
		CorePath:                 cfg.CorePath,
		ImageRoot:                cfg.ImageRoot,
		TemplateImageRoot:        cfg.TemplateImageRoot,
		SocketdEnabled:           boolValue(cfg.SocketdEnabled),
		RootfsRepos:              cfg.RootfsRepositories,
		RootfsSkipTLSVerify:      cfg.RootfsSkipTLSVerify,
		DefaultNATCIDR:           cfg.DefaultNATCIDR,
		DefaultNATThirdOctet:     cfg.DefaultNATThirdOctet,
		NestedAndroidNATCompat:   cfg.NestedAndroidNATCompat,
		BatteryDirectPower:       cfg.BatteryDirectPower,
		BatterySeriesCells:       cfg.BatterySeriesCells,
		OverviewPowerEnabled:     &cfg.OverviewPowerEnabled,
		BatteryMonitoringEnabled: &cfg.BatteryMonitoringEnabled,
		BatteryDetailEnabled:     &cfg.BatteryDetailEnabled,
		BatteryStatsSampleSecs:   cfg.BatteryStatsSampleSecs,
		BatteryStatsWriteMins:    cfg.BatteryStatsWriteMins,
		OverviewRefreshSecs:      cfg.OverviewRefreshSecs,
	})
	if err != nil {
		log.Fatal(err)
	}

	if cfg.AuthToken == "" {
		log.Printf("auth token disabled; keep the listener on localhost unless you trust the network")
	}

	addr := cfg.ListenAddr()
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("Droidspaces WebUI listening on http://%s", addr)
	log.Printf("Config file: %s", usedConfigPath)
	if cfg.GeneratedAuthToken {
		fmt.Printf("Generated temporary auth token: %s\n", cfg.AuthToken)
		fmt.Printf("Set authToken in the WebUI settings or config file to make it persistent.\n")
	}
	listenErr := make(chan error, 1)
	go func() { listenErr <- httpServer.ListenAndServe() }()

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-listenErr:
		if err != nil && err != http.ErrServerClosed {
			log.Fatal(err)
		}
	case signalValue := <-signals:
		log.Printf("received %s; stopping WebUI", signalValue)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			log.Printf("WebUI background cleanup: %v", err)
		}
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("HTTP shutdown: %v", err)
		}
	}
}

func boolValue(v *bool) bool {
	if v == nil {
		return false
	}
	return *v
}
