package logging

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
)

// WorkerMiddleware logs the outcome and duration of every Asynq job.
func WorkerMiddleware(logger zerolog.Logger) asynq.MiddlewareFunc {
	return func(next asynq.Handler) asynq.Handler {
		return asynq.HandlerFunc(func(ctx context.Context, task *asynq.Task) error {
			start := time.Now()

			err := next.ProcessTask(ctx, task)
			duration := time.Since(start)

			if err != nil {
				logger.Error().
					Err(err).
					Str("job_type", task.Type()).
					Str("status", "failure").
					Str("operation", "worker_job").
					Dur("duration", duration).
					Msg("worker job completed")

				return err
			}

			logger.Info().
				Str("job_type", task.Type()).
				Str("status", "success").
				Str("operation", "worker_job").
				Dur("duration", duration).
				Msg("worker job completed")

			return nil
		})
	}
}
