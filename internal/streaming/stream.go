package streaming

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// StreamConfig holds configuration for stream management.
type StreamConfig struct {
	// InitialBackoff is the initial backoff duration for reconnection.
	InitialBackoff time.Duration

	// MaxBackoff is the maximum backoff duration.
	MaxBackoff time.Duration

	// BackoffMultiplier is the multiplier for exponential backoff.
	BackoffMultiplier float64

	// HeartbeatInterval is the interval for sending heartbeats.
	HeartbeatInterval time.Duration

	// HeartbeatTimeout is the timeout for heartbeat responses.
	HeartbeatTimeout time.Duration

	// MaxRetries is the maximum number of reconnection attempts (0 = unlimited).
	MaxRetries int
}

// DefaultStreamConfig returns the default stream configuration.
func DefaultStreamConfig() StreamConfig {
	return StreamConfig{
		InitialBackoff:    100 * time.Millisecond,
		MaxBackoff:        30 * time.Second,
		BackoffMultiplier: 1.5,
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  10 * time.Second,
		MaxRetries:        0,
	}
}

// StreamManager manages stream lifecycle with reconnection and backoff.
type StreamManager struct {
	logger logrus.FieldLogger
	config StreamConfig

	// Connection state
	mu              sync.RWMutex
	conn            *grpc.ClientConn
	isConnected     bool
	retryCount      int
	lastConnectTime time.Time

	// Callbacks
	onConnected    func() error
	onDisconnected func()
	onError        func(error)

	// Lifecycle
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// NewStreamManager creates a new stream manager.
func NewStreamManager(logger logrus.FieldLogger, config StreamConfig) *StreamManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &StreamManager{
		logger:  logger,
		config:  config,
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		retryCount: 0,
	}
}

// SetCallbacks sets the callback functions for connection lifecycle events.
func (sm *StreamManager) SetCallbacks(
	onConnected func() error,
	onDisconnected func(),
	onError func(error),
) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.onConnected = onConnected
	sm.onDisconnected = onDisconnected
	sm.onError = onError
}

// Connect establishes a connection and starts the reconnection loop.
func (sm *StreamManager) Connect(addr string, opts ...grpc.DialOption) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Add default options
	defaultOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	opts = append(defaultOpts, opts...)

	conn, err := grpc.Dial(addr, opts...)
	if err != nil {
		sm.logger.WithError(err).Error("Failed to connect")
		if sm.onError != nil {
			sm.onError(err)
		}
		return err
	}

	sm.conn = conn
	sm.isConnected = true
	sm.lastConnectTime = time.Now()
	sm.retryCount = 0

	sm.logger.WithField("address", addr).Info("Connected")

	if sm.onConnected != nil {
		if err := sm.onConnected(); err != nil {
			sm.logger.WithError(err).Error("Connection callback failed")
			if sm.onError != nil {
				sm.onError(err)
			}
			return err
		}
	}

	// Start reconnection loop
	go sm.reconnectionLoop(addr, opts)

	return nil
}

// reconnectionLoop handles automatic reconnection with exponential backoff.
func (sm *StreamManager) reconnectionLoop(addr string, opts []grpc.DialOption) {
	backoff := sm.config.InitialBackoff

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-sm.done:
			return
		case <-time.After(time.Second): // Check connection periodically
			sm.mu.RLock()
			isConnected := sm.isConnected
			sm.mu.RUnlock()

			if !isConnected {
				sm.attemptReconnect(addr, opts, &backoff)
			}
		}
	}
}

// attemptReconnect attempts to reconnect with backoff.
func (sm *StreamManager) attemptReconnect(addr string, opts []grpc.DialOption, backoff *time.Duration) {
	sm.mu.Lock()

	// Check retry limit
	if sm.config.MaxRetries > 0 && sm.retryCount >= sm.config.MaxRetries {
		sm.logger.WithField("retries", sm.retryCount).Error("Max reconnection attempts reached")
		if sm.onError != nil {
			sm.onError(fmt.Errorf("max reconnection attempts (%d) exceeded", sm.config.MaxRetries))
		}
		sm.mu.Unlock()
		return
	}

	sm.retryCount++
	currentBackoff := *backoff

	sm.logger.WithFields(logrus.Fields{
		"address": addr,
		"backoff": currentBackoff,
		"attempt": sm.retryCount,
	}).Info("Attempting reconnection")

	sm.mu.Unlock()

	// Wait before reconnecting
	select {
	case <-time.After(currentBackoff):
	case <-sm.ctx.Done():
		return
	case <-sm.done:
		return
	}

	// Attempt connection
	conn, err := grpc.Dial(addr, opts...)
	if err != nil {
		sm.logger.WithError(err).Warn("Reconnection attempt failed")
		if sm.onError != nil {
			sm.onError(err)
		}

		// Update backoff
		newBackoff := time.Duration(float64(currentBackoff) * sm.config.BackoffMultiplier)
		if newBackoff > sm.config.MaxBackoff {
			newBackoff = sm.config.MaxBackoff
		}
		*backoff = newBackoff
		return
	}

	// Connection succeeded
	sm.mu.Lock()
	sm.conn = conn
	sm.isConnected = true
	sm.lastConnectTime = time.Now()
	sm.retryCount = 0
	sm.mu.Unlock()

	sm.logger.WithField("address", addr).Info("Reconnected")

	if sm.onConnected != nil {
		if err := sm.onConnected(); err != nil {
			sm.logger.WithError(err).Error("Reconnection callback failed")
			if sm.onError != nil {
				sm.onError(err)
			}
			sm.markDisconnected()
		}
	}

	// Reset backoff on successful connection
	*backoff = sm.config.InitialBackoff
}

// GetConnection returns the current gRPC connection.
func (sm *StreamManager) GetConnection() (*grpc.ClientConn, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	if sm.conn == nil || !sm.isConnected {
		return nil, fmt.Errorf("stream not connected")
	}
	return sm.conn, nil
}

// IsConnected returns whether the stream is currently connected.
func (sm *StreamManager) IsConnected() bool {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.isConnected
}

// MarkDisconnected marks the stream as disconnected.
func (sm *StreamManager) MarkDisconnected() {
	sm.markDisconnected()
}

// markDisconnected (internal) marks the stream as disconnected.
func (sm *StreamManager) markDisconnected() {
	sm.mu.Lock()
	wasConnected := sm.isConnected
	sm.isConnected = false
	sm.mu.Unlock()

	if wasConnected && sm.onDisconnected != nil {
		sm.onDisconnected()
	}

	sm.logger.Warn("Stream disconnected")
}

// Close closes the stream manager and underlying connection.
func (sm *StreamManager) Close() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Don't close the done channel multiple times
	select {
	case <-sm.done:
		// Already closed
		return nil
	default:
		close(sm.done)
	}

	sm.cancel()

	var err error
	if sm.conn != nil {
		// Safely close connection, catching any panics
		defer func() {
			if r := recover(); r != nil {
				sm.logger.WithField("panic", r).Warn("Panic during connection close")
			}
		}()
		err = sm.conn.Close()
		sm.conn = nil
	}
	return err
}

// GetStats returns connection statistics.
func (sm *StreamManager) GetStats() map[string]interface{} {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return map[string]interface{}{
		"connected":          sm.isConnected,
		"retry_count":        sm.retryCount,
		"last_connect_time":  sm.lastConnectTime,
		"connection_duration": time.Since(sm.lastConnectTime).Seconds(),
	}
}
