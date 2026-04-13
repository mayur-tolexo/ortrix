package main

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
)

// StartWorkflowRequest represents the input to start a workflow.
type StartWorkflowRequest struct {
	// WorkflowName is the name of the workflow to execute.
	WorkflowName string `json:"workflow_name" example:"order-processing"`
	// Input is the JSON payload for the workflow.
	Input map[string]interface{} `json:"input"`
}

// StartWorkflowResponse represents the result of starting a workflow.
type StartWorkflowResponse struct {
	// WorkflowID is the unique identifier of the started workflow.
	WorkflowID string `json:"workflow_id" example:"wf-abc123"`
	// Status is the current status of the workflow.
	Status string `json:"status" example:"started"`
	// Message provides additional details.
	Message string `json:"message" example:"workflow started successfully"`
}

// GetWorkflowStatusResponse represents the status of a workflow.
type GetWorkflowStatusResponse struct {
	// WorkflowID is the unique identifier of the workflow.
	WorkflowID string `json:"workflow_id" example:"wf-abc123"`
	// Status is the current status of the workflow.
	Status string `json:"status" example:"running"`
}

// ErrorResponse represents an error returned by the API.
type ErrorResponse struct {
	// Error is the error message.
	Error string `json:"error" example:"invalid request"`
}

// HTTPHandler provides HTTP/REST endpoints for the gateway.
type HTTPHandler struct {
	logger *logrus.Logger
}

// NewHTTPHandler creates a new HTTPHandler.
func NewHTTPHandler(logger *logrus.Logger) *HTTPHandler {
	return &HTTPHandler{logger: logger}
}

// StartWorkflow godoc
// @Summary Start a workflow
// @Description Starts a new workflow execution
// @Tags workflow
// @Accept json
// @Produce json
// @Param request body StartWorkflowRequest true "Workflow input"
// @Success 200 {object} StartWorkflowResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/v1/workflows/start [post]
func (h *HTTPHandler) StartWorkflow(w http.ResponseWriter, r *http.Request) {
	var req StartWorkflowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "invalid request body"})
		return
	}

	if req.WorkflowName == "" {
		h.writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "workflow_name is required"})
		return
	}

	h.logger.WithField("workflow_name", req.WorkflowName).Info("workflow started via REST API")

	// TODO: Route to the orchestrator via the gRPC layer.

	h.writeJSON(w, http.StatusOK, StartWorkflowResponse{
		WorkflowID: "wf-placeholder",
		Status:     "started",
		Message:    "workflow started successfully",
	})
}

// GetWorkflowStatus godoc
// @Summary Get workflow status
// @Description Returns the current status of a workflow
// @Tags workflow
// @Accept json
// @Produce json
// @Param id path string true "Workflow ID"
// @Success 200 {object} GetWorkflowStatusResponse
// @Failure 400 {object} ErrorResponse
// @Router /api/v1/workflows/{id}/status [get]
func (h *HTTPHandler) GetWorkflowStatus(w http.ResponseWriter, r *http.Request) {
	// Extract the workflow ID from the URL path.
	// Expected path: /api/v1/workflows/{id}/status
	id := extractPathParam(r.URL.Path, "/api/v1/workflows/", "/status")
	if id == "" {
		h.writeJSON(w, http.StatusBadRequest, ErrorResponse{Error: "workflow id is required"})
		return
	}

	h.logger.WithField("workflow_id", id).Info("workflow status queried via REST API")

	// TODO: Query the orchestrator for the actual workflow status.

	h.writeJSON(w, http.StatusOK, GetWorkflowStatusResponse{
		WorkflowID: id,
		Status:     "running",
	})
}

// HealthCheck godoc
// @Summary Health check
// @Description Returns the health status of the gateway service
// @Tags system
// @Produce json
// @Success 200 {object} map[string]string
// @Router /healthz [get]
func (h *HTTPHandler) HealthCheck(w http.ResponseWriter, _ *http.Request) {
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeJSON encodes a value as JSON and writes it to the response writer.
func (h *HTTPHandler) writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		h.logger.WithError(err).Error("failed to encode response")
	}
}

// extractPathParam extracts a dynamic segment from a URL path.
// For example, given path "/api/v1/workflows/abc/status", prefix "/api/v1/workflows/", and suffix "/status",
// it returns "abc".
func extractPathParam(path, prefix, suffix string) string {
	if len(path) <= len(prefix)+len(suffix) {
		return ""
	}
	trimmed := path[len(prefix):]
	if len(suffix) > 0 && len(trimmed) > len(suffix) {
		trimmed = trimmed[:len(trimmed)-len(suffix)]
	}
	return trimmed
}
