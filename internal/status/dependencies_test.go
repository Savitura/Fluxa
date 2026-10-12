package status

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stretchr/testify/require"
)

type dependencyHistoryStub struct {
	latest []domain.DependencyHealthCheck
	list   []domain.DependencyHealthCheck
	err    error
}

func (s dependencyHistoryStub) LatestDependencyHealth(context.Context) ([]domain.DependencyHealthCheck, error) {
	return s.latest, s.err
}
func (s dependencyHistoryStub) ListDependencyHealth(_ context.Context, _ string, _ *time.Time, _ int) ([]domain.DependencyHealthCheck, error) {
	return s.list, s.err
}

func TestDependencyStatusesMarksMissingAndStaleChecksUnknown(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(nil).WithDependencyHistory(dependencyHistoryStub{latest: []domain.DependencyHealthCheck{{Dependency: "redis", Status: "healthy", CheckedAt: now.Add(-time.Hour)}}}, []string{"postgres", "redis"})
	statuses, degraded, unavailable, err := service.dependencyStatuses(context.Background())
	require.NoError(t, err)
	require.True(t, degraded)
	require.False(t, unavailable)
	require.Len(t, statuses, 2)
	require.Equal(t, "unknown", statuses[0].Status)
	require.True(t, statuses[0].Stale)
	require.Equal(t, "unknown", statuses[1].Status)
	require.True(t, statuses[1].Stale)
}

func TestDependencyStatusesReportsUnhealthyAndDeterministicOrder(t *testing.T) {
	now := time.Now().UTC()
	service := NewService(nil).WithDependencyHistory(dependencyHistoryStub{latest: []domain.DependencyHealthCheck{{Dependency: "postgres", Status: "unhealthy", CheckedAt: now}, {Dependency: "redis", Status: "unhealthy", CheckedAt: now}}}, []string{"redis", "postgres"})
	statuses, degraded, unavailable, err := service.dependencyStatuses(context.Background())
	require.NoError(t, err)
	require.True(t, degraded)
	require.True(t, unavailable)
	require.Equal(t, []string{"postgres", "redis"}, []string{statuses[0].Dependency, statuses[1].Dependency})
}
