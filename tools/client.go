package tools

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Client bridges stdio MCP to a remote backend over zrok.
type Client struct {
	shareToken string
	access     *Access
	mcpClient  *mcp.Client
	session    *mcp.ClientSession
	server     *mcp.Server
}

// New creates a Client for the given share token.
func New(shareToken string) (*Client, error) {
	if shareToken == "" {
		return nil, fmt.Errorf("share token is required")
	}

	return &Client{
		shareToken: shareToken,
	}, nil
}

// Start connects to the backend and discovers tools.
func (c *Client) Start(ctx context.Context) error {
	dl.Log().With("shareToken", c.shareToken).Debug("starting mcp-tools")

	// create zrok access
	access, err := NewAccess(c.shareToken)
	if err != nil {
		return fmt.Errorf("failed to create access: %w", err)
	}
	c.access = access

	// create MCP client
	c.mcpClient = mcp.NewClient(
		&mcp.Implementation{
			Name:    "mcp-tools",
			Version: "1.0.0",
		},
		nil,
	)

	// create SSE transport using zrok HTTP client
	sseTransport := &mcp.SSEClientTransport{
		// the host doesn't matter for routing since zrok handles it
		Endpoint:   "http://mcp-backend/sse",
		HTTPClient: access.HTTPClient(),
	}

	// connect to backend
	session, err := c.mcpClient.Connect(ctx, sseTransport, nil)
	if err != nil {
		c.access.Close()
		return fmt.Errorf("failed to connect to backend: %w", err)
	}
	c.session = session

	dl.Log().Debug("connected to backend")

	// discover tools from backend
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		c.session.Close()
		c.access.Close()
		return fmt.Errorf("failed to list tools: %w", err)
	}

	dl.Log().With("toolCount", len(toolsResult.Tools)).Debug("discovered tools from backend")

	// create proxy server with discovered tools
	c.server = c.createProxyServer(toolsResult.Tools)

	dl.Log().Debug("mcp-tools started")
	return nil
}

// createProxyServer creates an MCP server that proxies to the backend.
func (c *Client) createProxyServer(tools []*mcp.Tool) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "mcp-tools",
			Version: "1.0.0",
		},
		nil,
	)

	for _, tool := range tools {
		t := tool
		server.AddTool(t, c.createProxyHandler(t.Name))
		dl.Log().With("tool", t.Name).Debug("registered proxy handler")
	}

	return server
}

// createProxyHandler creates a handler that forwards tool calls to the backend.
func (c *Client) createProxyHandler(toolName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dl.Log().With("tool", toolName).Debug("forwarding tool call to backend")

		result, err := c.session.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: req.Params.Arguments,
		})
		if err != nil {
			dl.Log().With("tool", toolName).With("error", err).Debug("tool call failed")
			return nil, err
		}

		dl.Log().With("tool", toolName).Debug("tool call completed")
		return result, nil
	}
}

// Run serves MCP on stdio (blocks until context cancelled).
func (c *Client) Run(ctx context.Context) error {
	if c.server == nil {
		return fmt.Errorf("client not started, call Start() first")
	}

	dl.Log().Debug("serving mcp on stdio")

	return c.server.Run(ctx, &mcp.StdioTransport{})
}

// HTTPOptions configures the HTTP server mode.
type HTTPOptions struct {
	Address      string // bind address (e.g., "127.0.0.1:8080")
	Stateless    bool   // if true, don't track sessions
	JSONResponse bool   // if true, prefer JSON over SSE responses
}

// RunHTTP serves MCP over HTTP (blocks until context cancelled).
func (c *Client) RunHTTP(ctx context.Context, opts *HTTPOptions) error {
	if c.server == nil {
		return fmt.Errorf("client not started, call Start() first")
	}

	if opts == nil {
		opts = &HTTPOptions{Address: "127.0.0.1:8080"}
	}

	dl.Log().With("address", opts.Address).Debug("serving MCP on http")

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return c.server },
		&mcp.StreamableHTTPOptions{
			Stateless:    opts.Stateless,
			JSONResponse: opts.JSONResponse,
		},
	)

	httpServer := &http.Server{
		Addr:    opts.Address,
		Handler: handler,
	}

	// graceful shutdown on context cancellation
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop gracefully shuts down the client.
func (c *Client) Stop() error {
	dl.Log().Debug("stopping mcp-tools")

	var lastErr error

	if c.session != nil {
		if err := c.session.Close(); err != nil {
			dl.Log().With("error", err).Debug("error closing session")
			lastErr = err
		}
	}

	if c.access != nil {
		if err := c.access.Close(); err != nil {
			dl.Log().With("error", err).Debug("error closing access")
			lastErr = err
		}
	}

	dl.Log().Debug("mcp-tools stopped")
	return lastErr
}
