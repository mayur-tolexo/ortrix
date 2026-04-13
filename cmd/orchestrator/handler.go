package main

import (
	"io"

	pb "github.com/mayur-tolexo/flowd/api/proto"
	"github.com/sirupsen/logrus"
)

// WorkerHandler implements the WorkerService gRPC server.
// It manages bidirectional streaming between workers and the orchestrator.
type WorkerHandler struct {
	pb.UnimplementedWorkerServiceServer
	logger *logrus.Logger
}

// NewWorkerHandler creates a new WorkerHandler.
func NewWorkerHandler(logger *logrus.Logger) *WorkerHandler {
	return &WorkerHandler{logger: logger}
}

// StreamTasks handles the bidirectional gRPC stream between a worker and the orchestrator.
// In the full implementation, this will:
//   - Receive WorkerRegistration and register the worker's capabilities.
//   - Dispatch tasks from the scheduler to the worker.
//   - Receive TaskResults and update task state.
//   - Handle heartbeats for worker liveness detection.
func (h *WorkerHandler) StreamTasks(stream pb.WorkerService_StreamTasksServer) error {
	h.logger.Info("new worker stream connected")

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			h.logger.Info("worker stream closed")
			return nil
		}
		if err != nil {
			h.logger.WithError(err).Error("error receiving from worker stream")
			return err
		}

		switch payload := msg.GetPayload().(type) {
		case *pb.WorkerMessage_Registration:
			h.logger.WithFields(logrus.Fields{
				"worker_id":    payload.Registration.GetWorkerId(),
				"capabilities": payload.Registration.GetCapabilities(),
			}).Info("worker registered")

			// TODO: Register worker in the scheduler's worker pool.
			// TODO: Begin dispatching tasks matching the worker's capabilities.

			if err := stream.Send(&pb.OrchestratorMessage{
				Payload: &pb.OrchestratorMessage_Ack{
					Ack: &pb.Ack{Message: "registered"},
				},
			}); err != nil {
				return err
			}

		case *pb.WorkerMessage_Result:
			h.logger.WithFields(logrus.Fields{
				"task_id": payload.Result.GetTaskId(),
				"success": payload.Result.GetSuccess(),
			}).Info("task result received")

			// TODO: Update task state in the WAL and state store.
			// TODO: Acknowledge result to the worker.

		case *pb.WorkerMessage_Heartbeat:
			h.logger.WithField("worker_id", payload.Heartbeat.GetWorkerId()).Debug("heartbeat received")

			// TODO: Update worker liveness in the scheduler.

		default:
			h.logger.Warn("unknown worker message type")
		}
	}
}
