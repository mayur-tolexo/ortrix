package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mayur-tolexo/ortrix/api/proto"
	"github.com/mayur-tolexo/ortrix/internal/executor"
	"github.com/mayur-tolexo/ortrix/internal/partition"
	"github.com/mayur-tolexo/ortrix/internal/task"
	"github.com/mayur-tolexo/ortrix/internal/wal"
)

type GatewayServiceTestSuite struct {
	suite.Suite
	logger         *logrus.Logger
	engine         *executor.Engine
	gateway        *GatewayService
	walLogger      wal.Logger
	partitionMgr   partition.Manager
}

func (suite *GatewayServiceTestSuite) SetupTest() {
	suite.logger = logrus.New()
	suite.logger.SetLevel(logrus.DebugLevel)

	// Setup dependencies
	suite.walLogger = wal.NewInMemoryLogger()
	suite.partitionMgr = partition.NewDefaultManager(10)
	suite.partitionMgr.RegisterNode("node-1")

	// Create engine
	suite.engine = executor.NewEngine(suite.logger, suite.walLogger, suite.partitionMgr)

	// Create gateway
	suite.gateway = NewGatewayService(suite.logger, suite.engine)
}

func (suite *GatewayServiceTestSuite) TearDownTest() {
	if suite.engine != nil {
		suite.engine.Close()
	}
}

func (suite *GatewayServiceTestSuite) TestNewGatewayService() {
	gs := NewGatewayService(suite.logger, suite.engine)

	assert.NotNil(suite.T(), gs)
	assert.NotNil(suite.T(), gs.logger)
	assert.Equal(suite.T(), suite.engine, gs.engine)
}

func (suite *GatewayServiceTestSuite) TestNewGatewayServiceDefaultLogger() {
	gs := NewGatewayService(nil, suite.engine)

	assert.NotNil(suite.T(), gs)
	assert.NotNil(suite.T(), gs.logger)
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskValid() {
	req := &proto.SubmitTaskRequest{
		Task: &proto.Task{
			Id:        "task-1",
			Type:      "email",
			Payload:   []byte("test@example.com"),
			CreatedAt: time.Now().Unix(),
		},
	}

	resp, err := suite.gateway.SubmitTask(context.Background(), req)

	require.NoError(suite.T(), err)
	assert.NotNil(suite.T(), resp)
	assert.Equal(suite.T(), true, resp.Accepted)
	assert.NotEmpty(suite.T(), resp.TaskId)
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskNilRequest() {
	resp, err := suite.gateway.SubmitTask(context.Background(), nil)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskNilTask() {
	req := &proto.SubmitTaskRequest{Task: nil}

	resp, err := suite.gateway.SubmitTask(context.Background(), req)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskMissingTaskType() {
	req := &proto.SubmitTaskRequest{
		Task: &proto.Task{
			Id:      "task-1",
			Type:    "",
			Payload: []byte("test"),
		},
	}

	resp, err := suite.gateway.SubmitTask(context.Background(), req)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskMissingTaskID() {
	req := &proto.SubmitTaskRequest{
		Task: &proto.Task{
			Id:      "",
			Type:    "email",
			Payload: []byte("test@example.com"),
		},
	}

	resp, err := suite.gateway.SubmitTask(context.Background(), req)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusNotFound() {
	req := &proto.TaskStatusRequest{TaskId: "nonexistent"}

	resp, err := suite.gateway.GetTaskStatus(context.Background(), req)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.NotFound, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusNilRequest() {
	resp, err := suite.gateway.GetTaskStatus(context.Background(), nil)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusMissingTaskID() {
	req := &proto.TaskStatusRequest{TaskId: ""}

	resp, err := suite.gateway.GetTaskStatus(context.Background(), req)

	assert.Error(suite.T(), err)
	assert.Nil(suite.T(), resp)
	assert.Equal(suite.T(), codes.InvalidArgument, status.Code(err))
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusPending() {
	// Create task
	t, err := suite.engine.CreateTask("workflow-1", "email", []byte("test@example.com"))
	require.NoError(suite.T(), err)

	// Query status
	req := &proto.TaskStatusRequest{TaskId: t.ID}
	resp, err := suite.gateway.GetTaskStatus(context.Background(), req)

	require.NoError(suite.T(), err)
	assert.NotNil(suite.T(), resp)
	assert.Equal(suite.T(), t.ID, resp.TaskId)
	assert.Equal(suite.T(), string(task.StatePending), resp.Status)
	assert.Nil(suite.T(), resp.Result)
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusCompleted() {
	// Create task
	t, err := suite.engine.CreateTask("workflow-1", "email", []byte("test@example.com"))
	require.NoError(suite.T(), err)

	// Transition to completed
	_, err = suite.engine.TransitionTask(t.ID, task.StateScheduled)
	require.NoError(suite.T(), err)

	_, err = suite.engine.TransitionTask(t.ID, task.StateDispatched)
	require.NoError(suite.T(), err)

	result := []byte("email sent")
	_, err = suite.engine.CompleteTask(t.ID, result)
	require.NoError(suite.T(), err)

	// Query status
	req := &proto.TaskStatusRequest{TaskId: t.ID}
	resp, err := suite.gateway.GetTaskStatus(context.Background(), req)

	require.NoError(suite.T(), err)
	assert.NotNil(suite.T(), resp)
	assert.Equal(suite.T(), t.ID, resp.TaskId)
	assert.Equal(suite.T(), string(task.StateCompleted), resp.Status)
	assert.NotNil(suite.T(), resp.Result)
	assert.Equal(suite.T(), true, resp.Result.Success)
	assert.Equal(suite.T(), result, resp.Result.Output)
}

func (suite *GatewayServiceTestSuite) TestGetTaskStatusFailed() {
	// Create task
	t, err := suite.engine.CreateTask("workflow-1", "email", []byte("test@example.com"))
	require.NoError(suite.T(), err)

	// Transition to failed
	_, err = suite.engine.TransitionTask(t.ID, task.StateScheduled)
	require.NoError(suite.T(), err)

	_, err = suite.engine.TransitionTask(t.ID, task.StateDispatched)
	require.NoError(suite.T(), err)

	errMsg := "connection timeout"
	_, err = suite.engine.FailTask(t.ID, errMsg)
	require.NoError(suite.T(), err)

	// Query status
	req := &proto.TaskStatusRequest{TaskId: t.ID}
	resp, err := suite.gateway.GetTaskStatus(context.Background(), req)

	require.NoError(suite.T(), err)
	assert.NotNil(suite.T(), resp)
	assert.Equal(suite.T(), string(task.StateFailed), resp.Status)
	assert.NotNil(suite.T(), resp.Result)
	assert.Equal(suite.T(), false, resp.Result.Success)
	assert.Equal(suite.T(), errMsg, resp.Result.Error)
}

func (suite *GatewayServiceTestSuite) TestSubmitMultipleTasks() {
	for i := 0; i < 5; i++ {
		req := &proto.SubmitTaskRequest{
			Task: &proto.Task{
				Id:        suite.T().Name() + string(rune(i)),
				Type:      "process",
				Payload:   []byte("data"),
				CreatedAt: time.Now().Unix(),
			},
		}

		resp, err := suite.gateway.SubmitTask(context.Background(), req)
		require.NoError(suite.T(), err)
		assert.Equal(suite.T(), true, resp.Accepted)
	}
}

func (suite *GatewayServiceTestSuite) TestWorkerServiceHandler() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	assert.NotNil(suite.T(), handler)
	assert.NotNil(suite.T(), handler.logger)
	assert.Equal(suite.T(), suite.engine, handler.engine)
	assert.Equal(suite.T(), 0, len(handler.workers))
}

func (suite *GatewayServiceTestSuite) TestWorkerServiceHandlerDefaultLogger() {
	handler := NewWorkerServiceHandler(nil, suite.engine)

	assert.NotNil(suite.T(), handler)
	assert.NotNil(suite.T(), handler.logger)
}

func (suite *GatewayServiceTestSuite) TestGetWorkerStats() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	// Add mock worker
	handler.mu.Lock()
	handler.workers["worker-1"] = &WorkerConnection{
		WorkerID:     "worker-1",
		Capabilities: []string{"email", "sms"},
		ConnectedAt:  time.Now(),
		LastSeen:     time.Now(),
		TasksInFlight: 3,
	}
	handler.mu.Unlock()

	stats := handler.GetWorkerStats()

	assert.NotNil(suite.T(), stats)
	assert.Equal(suite.T(), 1, stats["total_workers"])
	assert.NotNil(suite.T(), stats["workers"])

	workers := stats["workers"].([]map[string]interface{})
	assert.Equal(suite.T(), 1, len(workers))
	assert.Equal(suite.T(), "worker-1", workers[0]["worker_id"])
}

func (suite *GatewayServiceTestSuite) TestGetWorkerStatsMultiple() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	// Add multiple workers
	handler.mu.Lock()
	for i := 1; i <= 5; i++ {
		handler.workers[suite.T().Name()+string(rune(i))] = &WorkerConnection{
			WorkerID:     suite.T().Name() + string(rune(i)),
			Capabilities: []string{"task-type"},
			ConnectedAt:  time.Now(),
			LastSeen:     time.Now(),
		}
	}
	handler.mu.Unlock()

	stats := handler.GetWorkerStats()

	assert.NotNil(suite.T(), stats)
	assert.Equal(suite.T(), 5, stats["total_workers"])
}

func (suite *GatewayServiceTestSuite) TestGetWorkerStatsNoWorkers() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	stats := handler.GetWorkerStats()

	assert.NotNil(suite.T(), stats)
	assert.Equal(suite.T(), 0, stats["total_workers"])
	assert.Equal(suite.T(), 0, len(stats["workers"].([]map[string]interface{})))
}

func (suite *GatewayServiceTestSuite) TestSubmitTaskConcurrency() {
	for i := 0; i < 50; i++ {
		go func(index int) {
			req := &proto.SubmitTaskRequest{
				Task: &proto.Task{
					Id:        suite.T().Name() + string(rune(index)),
					Type:      "concurrent",
					Payload:   []byte("data"),
					CreatedAt: time.Now().Unix(),
				},
			}

			resp, err := suite.gateway.SubmitTask(context.Background(), req)
			assert.NoError(suite.T(), err)
			assert.NotNil(suite.T(), resp)
		}(i)
	}

	time.Sleep(100 * time.Millisecond)
}

func (suite *GatewayServiceTestSuite) TestHandleTaskResult() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	// Create task
	t, err := suite.engine.CreateTask("workflow-1", "email", []byte("test@example.com"))
	require.NoError(suite.T(), err)

	// Transition to dispatched
	_, err = suite.engine.TransitionTask(t.ID, task.StateScheduled)
	require.NoError(suite.T(), err)
	_, err = suite.engine.TransitionTask(t.ID, task.StateDispatched)
	require.NoError(suite.T(), err)

	// Handle result
	result := &proto.TaskResult{
		TaskId:      t.ID,
		Success:     true,
		Output:      []byte("success"),
		CompletedAt: time.Now().Unix(),
	}

	handler.handleTaskResult(result)

	// Verify task is completed
	completed, err := suite.engine.GetTask(t.ID)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), task.StateCompleted, completed.State)
}

func (suite *GatewayServiceTestSuite) TestHandleTaskResultFailed() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	// Create task
	t, err := suite.engine.CreateTask("workflow-1", "email", []byte("test@example.com"))
	require.NoError(suite.T(), err)

	// Transition to dispatched
	_, err = suite.engine.TransitionTask(t.ID, task.StateScheduled)
	require.NoError(suite.T(), err)
	_, err = suite.engine.TransitionTask(t.ID, task.StateDispatched)
	require.NoError(suite.T(), err)

	// Handle failed result
	result := &proto.TaskResult{
		TaskId:      t.ID,
		Success:     false,
		Error:       "timeout",
		CompletedAt: time.Now().Unix(),
	}

	handler.handleTaskResult(result)

	// Verify task is failed
	failed, err := suite.engine.GetTask(t.ID)
	require.NoError(suite.T(), err)
	assert.Equal(suite.T(), task.StateFailed, failed.State)
	assert.Equal(suite.T(), "timeout", failed.Error)
}

func (suite *GatewayServiceTestSuite) TestHandleTaskResultNil() {
	handler := NewWorkerServiceHandler(suite.logger, suite.engine)

	// Should not crash
	handler.handleTaskResult(nil)
}

func TestGatewayServiceTestSuite(t *testing.T) {
	suite.Run(t, new(GatewayServiceTestSuite))
}
