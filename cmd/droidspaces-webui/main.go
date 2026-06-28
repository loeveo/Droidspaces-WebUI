package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ravindu644/droidspaces-oss/webui/internal/config"
	"github.com/ravindu644/droidspaces-oss/webui/internal/web"
)

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
	flag.Parse()

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
	})
	if err != nil {
		log.Fatal(err)
	}

	server, err := web.NewServer(web.Options{
		DroidspacesPath:     cfg.DroidspacesPath,
		AuthToken:           cfg.AuthToken,
		Workspace:           cfg.Workspace,
		ConfigPath:          usedConfigPath,
		Mode:                cfg.Mode,
		CorePath:            cfg.CorePath,
		ImageRoot:           cfg.ImageRoot,
		TemplateImageRoot:   cfg.TemplateImageRoot,
		RootfsRepos:         cfg.RootfsRepositories,
		RootfsSkipTLSVerify: cfg.RootfsSkipTLSVerify,
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

	fmt.Printf("Droidspaces WebUI listening on http://%s\n", addr)
	fmt.Printf("Config file: %s\n", usedConfigPath)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
