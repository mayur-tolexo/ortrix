package sdk

import (
	"context"
	"testing"
)

func TestNewWorker(t *testing.T) {
	w := NewWorker("test-worker-1")
	if w.ID() != "test-worker-1" {
		t.Errorf("expected ID test-worker-1, got %s", w.ID())
	}
	if len(w.Capabilities()) != 0 {
		t.Errorf("expected no capabilities, got %v", w.Capabilities())
	}
}

func TestRegisterHandler(t *testing.T) {
	w := NewWorker("test-worker-2")

	handler := func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	if err := w.RegisterHandler("image-resize", handler); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	caps := w.Capabilities()
	if len(caps) != 1 || caps[0] != "image-resize" {
		t.Errorf("expected [image-resize], got %v", caps)
	}

	h, ok := w.GetHandler("image-resize")
	if !ok {
		t.Fatal("expected handler to be found")
	}
	result, err := h(context.Background(), "task-1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(result) != "ok" {
		t.Errorf("expected ok, got %s", string(result))
	}
}

func TestRegisterHandlerEmptyType(t *testing.T) {
	w := NewWorker("test-worker-3")
	err := w.RegisterHandler("", func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return nil, nil
	})
	if err == nil {
		t.Error("expected error for empty task type")
	}
}

func TestRegisterHandlerNilHandler(t *testing.T) {
	w := NewWorker("test-worker-4")
	err := w.RegisterHandler("some-type", nil)
	if err == nil {
		t.Error("expected error for nil handler")
	}
}

func TestGetHandlerNotFound(t *testing.T) {
	w := NewWorker("test-worker-5")
	_, ok := w.GetHandler("nonexistent")
	if ok {
		t.Error("expected handler not to be found")
	}
}

func TestStartNotImplemented(t *testing.T) {
	w := NewWorker("test-worker-6")
	err := w.Start(context.Background(), "localhost:9090")
	if err == nil {
		t.Error("expected error from unimplemented Start")
	}
}

func TestRegisterHandlerDuplicateType(t *testing.T) {
	w := NewWorker("test-worker-7")

	handler1 := func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("v1"), nil
	}
	handler2 := func(_ context.Context, _ string, _ []byte) ([]byte, error) {
		return []byte("v2"), nil
	}

	if err := w.RegisterHandler("image-resize", handler1); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := w.RegisterHandler("image-resize", handler2); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	caps := w.Capabilities()
	if len(caps) != 1 {
		t.Errorf("expected 1 capability, got %d: %v", len(caps), caps)
	}

	h, _ := w.GetHandler("image-resize")
	result, _ := h(context.Background(), "task-1", nil)
	if string(result) != "v2" {
		t.Errorf("expected handler to be updated to v2, got %s", string(result))
	}
}
