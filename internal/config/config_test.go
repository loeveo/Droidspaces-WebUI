package config

import "testing"

func TestPublicModeRequiresAuthToken(t *testing.T) {
	cfg := Default()
	cfg.Mode = ModePublic
	cfg.AuthToken = ""

	if err := normalize(&cfg); err == nil {
		t.Fatal("expected public mode without authToken to fail")
	}
}

func TestImageRootsDefaultFromCorePath(t *testing.T) {
	cfg := Config{
		Mode:            ModeLocal,
		Host:            "127.0.0.1",
		Port:            9090,
		DroidspacesPath: "/data/local/Droidspaces/bin/droidspaces",
	}

	if err := normalize(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.CorePath != "/data/local/Droidspaces/bin" {
		t.Fatalf("CorePath = %q", cfg.CorePath)
	}
	if cfg.ImageRoot != cfg.CorePath {
		t.Fatalf("ImageRoot = %q, want %q", cfg.ImageRoot, cfg.CorePath)
	}
	if cfg.TemplateImageRoot != "/data/local/Droidspaces/bin/templates" {
		t.Fatalf("TemplateImageRoot = %q", cfg.TemplateImageRoot)
	}
}
