package ipc

import (
	"context"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/michaelquigley/df/dl"
	"github.com/hanzozt/mcp-gateway/ipc/gatewayGrpc"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
)

// Config holds gateway IPC client configuration.
type Config struct {
	SocketPath        string
	HeartbeatInterval time.Duration
	ReconnectInterval time.Duration
}

// DefaultConfig returns default gateway IPC client configuration.
func DefaultConfig() *Config {
	return &Config{
		SocketPath:        "/var/run/mcp-orchestrator/orchestrator.sock",
		HeartbeatInterval: 30 * time.Second,
		ReconnectInterval: 5 * time.Second,
	}
}

// Client implements the gateway-side IPC client for communicating with the orchestrator.
type Client struct {
	cfg        *Config
	shareToken string
	conn       *grpc.ClientConn
	stream     gatewayGrpc.GatewayIPC_ConnectClient
	sendCh     chan *gatewayGrpc.GatewayMessage
	shutdownCh chan string
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	mu         sync.Mutex
	connected  bool
	version    string

	// reconnection state
	reconnecting bool
	reconnectMu  sync.Mutex
	parentCtx    context.Context // parent context for reconnection loop

	// OnReconnect is called after successful reconnection to allow the caller
	// to restart heartbeats and other dependent goroutines
	OnReconnect func()

	// metrics for heartbeat reporting
	activeConnections int32
	toolInvocations   int64
	metricsMu         sync.RWMutex
}

// NewClient creates a new gateway IPC client.
func NewClient(shareToken string, cfg *Config) *Client {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return &Client{
		cfg:        cfg,
		shareToken: shareToken,
		sendCh:     make(chan *gatewayGrpc.GatewayMessage, 16),
		shutdownCh: make(chan string, 1),
	}
}

// Connect establishes a connection to the orchestrator.
func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		return errors.New("already connected")
	}

	// store parent context for reconnection loop
	c.parentCtx = ctx

	// check if socket exists
	if _, err := os.Stat(c.cfg.SocketPath); os.IsNotExist(err) {
		return errors.Errorf("orchestrator socket not found: %s", c.cfg.SocketPath)
	}

	opts := []grpc.DialOption{
		grpc.WithContextDialer(func(_ context.Context, addr string) (net.Conn, error) {
			return net.Dial("unix", addr)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	resolver.SetDefaultScheme("passthrough")

	conn, err := grpc.NewClient(c.cfg.SocketPath, opts...)
	if err != nil {
		return errors.Wrap(err, "error connecting to orchestrator")
	}
	c.conn = conn

	client := gatewayGrpc.NewGatewayIPCClient(conn)

	c.ctx, c.cancel = context.WithCancel(ctx)
	stream, err := client.Connect(c.ctx)
	if err != nil {
		conn.Close()
		return errors.Wrap(err, "error opening stream to orchestrator")
	}
	c.stream = stream
	c.connected = true

	// recreate send channel for new connection
	c.sendCh = make(chan *gatewayGrpc.GatewayMessage, 16)

	// start send and receive loops
	c.wg.Add(2)
	go c.sendLoop()
	go c.recvLoop()

	dl.Infof("connected to orchestrator at '%s'", c.cfg.SocketPath)
	return nil
}

// Register sends the registration request to the orchestrator.
func (c *Client) Register() error {
	c.mu.Lock()
	if !c.connected {
		c.mu.Unlock()
		return errors.New("not connected")
	}
	c.mu.Unlock()

	// send register request directly on stream (not through channel)
	// because we need to handle the response synchronously
	regReq := &gatewayGrpc.GatewayMessage{
		Payload: &gatewayGrpc.GatewayMessage_Register{
			Register: &gatewayGrpc.RegisterRequest{
				ShareToken: c.shareToken,
				Pid:        int64(os.Getpid()),
			},
		},
	}

	if err := c.stream.Send(regReq); err != nil {
		return errors.Wrap(err, "error sending register request")
	}

	dl.Infof("registration sent for gateway '%s'", c.shareToken)
	return nil
}

// Close closes the connection to the orchestrator.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.connected {
		return nil
	}

	c.connected = false
	c.cancel()

	if c.stream != nil {
		c.stream.CloseSend()
	}

	c.wg.Wait()

	if c.conn != nil {
		c.conn.Close()
	}

	dl.Info("disconnected from orchestrator")
	return nil
}

// resetConnection cleans up the current connection resources to prepare for reconnection.
// must be called without holding c.mu lock.
func (c *Client) resetConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.connected = false

	if c.cancel != nil {
		c.cancel()
	}

	if c.stream != nil {
		c.stream.CloseSend()
		c.stream = nil
	}

	// wait for send/recv loops to exit
	c.mu.Unlock()
	c.wg.Wait()
	c.mu.Lock()

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// StartReconnectLoop starts the reconnection loop in a goroutine.
// this is used when the initial connection fails or when the connection is lost.
func (c *Client) StartReconnectLoop(ctx context.Context) {
	c.parentCtx = ctx
	go c.reconnectLoop()
}

// reconnectLoop attempts to reconnect to the orchestrator with a fixed interval.
func (c *Client) reconnectLoop() {
	c.reconnectMu.Lock()
	if c.reconnecting {
		c.reconnectMu.Unlock()
		return
	}
	c.reconnecting = true
	c.reconnectMu.Unlock()

	defer func() {
		c.reconnectMu.Lock()
		c.reconnecting = false
		c.reconnectMu.Unlock()
	}()

	dl.Info("starting reconnection loop")

	ticker := time.NewTicker(c.cfg.ReconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.parentCtx.Done():
			dl.Info("reconnection loop cancelled")
			return
		case <-ticker.C:
			// check if socket exists before attempting connection
			if _, err := os.Stat(c.cfg.SocketPath); os.IsNotExist(err) {
				dl.Debug("orchestrator socket not found, waiting...")
				continue
			}

			// clean up old connection
			c.resetConnection()

			// attempt to connect
			if err := c.Connect(c.parentCtx); err != nil {
				dl.Debugf("reconnection attempt failed: %v", err)
				continue
			}

			// attempt to register
			if err := c.Register(); err != nil {
				dl.Warnf("reconnection registration failed: %v", err)
				c.resetConnection()
				continue
			}

			dl.Info("reconnected to orchestrator")

			// notify callback if set
			if c.OnReconnect != nil {
				c.OnReconnect()
			}

			return
		}
	}
}

// ShutdownCh returns a channel that receives shutdown commands from the orchestrator.
func (c *Client) ShutdownCh() <-chan string {
	return c.shutdownCh
}

// SetVersion sets the version string to report in ping responses.
func (c *Client) SetVersion(version string) {
	c.mu.Lock()
	c.version = version
	c.mu.Unlock()
}

// UpdateMetrics updates the metrics reported in heartbeats.
func (c *Client) UpdateMetrics(activeConnections int32, toolInvocations int64) {
	c.metricsMu.Lock()
	c.activeConnections = activeConnections
	c.toolInvocations = toolInvocations
	c.metricsMu.Unlock()
}

// IncrementToolInvocations increments the tool invocation counter.
func (c *Client) IncrementToolInvocations() {
	c.metricsMu.Lock()
	c.toolInvocations++
	c.metricsMu.Unlock()
}

// ReportStatus sends a status update to the orchestrator.
func (c *Client) ReportStatus(state string, err error) {
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	c.sendCh <- &gatewayGrpc.GatewayMessage{
		Payload: &gatewayGrpc.GatewayMessage_Status{
			Status: &gatewayGrpc.StatusReport{
				ShareToken: c.shareToken,
				State:      state,
				Error:      errStr,
			},
		},
	}
}

// StartHeartbeat starts the heartbeat loop. Should be called after successful registration.
func (c *Client) StartHeartbeat(ctx context.Context) {
	c.wg.Add(1)
	go c.heartbeatLoop(ctx)
}

func (c *Client) sendLoop() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case msg, ok := <-c.sendCh:
			if !ok {
				return
			}
			if err := c.stream.Send(msg); err != nil {
				dl.Warnf("error sending message: %v", err)
				return
			}
		}
	}
}

func (c *Client) recvLoop() {
	defer c.wg.Done()

	for {
		msg, err := c.stream.Recv()
		if err == io.EOF {
			dl.Info("orchestrator closed connection")
			c.triggerReconnect()
			return
		}
		if err != nil {
			if c.ctx.Err() != nil {
				// context cancelled, expected shutdown
				return
			}
			dl.Warnf("error receiving message: %v", err)
			c.triggerReconnect()
			return
		}

		c.handleMessage(msg)
	}
}

// triggerReconnect marks the connection as disconnected and starts the reconnection loop.
func (c *Client) triggerReconnect() {
	c.mu.Lock()
	c.connected = false
	parentCtx := c.parentCtx
	c.mu.Unlock()

	if parentCtx != nil && parentCtx.Err() == nil {
		dl.Info("connection lost, starting reconnection")
		go c.reconnectLoop()
	}
}

func (c *Client) handleMessage(msg *gatewayGrpc.OrchestratorMessage) {
	switch payload := msg.Payload.(type) {
	case *gatewayGrpc.OrchestratorMessage_RegisterResponse:
		if payload.RegisterResponse.Accepted {
			dl.Info("registration accepted by orchestrator")
		} else {
			dl.Errorf("registration rejected: %s", payload.RegisterResponse.Error)
		}

	case *gatewayGrpc.OrchestratorMessage_HeartbeatAck:
		dl.Debug("heartbeat acknowledged")

	case *gatewayGrpc.OrchestratorMessage_Ping:
		c.mu.Lock()
		version := c.version
		c.mu.Unlock()

		c.sendCh <- &gatewayGrpc.GatewayMessage{
			Payload: &gatewayGrpc.GatewayMessage_Pong{
				Pong: &gatewayGrpc.PingResponse{
					Version: version,
				},
			},
		}

	case *gatewayGrpc.OrchestratorMessage_Shutdown:
		dl.Infof("received shutdown command: %s", payload.Shutdown.Reason)

		// acknowledge shutdown
		c.sendCh <- &gatewayGrpc.GatewayMessage{
			Payload: &gatewayGrpc.GatewayMessage_ShutdownAck{
				ShutdownAck: &gatewayGrpc.ShutdownAck{
					Accepted: true,
				},
			},
		}

		// signal shutdown to listener
		select {
		case c.shutdownCh <- payload.Shutdown.Reason:
		default:
			// channel full, shutdown already signalled
		}
	}
}

func (c *Client) heartbeatLoop(ctx context.Context) {
	defer c.wg.Done()

	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.sendHeartbeat()
		}
	}
}

func (c *Client) sendHeartbeat() {
	c.metricsMu.RLock()
	activeConns := c.activeConnections
	toolInvocs := c.toolInvocations
	c.metricsMu.RUnlock()

	select {
	case c.sendCh <- &gatewayGrpc.GatewayMessage{
		Payload: &gatewayGrpc.GatewayMessage_Heartbeat{
			Heartbeat: &gatewayGrpc.HeartbeatRequest{
				ShareToken:        c.shareToken,
				ActiveConnections: activeConns,
				ToolInvocations:   toolInvocs,
			},
		},
	}:
		dl.Debug("heartbeat sent")
	default:
		dl.Warn("heartbeat send channel full, skipping")
	}
}
