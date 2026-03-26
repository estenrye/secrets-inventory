package config

import (
	"errors"
	"strings"
)

type Config struct {
	GitHub  GitHubConfig  `yaml:"github"`
	Targets Targets       `yaml:"targets"`
	Scanner ScannerConfig `yaml:"scanner"`
}

type GitHubConfig struct {
	Token   string `yaml:"token"`
	BaseURL string `yaml:"base_url"`
}

type Targets struct {
	Orgs  []string `yaml:"orgs"`
	Users []string `yaml:"users"`
	Repos []string `yaml:"repos"` // entries like owner/name
}

type ScannerConfig struct {
	ScriptExtensions []string `yaml:"script_extensions"`
	MaxFileBytes     int64    `yaml:"max_file_bytes"`
	IncludeUnknownEnv bool    `yaml:"include_unknown_env"`
}

func (c *Config) Normalize() error {
	for i := range c.Targets.Orgs {
		c.Targets.Orgs[i] = strings.TrimSpace(c.Targets.Orgs[i])
	}
	for i := range c.Targets.Users {
		c.Targets.Users[i] = strings.TrimSpace(c.Targets.Users[i])
	}
	for i := range c.Targets.Repos {
		c.Targets.Repos[i] = strings.TrimSpace(c.Targets.Repos[i])
	}
	if c.Scanner.MaxFileBytes == 0 {
		c.Scanner.MaxFileBytes = 512 * 1024
	}
	if len(c.Scanner.ScriptExtensions) == 0 {
		c.Scanner.ScriptExtensions = []string{".sh", ".bash", ".zsh", ".py", ".js", ".ts"}
	}
	if len(c.Targets.Orgs) == 0 && len(c.Targets.Users) == 0 && len(c.Targets.Repos) == 0 {
		return errors.New("config.targets must include at least one of orgs/users/repos")
	}
	return nil
}
