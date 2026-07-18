package schedule

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, s *domain.Schedule) error
	GetByID(ctx context.Context, id string) (*domain.Schedule, error)
	List(ctx context.Context) ([]*domain.Schedule, error)
	Update(ctx context.Context, s *domain.Schedule) error
	// ListDue returns active schedules whose next_run_at has elapsed. Called
	// by the background worker with an unscoped context, so it spans tenants.
	ListDue(ctx context.Context, now time.Time) ([]*domain.Schedule, error)
}
