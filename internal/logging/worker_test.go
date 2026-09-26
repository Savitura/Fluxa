package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
)

func TestWorkerMiddlewareLogsSuccessfulJob(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "info")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	handler := WorkerMiddleware(logger)(asynq.HandlerFunc(
		func(ctx context.Context, task *asynq.Task) error {
			return nil
		},
	))

	task := asynq.NewTask("test:success", nil)

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask() error = %v", err)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}

	if entry["job_type"] != "test:success" {
		t.Errorf("job_type = %v, want test:success", entry["job_type"])
	}

	if entry["status"] != "success" {
		t.Errorf("status = %v, want success", entry["status"])
	}

	if entry["operation"] != "worker_job" {
		t.Errorf("operation = %v, want worker_job", entry["operation"])
	}

	if _, ok := entry["duration"]; !ok {
		t.Error("duration field is missing")
	}
}

func TestWorkerMiddlewareLogsFailedJob(t *testing.T) {
	var buf bytes.Buffer

	logger, err := New(&buf, "info")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	jobErr := errors.New("job failed")

	handler := WorkerMiddleware(logger)(asynq.HandlerFunc(
		func(ctx context.Context, task *asynq.Task) error {
			return jobErr
		},
	))

	task := asynq.NewTask("test:failure", nil)

	err = handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, jobErr) {
		t.Fatalf("ProcessTask() error = %v, want %v", err, jobErr)
	}

	var entry map[string]interface{}
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &entry); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}

	if entry["job_type"] != "test:failure" {
		t.Errorf("job_type = %v, want test:failure", entry["job_type"])
	}

	if entry["status"] != "failure" {
		t.Errorf("status = %v, want failure", entry["status"])
	}

	if entry["level"] != "error" {
		t.Errorf("level = %v, want error", entry["level"])
	}

	if entry["operation"] != "worker_job" {
		t.Errorf("operation = %v, want worker_job", entry["operation"])
	}

	if _, ok := entry["duration"]; !ok {
		t.Error("duration field is missing")
	}
}
