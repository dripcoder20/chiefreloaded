package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultLoopUsageThresholds(t *testing.T) {
	cfg := DefaultLoop()
	if cfg.Usage.ContextWarnPercent != DefaultContextWarnPercent {
		t.Errorf("warn percent = %g, want %g", cfg.Usage.ContextWarnPercent, float64(DefaultContextWarnPercent))
	}
	if cfg.Usage.ContextCriticalPercent != DefaultContextCriticalPercent {
		t.Errorf("critical percent = %g, want %g", cfg.Usage.ContextCriticalPercent, float64(DefaultContextCriticalPercent))
	}
	if cfg.Usage.CostWarnAmount != nil {
		t.Errorf("cost warn amount = %v, want nil", *cfg.Usage.CostWarnAmount)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should validate, got %v", err)
	}
}

func TestUsageValidate(t *testing.T) {
	negative := -1.0
	zeroCost := 0.0
	cases := []struct {
		name  string
		usage UsageConfig
		valid bool
	}{
		{"defaults", UsageConfig{ContextWarnPercent: 80, ContextCriticalPercent: 95}, true},
		{"custom valid", UsageConfig{ContextWarnPercent: 50, ContextCriticalPercent: 90, CostWarnAmount: &zeroCost}, true},
		{"critical equals warning", UsageConfig{ContextWarnPercent: 80, ContextCriticalPercent: 80}, false},
		{"critical below warning", UsageConfig{ContextWarnPercent: 90, ContextCriticalPercent: 80}, false},
		{"warn at zero", UsageConfig{ContextWarnPercent: 0, ContextCriticalPercent: 95}, false},
		{"warn at hundred", UsageConfig{ContextWarnPercent: 100, ContextCriticalPercent: 95}, false},
		{"critical above hundred", UsageConfig{ContextWarnPercent: 80, ContextCriticalPercent: 101}, false},
		{"negative cost", UsageConfig{ContextWarnPercent: 80, ContextCriticalPercent: 95, CostWarnAmount: &negative}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.usage.validate()
			if c.valid && err != nil {
				t.Errorf("expected valid, got %v", err)
			}
			if !c.valid && err == nil {
				t.Error("expected invalid, got nil")
			}
		})
	}
}

func TestNormaliseFillsUnsetUsagePercents(t *testing.T) {
	c := &LoopConfig{}
	c.Normalise()
	if c.Usage.ContextWarnPercent != DefaultContextWarnPercent {
		t.Errorf("warn percent = %g, want default", c.Usage.ContextWarnPercent)
	}
	if c.Usage.ContextCriticalPercent != DefaultContextCriticalPercent {
		t.Errorf("critical percent = %g, want default", c.Usage.ContextCriticalPercent)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("normalised zero-config should validate, got %v", err)
	}
}

func TestLoadLoopMigratesLegacyConfigWithoutUsageBlock(t *testing.T) {
	dir := t.TempDir()
	writeConfig(t, dir, "worktree:\n  setup: npm ci\n")

	cfg, err := LoadLoop(dir)
	if err != nil {
		t.Fatalf("LoadLoop: %v", err)
	}
	if cfg.Usage.ContextWarnPercent != DefaultContextWarnPercent {
		t.Errorf("warn percent = %g, want default", cfg.Usage.ContextWarnPercent)
	}
	if cfg.Usage.ContextCriticalPercent != DefaultContextCriticalPercent {
		t.Errorf("critical percent = %g, want default", cfg.Usage.ContextCriticalPercent)
	}
}

func TestSaveAndLoadLoopRoundTripsUsage(t *testing.T) {
	dir := t.TempDir()
	amount := 12.5
	cfg := DefaultLoop()
	cfg.Usage = UsageConfig{ContextWarnPercent: 70, ContextCriticalPercent: 90, CostWarnAmount: &amount}

	if err := SaveLoop(dir, cfg); err != nil {
		t.Fatalf("SaveLoop: %v", err)
	}
	loaded, err := LoadLoop(dir)
	if err != nil {
		t.Fatalf("LoadLoop: %v", err)
	}
	if loaded.Usage.ContextWarnPercent != 70 || loaded.Usage.ContextCriticalPercent != 90 {
		t.Errorf("percents = %g/%g, want 70/90", loaded.Usage.ContextWarnPercent, loaded.Usage.ContextCriticalPercent)
	}
	if loaded.Usage.CostWarnAmount == nil || *loaded.Usage.CostWarnAmount != amount {
		t.Errorf("cost warn amount = %v, want %g", loaded.Usage.CostWarnAmount, amount)
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	chiefDir := filepath.Join(dir, ".chief")
	if err := os.MkdirAll(chiefDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chiefDir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
