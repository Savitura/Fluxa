package status

import (
	"context"
	"github.com/fluxa/fluxa/internal/domain"
)

type Repository interface {
	Create(ctx context.Context, inc *domain.Incident) error
	GetByID(ctx context.Context, id string) (*domain.Incident, error)
	List(ctx context.Context, limit int) ([]domain.Incident, error)
	Update(ctx context.Context, inc *domain.Incident) error
}
