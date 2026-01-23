package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/michaelquigley/df/dd"
	"github.com/michaelquigley/df/dl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openziti/mcp-gateway/gateway"
	"github.com/openziti/mcp-gateway/model"
)

// Bridge wraps a single stdio MCP server and exposes it via zrok.
// each incoming client gets its own subprocess for full isolation.
type Bridge struct {
	cfg        *Config
	tools      []*mcp.Tool // discovered at startup (immutable)
	share      *gateway.Share
	httpServer *http.Server
	mu         sync.Mutex
	sessions   map[string]*bridgeSession
}

// bridgeSession represents one client's connection to a dedicated backend subprocess.
type bridgeSession struct {
	id         string
	createdAt  time.Time
	remoteAddr string
	userAgent  string
	client     *mcp.Client
	session    *mcp.ClientSession
	cmd        *exec.Cmd
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates a Bridge from config.
func New(cfg *Config) (*Bridge, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &Bridge{
		cfg:      cfg,
		sessions: make(map[string]*bridgeSession),
	}, nil
}

// Start discovers tools from a temporary backend, creates zrok share, and outputs token.
func (b *Bridge) Start(ctx context.Context) error {
	dl.Log().With("command", b.cfg.Command).Info("starting mcp-bridge")

	// discover tools from a temporary backend
	tools, err := b.discoverTools(ctx)
	if err != nil {
		return fmt.Errorf("failed to discover tools: %w", err)
	}
	b.tools = tools

	dl.Log().With("tool_count", len(tools)).Info("discovered tools from backend")

	// create zrok share
	share, err := gateway.NewShare()
	if err != nil {
		return fmt.Errorf("failed to create share: %w", err)
	}
	b.share = share

	// create HTTP server with per-request subprocess spawning
	b.httpServer = b.createHTTPServer()

	// output share token to stdout
	if json, err := dd.UnbindJSON(&model.TokenOutput{ShareToken: share.Token()}); err == nil {
		fmt.Println(string(json))
	} else {
		dl.Log().With("error", err).Warn("failed to unbind share token")
	}

	dl.Log().With("share_token", share.Token()).Info("mcp-bridge started")
	return nil
}

// discoverTools spawns a temporary backend to discover available tools.
func (b *Bridge) discoverTools(ctx context.Context) ([]*mcp.Tool, error) {
	mcpClient := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mcp-bridge",
			Version: "1.0.0",
		},
		nil,
	)

	// build command for stdio transport
	cmd := exec.CommandContext(ctx, b.cfg.Command, b.cfg.Args...)
	if b.cfg.WorkingDir != "" {
		cmd.Dir = b.cfg.WorkingDir
	}

	// set environment variables
	cmd.Env = os.Environ()
	for k, v := range b.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// create transport and connect
	transport := &mcp.CommandTransport{Command: cmd}
	session, err := mcpClient.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to backend: %w", err)
	}

	// discover tools
	toolsResult, err := session.ListTools(ctx, nil)
	if err != nil {
		session.Close()
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	// close discovery session - per-client sessions will spawn their own
	if err := session.Close(); err != nil {
		dl.Log().With("error", err).Debug("error closing discovery session")
	}

	return toolsResult.Tools, nil
}

// createHTTPServer creates an HTTP server that spawns per-client subprocesses.
func (b *Bridge) createHTTPServer() *http.Server {
	handler := mcp.NewSSEHandler(func(r *http.Request) *mcp.Server {
		// spawn new subprocess for this client
		session, err := b.createBridgeSession(r.Context(), r.RemoteAddr, r.Header.Get("User-Agent"))
		if err != nil {
			dl.Log().With("error", err).Error("failed to create bridge session")
			return nil
		}

		// cleanup when client disconnects
		go func() {
			<-r.Context().Done()
			b.removeBridgeSession(session.id)
		}()

		return session.createProxyServer(b.tools)
	}, nil)

	return &http.Server{
		Handler: handler,
	}
}

// createBridgeSession spawns a new subprocess and connects to it.
func (b *Bridge) createBridgeSession(ctx context.Context, remoteAddr, userAgent string) (*bridgeSession, error) {
	sessionCtx, cancel := context.WithCancel(ctx)
	sessionID := uuid.New().String()

	mcpClient := mcp.NewClient(
		&mcp.Implementation{
			Name:    "mcp-bridge",
			Version: "1.0.0",
		},
		nil,
	)

	// build command for this session
	cmd := exec.CommandContext(sessionCtx, b.cfg.Command, b.cfg.Args...)
	if b.cfg.WorkingDir != "" {
		cmd.Dir = b.cfg.WorkingDir
	}

	// set environment variables
	cmd.Env = os.Environ()
	for k, v := range b.cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	// create transport and connect
	transport := &mcp.CommandTransport{Command: cmd}
	session, err := mcpClient.Connect(sessionCtx, transport, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to backend: %w", err)
	}

	bs := &bridgeSession{
		id:         sessionID,
		createdAt:  time.Now(),
		remoteAddr: remoteAddr,
		userAgent:  userAgent,
		client:     mcpClient,
		session:    session,
		cmd:        cmd,
		ctx:        sessionCtx,
		cancel:     cancel,
	}

	// track session
	b.mu.Lock()
	b.sessions[sessionID] = bs
	b.mu.Unlock()

	dl.Log().
		With("session_id", sessionID).
		With("remote_addr", remoteAddr).
		With("user_agent", userAgent).
		Info("client session started")

	return bs, nil
}

// removeBridgeSession closes and removes a session.
func (b *Bridge) removeBridgeSession(sessionID string) {
	b.mu.Lock()
	session, ok := b.sessions[sessionID]
	if ok {
		delete(b.sessions, sessionID)
	}
	b.mu.Unlock()

	if ok {
		session.Close()
		dl.Log().With("session_id", sessionID).Debug("removed bridge session")
	}
}

// createProxyServer creates an MCP server that proxies to this session's backend.
func (bs *bridgeSession) createProxyServer(tools []*mcp.Tool) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{
			Name:    "mcp-bridge",
			Version: "1.0.0",
		},
		nil,
	)

	for _, tool := range tools {
		t := tool
		server.AddTool(t, bs.createProxyHandler(t.Name))
	}

	return server
}

// createProxyHandler creates a handler that forwards tool calls to this session's backend.
func (bs *bridgeSession) createProxyHandler(toolName string) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()

		result, err := bs.session.CallTool(ctx, &mcp.CallToolParams{
			Name:      toolName,
			Arguments: req.Params.Arguments,
		})
		duration := time.Since(start)

		if err != nil {
			dl.Log().
				With("session_id", bs.id).
				With("tool", toolName).
				With("args", summarizeArgs(req.Params.Arguments)).
				With("duration_ms", duration.Milliseconds()).
				With("error", err.Error()).
				Info("tool call failed")
			return nil, err
		}

		dl.Log().
			With("session_id", bs.id).
			With("tool", toolName).
			With("args", summarizeArgs(req.Params.Arguments)).
			With("duration_ms", duration.Milliseconds()).
			With("result_type", getResultType(result)).
			Info("tool call succeeded")
		return result, nil
	}
}

// Close cleans up the bridge session and terminates the subprocess.
func (bs *bridgeSession) Close() error {
	var errs []error

	// cancel context
	bs.cancel()

	// close MCP session
	if bs.session != nil {
		if err := bs.session.Close(); err != nil {
			errs = append(errs, fmt.Errorf("closing session: %w", err))
		}
	}

	// terminate subprocess with graceful shutdown
	if bs.cmd != nil && bs.cmd.Process != nil {
		// send SIGTERM first
		if err := bs.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			dl.Log().With("session_id", bs.id).With("error", err).Debug("sigterm failed, trying sigkill")
			bs.cmd.Process.Kill()
		} else {
			// wait for process to exit with timeout
			done := make(chan error, 1)
			go func() { done <- bs.cmd.Wait() }()

			select {
			case <-done:
				// process exited cleanly
			case <-time.After(5 * time.Second):
				dl.Log().With("session_id", bs.id).Debug("process did not exit after sigterm, sending sigkill")
				bs.cmd.Process.Kill()
			}
		}
	}

	dl.Log().
		With("session_id", bs.id).
		With("duration_ms", time.Since(bs.createdAt).Milliseconds()).
		Info("client session ended")

	return errors.Join(errs...)
}

// Run serves MCP over the zrok share.
// this blocks until the context is cancelled.
func (b *Bridge) Run(ctx context.Context) error {
	dl.Log().Info("serving MCP over zrok share")

	// serve in a goroutine so we can handle context cancellation
	errCh := make(chan error, 1)
	go func() {
		err := b.httpServer.Serve(b.share.Listener())
		if errors.Is(err, http.ErrServerClosed) {
			errCh <- nil
		} else {
			errCh <- err
		}
	}()

	// wait for context cancellation or server error
	select {
	case <-ctx.Done():
		dl.Log().Info("context cancelled, shutting down")
		return b.httpServer.Shutdown(context.Background())
	case err := <-errCh:
		return err
	}
}

// Stop gracefully shuts down the bridge.
func (b *Bridge) Stop() error {
	dl.Log().Info("stopping mcp-bridge")

	var lastErr error

	if b.httpServer != nil {
		if err := b.httpServer.Shutdown(context.Background()); err != nil {
			dl.Log().With("error", err).Warn("error shutting down server")
			lastErr = err
		}
	}

	if b.share != nil {
		if err := b.share.Close(); err != nil {
			dl.Log().With("error", err).Warn("error closing share")
			lastErr = err
		}
	}

	// close all remaining sessions
	b.mu.Lock()
	sessions := make([]*bridgeSession, 0, len(b.sessions))
	for _, s := range b.sessions {
		sessions = append(sessions, s)
	}
	b.sessions = make(map[string]*bridgeSession)
	b.mu.Unlock()

	for _, s := range sessions {
		if err := s.Close(); err != nil {
			dl.Log().With("session_id", s.id).With("error", err).Warn("error closing session")
			lastErr = err
		}
	}

	dl.Log().Info("mcp-bridge stopped")
	return lastErr
}

// ShareToken returns the share token after Start().
func (b *Bridge) ShareToken() string {
	if b.share == nil {
		return ""
	}
	return b.share.Token()
}

// summarizeArgs creates a loggable summary of tool arguments.
// truncates long values to avoid log bloat.
func summarizeArgs(args any) string {
	if args == nil {
		return "{}"
	}
	data, err := json.Marshal(args)
	if err != nil {
		return "<marshal error>"
	}
	if len(data) > 500 {
		return string(data[:500]) + "..."
	}
	return string(data)
}

// getResultType extracts a summary of the result for logging.
func getResultType(result *mcp.CallToolResult) string {
	if result == nil {
		return "nil"
	}
	if result.IsError {
		return "error"
	}
	if len(result.Content) == 0 {
		return "empty"
	}
	// summarize content types by checking interface types
	types := make([]string, 0, len(result.Content))
	for _, c := range result.Content {
		switch c.(type) {
		case *mcp.TextContent:
			types = append(types, "text")
		case *mcp.ImageContent:
			types = append(types, "image")
		case *mcp.AudioContent:
			types = append(types, "audio")
		case *mcp.EmbeddedResource:
			types = append(types, "resource")
		default:
			types = append(types, "unknown")
		}
	}
	return strings.Join(types, ",")
}
