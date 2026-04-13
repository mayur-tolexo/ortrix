package sdk

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/mayur-tolexo/ortrix/api/proto"
	"github.com/mayur-tolexo/ortrix/internal/streaming"
)

type WorkerTestSuite struct {
	suite.Suite
	logger *logrus.Logger
	config WorkerConfig
}

func (suite *WorkerTestSuite) SetupTest() {
	suite.logger = logrus.New()
	suite.logger.SetLevel(logrus.DebugLevel)

	suite.config = WorkerConfig{
		WorkerID:      "test-worker-1",
		ServerAddress: "localhost:5000",
		Capabilities:  []string{"email", "sms"},
		StreamConfig:  streaming.DefaultStreamConfig(),
		Logger:        suite.logger,
	}
}

func (suite *WorkerTestSuite) TestNewWorkerWithValidConfig() {
	w, err := NewWorker(suite.config)

	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), w)
	assert.Equal(suite.T(), suite.config.WorkerID, w.config.WorkerID)
	assert.Equal(suite.T(), suite.config.ServerAddress, w.config.ServerAddress)
	assert.False(suite.T(), w.IsConnected())
	assert.NotNil(suite.T(), w.sm)

	w.Stop(context.Background())
}

func (suite *WorkerTestSuite) TestNewWorkerMissingWorkerID() {
	suite.config.WorkerID = ""

	w, err := NewWorker(suite.config)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), w)
	assert.Equal(suite.T(), "worker ID is required", err.Error())
}

func (suite *WorkerTestSuite) TestNewWorkerMissingServerAddress() {
	suite.config.ServerAddress = ""

	w, err := NewWorker(suite.config)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), w)
	assert.Equal(suite.T(), "server address is required", err.Error())
}

func (suite *WorkerTestSuite) TestNewWorkerDefaultLogger() {
	suite.config.Logger = nil

	w, err := NewWorker(suite.config)

	require.NoError(suite.T(), err)
	require.NotNil(suite.T(), w)
	assert.NotNil(suite.T(), w.logger)

	w.Stop(context.Background())
}

func (suite *WorkerTestSuite) TestRegisterHandler() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handler := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return &proto.TaskResult{Success: true}, nil
	}

	err = w.RegisterHandler("email", handler)

	assert.NoError(suite.T(), err)

	w.mu.RLock()
	_, exists := w.handlers["email"]
	w.mu.RUnlock()

	assert.True(suite.T(), exists)
}

func (suite *WorkerTestSuite) TestRegisterHandlerEmptyTaskType() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handler := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return &proto.TaskResult{Success: true}, nil
	}

	err = w.RegisterHandler("", handler)

	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), "task type is required", err.Error())
}

func (suite *WorkerTestSuite) TestRegisterHandlerNilHandler() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	err = w.RegisterHandler("email", nil)

	assert.Error(suite.T(), err)
	assert.Equal(suite.T(), "handler cannot be nil", err.Error())
}

func (suite *WorkerTestSuite) TestRegisterMultipleHandlers() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handler1 := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return &proto.TaskResult{Success: true}, nil
	}

	handler2 := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return &proto.TaskResult{Success: true}, nil
	}

	err = w.RegisterHandler("email", handler1)
	assert.NoError(suite.T(), err)

	err = w.RegisterHandler("sms", handler2)
	assert.NoError(suite.T(), err)

	w.mu.RLock()
	assert.Equal(suite.T(), 2, len(w.handlers))
	w.mu.RUnlock()
}

func (suite *WorkerTestSuite) TestIsConnected() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	assert.False(suite.T(), w.IsConnected())

	w.setConnected(true)
	assert.True(suite.T(), w.IsConnected())

	w.setConnected(false)
	assert.False(suite.T(), w.IsConnected())
}

func (suite *WorkerTestSuite) TestGetStats() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	stats := w.GetStats()

	assert.NotNil(suite.T(), stats)
	assert.Equal(suite.T(), int64(0), stats["tasks_processed"])
	assert.Equal(suite.T(), int64(0), stats["tasks_failed"])
}

func (suite *WorkerTestSuite) TestRecordTaskProcessed() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	w.recordTaskProcessed(100*time.Millisecond, true)

	stats := w.GetStats()
	assert.Equal(suite.T(), int64(1), stats["tasks_processed"])
	assert.Equal(suite.T(), int64(0), stats["tasks_failed"])
}

func (suite *WorkerTestSuite) TestRecordTaskProcessedFailed() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	w.recordTaskProcessed(100*time.Millisecond, false)

	stats := w.GetStats()
	assert.Equal(suite.T(), int64(1), stats["tasks_processed"])
	assert.Equal(suite.T(), int64(1), stats["tasks_failed"])
}

func (suite *WorkerTestSuite) TestRecordMultipleTasks() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	w.recordTaskProcessed(100*time.Millisecond, true)
	w.recordTaskProcessed(200*time.Millisecond, true)
	w.recordTaskProcessed(50*time.Millisecond, false)

	stats := w.GetStats()
	assert.Equal(suite.T(), int64(3), stats["tasks_processed"])
	assert.Equal(suite.T(), int64(1), stats["tasks_failed"])
}

func (suite *WorkerTestSuite) TestProcessTaskWithMissingHandler() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	task := &proto.Task{
		Id:        "task-1",
		Type:      "unknown",
		Payload:   []byte("test"),
		CreatedAt: time.Now().Unix(),
	}

	// Create a mock stream
	mockStream := &MockWorkerServiceStreamTasksClient{}
	mockStream.On("Send", mock.Anything).Return(nil)

	w.processTask(task, mockStream)

	// Verify result was sent
	mockStream.AssertCalled(suite.T(), "Send", mock.MatchedBy(func(msg *proto.WorkerMessage) bool {
		result := msg.GetResult()
		return result != nil && result.TaskId == "task-1" && !result.Success
	}))
}

func (suite *WorkerTestSuite) TestProcessTaskWithHandler() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handlerCalled := false
	handler := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		handlerCalled = true
		return &proto.TaskResult{
			TaskId:      task.Id,
			Success:     true,
			Output:      []byte("success"),
			CompletedAt: time.Now().Unix(),
		}, nil
	}

	w.RegisterHandler("email", handler)

	task := &proto.Task{
		Id:        "task-1",
		Type:      "email",
		Payload:   []byte("test@example.com"),
		CreatedAt: time.Now().Unix(),
	}

	// Create a mock stream
	mockStream := &MockWorkerServiceStreamTasksClient{}
	mockStream.On("Send", mock.Anything).Return(nil)

	w.processTask(task, mockStream)

	assert.True(suite.T(), handlerCalled)
	mockStream.AssertCalled(suite.T(), "Send", mock.Anything)
}

func (suite *WorkerTestSuite) TestProcessTaskHandlerError() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handler := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return nil, errors.New("handler error")
	}

	w.RegisterHandler("email", handler)

	task := &proto.Task{
		Id:        "task-1",
		Type:      "email",
		Payload:   []byte("test@example.com"),
		CreatedAt: time.Now().Unix(),
	}

	// Create a mock stream
	mockStream := &MockWorkerServiceStreamTasksClient{}
	mockStream.On("Send", mock.Anything).Return(nil)

	w.processTask(task, mockStream)

	mockStream.AssertCalled(suite.T(), "Send", mock.MatchedBy(func(msg *proto.WorkerMessage) bool {
		result := msg.GetResult()
		return result != nil && !result.Success && result.Error == "handler error"
	}))
}

func (suite *WorkerTestSuite) TestHandleOrchestratorMessageTask() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	handler := func(ctx context.Context, task *proto.Task) (*proto.TaskResult, error) {
		return &proto.TaskResult{Success: true}, nil
	}

	w.RegisterHandler("email", handler)

	msg := &proto.OrchestratorMessage{
		Payload: &proto.OrchestratorMessage_Task{
			Task: &proto.Task{
				Id:        "task-1",
				Type:      "email",
				CreatedAt: time.Now().Unix(),
			},
		},
	}

	mockStream := &MockWorkerServiceStreamTasksClient{}
	mockStream.On("Send", mock.Anything).Return(nil)

	w.handleOrchestratorMessage(msg, mockStream)

	mockStream.AssertCalled(suite.T(), "Send", mock.Anything)
}

func (suite *WorkerTestSuite) TestHandleOrchestratorMessageAck() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	msg := &proto.OrchestratorMessage{
		Payload: &proto.OrchestratorMessage_Ack{
			Ack: &proto.Ack{
				Message: "task received",
			},
		},
	}

	mockStream := &MockWorkerServiceStreamTasksClient{}
	mockStream.On("Send", mock.Anything).Return(nil)

	// Should not crash
	w.handleOrchestratorMessage(msg, mockStream)

	// No task processing should occur
	mockStream.AssertNotCalled(suite.T(), "Send")
}

func (suite *WorkerTestSuite) TestHandleOrchestratorMessageNil() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	mockStream := &MockWorkerServiceStreamTasksClient{}

	// Should not crash with nil message
	w.handleOrchestratorMessage(nil, mockStream)

	mockStream.AssertNotCalled(suite.T(), "Send")
}

func (suite *WorkerTestSuite) TestStop() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)

	err = w.Stop(context.Background())

	assert.NoError(suite.T(), err)
}

func (suite *WorkerTestSuite) TestSetConnectedConcurrency() {
	w, err := NewWorker(suite.config)
	require.NoError(suite.T(), err)
	defer w.Stop(context.Background())

	for i := 0; i < 100; i++ {
		go func() {
			w.setConnected(true)
			_ = w.IsConnected()
			w.setConnected(false)
		}()
	}

	time.Sleep(100 * time.Millisecond)

	assert.False(suite.T(), w.IsConnected())
}

// Mock implementations for testing

type MockWorkerServiceStreamTasksClient struct {
	mock.Mock
	proto.WorkerService_StreamTasksClient
}

func (m *MockWorkerServiceStreamTasksClient) Send(msg *proto.WorkerMessage) error {
	args := m.Called(msg)
	return args.Error(0)
}

func (m *MockWorkerServiceStreamTasksClient) Recv() (*proto.OrchestratorMessage, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*proto.OrchestratorMessage), args.Error(1)
}

func (m *MockWorkerServiceStreamTasksClient) CloseSend() error {
	args := m.Called()
	return args.Error(0)
}

func TestWorkerTestSuite(t *testing.T) {
	suite.Run(t, new(WorkerTestSuite))
}
