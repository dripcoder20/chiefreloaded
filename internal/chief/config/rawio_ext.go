package config

// Loop's file I/O for the extended config. Separate from config_ext.go only to
// keep the policy in one file and the plumbing in another.

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// rawConfig reads the file with Loop's block optional, so a config written by
// chief — which has no `git:` key — is distinguishable from one where the user
// deliberately wrote defaults. The pointer is the whole point: absent and
// present-but-empty need different handling on migration.
type rawConfig struct {
	Config `yaml:",inline"`
	Git    *GitConfig   `yaml:"git,omitempty"`
	Usage  *UsageConfig `yaml:"usage,omitempty"`
}

func loadRaw(baseDir string) (*rawConfig, error) {
	data, err := os.ReadFile(configPath(baseDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func saveRaw(baseDir string, cfg *LoopConfig) error {
	path := configPath(baseDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	raw := rawConfig{Config: cfg.Config, Git: &cfg.Git, Usage: &cfg.Usage}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
