package bridge

import "fmt"

// Config holds the configuration for a single tool backend.
type Config struct {
	Command    string
	Args       []string
	Env        map[string]string
	WorkingDir string
}

// Validate ensures the config is valid.
func (c *Config) Validate() error {
	if c.Command == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}
