package gateway

import (
	"fmt"
	"time"

	"github.com/michaelquigley/df/dd"
	"github.com/openziti/mcp-gateway/aggregator"
)

// Config represents the share backend configuration.
type Config struct {
	Aggregator   aggregator.AggregatorConfig
	Backends     []aggregator.BackendConfig
	ShareToken   string // if set, use existing share (managed mode)
	Orchestrator *OrchestratorConfig
	LogFile      string // if set, redirect logging to this file
}

// OrchestratorConfig holds configuration for connecting to the orchestrator.
type OrchestratorConfig struct {
	SocketPath        string
	HeartbeatInterval time.Duration
}

// DefaultOrchestratorConfig returns default orchestrator connection configuration.
func DefaultOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		SocketPath:        "/var/run/mcp-orchestrator/orchestrator.sock",
		HeartbeatInterval: 30 * time.Second,
	}
}

// DefaultConfig returns a Config with defaults pre-populated.
func DefaultConfig() *Config {
	aggDefaults := aggregator.DefaultConfig()
	return &Config{
		Aggregator: aggDefaults.Aggregator,
	}
}

// LoadConfig loads configuration from a YAML file, merging into defaults.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if err := dd.MergeYAMLFile(cfg, path); err != nil {
		return nil, &ConfigError{Field: "file", Message: fmt.Sprintf("failed to load '%s': %v", path, err)}
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate checks the configuration for errors.
func (c *Config) Validate() error {
	if len(c.Backends) == 0 {
		return &ConfigError{Field: "backends", Message: "at least one backend is required"}
	}

	// validate backends using aggregator's validation logic
	aggCfg := c.toAggregatorConfig()
	if err := aggCfg.Validate(); err != nil {
		return err
	}

	return nil
}

// toAggregatorConfig converts to an aggregator.Config for reuse.
func (c *Config) toAggregatorConfig() *aggregator.Config {
	return &aggregator.Config{
		Aggregator: c.Aggregator,
		Backends:   c.Backends,
	}
}

// ConfigError represents a configuration validation error.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("configuration error in '%s': %s", e.Field, e.Message)
}
