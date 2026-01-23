package aggregator

import (
	"context"
	"fmt"

	"github.com/michaelquigley/df/dl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Aggregator orchestrates backend connections, namespacing, and the MCP server.
type Aggregator struct {
	config    *Config
	backends  *BackendManager
	namespace *Namespace
	server    *Server
}

// New creates a new Aggregator from configuration.
func New(cfg *Config) (*Aggregator, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &Aggregator{
		config:    cfg,
		backends:  NewBackendManager(cfg),
		namespace: NewNamespace(cfg.Aggregator.Separator),
	}, nil
}

// NewFromFile creates a new Aggregator by loading configuration from a YAML file.
func NewFromFile(path string) (*Aggregator, error) {
	cfg, err := LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return New(cfg)
}

// Start initializes connections to all backends and builds the tool namespace.
// this is where structural enforcement happens: tools are filtered during initialization.
func (a *Aggregator) Start(ctx context.Context) error {
	dl.Info("starting aggregator")

	// connect to all backends (fail-fast on any connection error)
	if err := a.backends.Connect(ctx); err != nil {
		return err
	}

	// build the namespace with filtered tools from each backend
	for _, bcfg := range a.config.Backends {
		backend, ok := a.backends.GetBackend(bcfg.ID)
		if !ok {
			continue
		}
		a.namespace.AddTools(bcfg.ID, backend.Tools(), &bcfg.Tools)
	}

	// create the server with the populated namespace
	a.server = NewServer(a.config, a.namespace, a.backends)

	dl.Log().With("backends", len(a.config.Backends)).With("tools", a.namespace.Count()).Info("aggregator started")
	return nil
}

// Run starts the MCP server on stdio transport.
func (a *Aggregator) Run(ctx context.Context) error {
	if a.server == nil {
		return fmt.Errorf("aggregator not started, call Start() first")
	}

	transport := &mcp.StdioTransport{}
	dl.Info("running aggregator on stdio")
	return a.server.Run(ctx, transport)
}

// Stop gracefully shuts down the aggregator.
func (a *Aggregator) Stop() error {
	dl.Info("stopping aggregator")
	return a.backends.Close()
}

// Tools returns all aggregated, namespaced tools.
func (a *Aggregator) Tools() []mcp.Tool {
	return a.namespace.AllTools()
}

// Config returns the aggregator configuration.
func (a *Aggregator) Config() *Config {
	return a.config
}

// Namespace returns the aggregator's namespace for inspection.
func (a *Aggregator) Namespace() *Namespace {
	return a.namespace
}

// Backends returns the backend manager for inspection.
func (a *Aggregator) Backends() *BackendManager {
	return a.backends
}

// ToolCount returns the number of registered tools.
func (a *Aggregator) ToolCount() int {
	return a.namespace.Count()
}

// CallTool invokes a tool by its namespaced name.
func (a *Aggregator) CallTool(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if a.server == nil {
		return nil, fmt.Errorf("aggregator not started, call Start() first")
	}
	return a.server.CallTool(ctx, req)
}
