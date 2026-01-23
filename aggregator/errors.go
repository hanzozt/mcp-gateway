package aggregator

import "fmt"

// ConfigError represents a configuration validation error.
type ConfigError struct {
	Field   string
	Message string
}

func (e *ConfigError) Error() string {
	return fmt.Sprintf("configuration error in '%s': %s", e.Field, e.Message)
}

// BackendError represents an error related to a backend MCP server.
type BackendError struct {
	BackendID string
	Op        string
	Err       error
}

func (e *BackendError) Error() string {
	return fmt.Sprintf("backend '%s' %s error: %v", e.BackendID, e.Op, e.Err)
}

func (e *BackendError) Unwrap() error {
	return e.Err
}

// ToolNotFoundError indicates a requested tool does not exist.
type ToolNotFoundError struct {
	Name string
}

func (e *ToolNotFoundError) Error() string {
	return fmt.Sprintf("tool '%s' not found", e.Name)
}

// RoutingError indicates a failure in routing a tool call.
type RoutingError struct {
	ToolName string
	Message  string
}

func (e *RoutingError) Error() string {
	return fmt.Sprintf("routing error for tool '%s': %s", e.ToolName, e.Message)
}
