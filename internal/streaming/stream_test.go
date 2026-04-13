package streaming

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc"
)

type StreamManagerTestSuite struct {
	suite.Suite
	logger *logrus.Logger
	sm     *StreamManager
}

func (suite *StreamManagerTestSuite) SetupTest() {
	suite.logger = logrus.New()
	suite.logger.SetLevel(logrus.DebugLevel)
	suite.sm = NewStreamManager(suite.logger, DefaultStreamConfig())
}

func (suite *StreamManagerTestSuite) TearDownTest() {
	if suite.sm != nil {
		suite.sm.Close()
	}
}

func (suite *StreamManagerTestSuite) TestDefaultStreamConfig() {
	config := DefaultStreamConfig()

	assert.Equal(suite.T(), 100*time.Millisecond, config.InitialBackoff)
	assert.Equal(suite.T(), 30*time.Second, config.MaxBackoff)
	assert.Equal(suite.T(), 1.5, config.BackoffMultiplier)
	assert.Equal(suite.T(), 5*time.Second, config.HeartbeatInterval)
	assert.Equal(suite.T(), 10*time.Second, config.HeartbeatTimeout)
	assert.Equal(suite.T(), 0, config.MaxRetries)
}

func (suite *StreamManagerTestSuite) TestNewStreamManager() {
	sm := NewStreamManager(suite.logger, DefaultStreamConfig())
	defer sm.Close()

	assert.NotNil(suite.T(), sm)
	assert.False(suite.T(), sm.IsConnected())
	assert.NotNil(suite.T(), sm.ctx)
	assert.NotNil(suite.T(), sm.done)
}

func (suite *StreamManagerTestSuite) TestSetCallbacks() {
	suite.sm.SetCallbacks(
		func() error {
			return nil
		},
		func() {
		},
		func(err error) {
		},
	)

	suite.sm.mu.Lock()
	assert.NotNil(suite.T(), suite.sm.onConnected)
	assert.NotNil(suite.T(), suite.sm.onDisconnected)
	assert.NotNil(suite.T(), suite.sm.onError)
	suite.sm.mu.Unlock()
}

func (suite *StreamManagerTestSuite) TestIsConnected() {
	assert.False(suite.T(), suite.sm.IsConnected())

	suite.sm.mu.Lock()
	suite.sm.isConnected = true
	suite.sm.mu.Unlock()

	assert.True(suite.T(), suite.sm.IsConnected())
}

func (suite *StreamManagerTestSuite) TestMarkDisconnected() {
	disconnectedCalled := false

	suite.sm.SetCallbacks(
		nil,
		func() {
			disconnectedCalled = true
		},
		nil,
	)

	suite.sm.mu.Lock()
	suite.sm.isConnected = true
	suite.sm.mu.Unlock()

	suite.sm.MarkDisconnected()

	assert.False(suite.T(), suite.sm.IsConnected())
	assert.True(suite.T(), disconnectedCalled)
}

func (suite *StreamManagerTestSuite) TestGetConnectionNotConnected() {
	conn, err := suite.sm.GetConnection()

	assert.Nil(suite.T(), conn)
	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), "stream not connected", err.Error())
}

func (suite *StreamManagerTestSuite) TestGetConnectionConnected() {
	mockConn := &grpc.ClientConn{}

	suite.sm.mu.Lock()
	suite.sm.conn = mockConn
	suite.sm.isConnected = true
	suite.sm.mu.Unlock()

	conn, err := suite.sm.GetConnection()

	assert.NoError(suite.T(), err)
	assert.Equal(suite.T(), mockConn, conn)
}

func (suite *StreamManagerTestSuite) TestGetStats() {
	suite.sm.mu.Lock()
	suite.sm.isConnected = true
	suite.sm.retryCount = 5
	suite.sm.lastConnectTime = time.Now()
	suite.sm.mu.Unlock()

	stats := suite.sm.GetStats()

	assert.NotNil(suite.T(), stats)
	assert.Equal(suite.T(), true, stats["connected"])
	assert.Equal(suite.T(), 5, stats["retry_count"])
	assert.NotNil(suite.T(), stats["last_connect_time"])
	assert.NotNil(suite.T(), stats["connection_duration"])
}

func (suite *StreamManagerTestSuite) TestClose() {
	mockConn := &grpc.ClientConn{}

	suite.sm.mu.Lock()
	suite.sm.conn = mockConn
	suite.sm.mu.Unlock()

	err := suite.sm.Close()

	assert.NoError(suite.T(), err)
}

func (suite *StreamManagerTestSuite) TestCloseWithoutConnection() {
	suite.sm.mu.Lock()
	suite.sm.conn = nil
	suite.sm.mu.Unlock()

	err := suite.sm.Close()

	assert.NoError(suite.T(), err)
}

func (suite *StreamManagerTestSuite) TestMarkDisconnectedMultipleTimes() {
	disconnectedCallCount := 0

	suite.sm.SetCallbacks(
		nil,
		func() {
			disconnectedCallCount++
		},
		nil,
	)

	// First disconnect
	suite.sm.mu.Lock()
	suite.sm.isConnected = true
	suite.sm.mu.Unlock()

	suite.sm.MarkDisconnected()
	assert.Equal(suite.T(), 1, disconnectedCallCount)

	// Second disconnect (should not call callback)
	suite.sm.MarkDisconnected()
	assert.Equal(suite.T(), 1, disconnectedCallCount)
}

func (suite *StreamManagerTestSuite) TestConcurrentOperations() {
	var wg sync.WaitGroup
	panicCount := 0
	mu := sync.Mutex{}

	// Create a mock connection
	mockConn, err := grpc.Dial("localhost:50051")
	if err == nil {
		defer mockConn.Close()
	}

	// Simulate concurrent operations
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					mu.Lock()
					panicCount++
					mu.Unlock()
				}
				wg.Done()
			}()

			// Simulate connect
			suite.sm.mu.Lock()
			suite.sm.isConnected = true
			suite.sm.conn = mockConn
			suite.sm.mu.Unlock()

			// Get connection (should succeed while connected)
			conn, err := suite.sm.GetConnection()
			if err == nil && conn != nil {
				// Success
			}

			// Mark disconnected
			suite.sm.MarkDisconnected()

			// Get connection again (should now fail gracefully)
			_, _ = suite.sm.GetConnection()
		}()
	}

	wg.Wait()

	// Should not have any panics from concurrent operations
	assert.Equal(suite.T(), 0, panicCount)
}

func (suite *StreamManagerTestSuite) TestExponentialBackoffCalculation() {
	config := DefaultStreamConfig()
	backoff := config.InitialBackoff

	// First backoff
	assert.Equal(suite.T(), 100*time.Millisecond, backoff)

	// Second backoff (1.5x)
	backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
	assert.Equal(suite.T(), 150*time.Millisecond, backoff)

	// Third backoff (1.5x)
	backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
	assert.Equal(suite.T(), 225*time.Millisecond, backoff)

	// Should cap at MaxBackoff
	for backoff <= config.MaxBackoff {
		backoff = time.Duration(float64(backoff) * config.BackoffMultiplier)
	}
	if backoff > config.MaxBackoff {
		backoff = config.MaxBackoff
	}
	assert.Equal(suite.T(), config.MaxBackoff, backoff)
}

func (suite *StreamManagerTestSuite) TestContextCancellation() {
	sm := NewStreamManager(suite.logger, DefaultStreamConfig())
	defer sm.Close()

	// Cancel context
	sm.mu.Lock()
	sm.cancel()
	sm.mu.Unlock()

	// Wait a bit for context to propagate
	time.Sleep(100 * time.Millisecond)

	// Verify context is cancelled
	assert.Error(suite.T(), sm.ctx.Err())
}

func (suite *StreamManagerTestSuite) TestSetCallbacksThreadSafety() {
	callbacks := make([]int, 10)
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			suite.sm.SetCallbacks(
				func() error {
					callbacks[idx]++
					return nil
				},
				nil,
				nil,
			)
		}()
	}

	wg.Wait()

	// All callbacks should be set (last one wins)
	suite.sm.mu.Lock()
	assert.NotNil(suite.T(), suite.sm.onConnected)
	suite.sm.mu.Unlock()
}

func (suite *StreamManagerTestSuite) TestOnConnectedCallbackSuccess() {
	callCount := 0
	onConnected := func() error {
		callCount++
		return nil
	}

	suite.sm.SetCallbacks(onConnected, nil, nil)
	suite.sm.mu.Lock()
	if suite.sm.onConnected != nil {
		_ = suite.sm.onConnected()
	}
	suite.sm.mu.Unlock()

	assert.Equal(suite.T(), 1, callCount)
}

func (suite *StreamManagerTestSuite) TestOnConnectedCallbackError() {
	callCount := 0
	onConnected := func() error {
		callCount++
		return errors.New("connection failed")
	}

	suite.sm.SetCallbacks(onConnected, nil, nil)
	suite.sm.mu.Lock()
	err := suite.sm.onConnected()
	suite.sm.mu.Unlock()

	assert.Equal(suite.T(), 1, callCount)
	assert.Error(suite.T(), err)
}

func (suite *StreamManagerTestSuite) TestOnDisconnectedCallback() {
	callCount := 0
	onDisconnected := func() {
		callCount++
	}

	suite.sm.SetCallbacks(nil, onDisconnected, nil)
	suite.sm.mu.Lock()
	if suite.sm.onDisconnected != nil {
		suite.sm.onDisconnected()
	}
	suite.sm.mu.Unlock()

	assert.Equal(suite.T(), 1, callCount)
}

func (suite *StreamManagerTestSuite) TestOnErrorCallback() {
	callCount := 0
	var lastErr error
	onError := func(err error) {
		callCount++
		lastErr = err
	}

	suite.sm.SetCallbacks(nil, nil, onError)
	testErr := errors.New("test error")
	suite.sm.mu.Lock()
	if suite.sm.onError != nil {
		suite.sm.onError(testErr)
	}
	suite.sm.mu.Unlock()

	assert.Equal(suite.T(), 1, callCount)
	assert.Equal(suite.T(), testErr, lastErr)
}

func TestStreamManagerTestSuite(t *testing.T) {
	suite.Run(t, new(StreamManagerTestSuite))
}
