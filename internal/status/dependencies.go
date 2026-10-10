package status

import (
	"context"
	"regexp"
	"sort"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/health"
)

var dependencyNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

const maxDependencySampleAge = 3 * health.SampleInterval

// DependencyHistoryRepository is the persistence contract for platform checks.
type DependencyHistoryRepository interface {
	LatestDependencyHealth(context.Context) ([]domain.DependencyHealthCheck, error)
	ListDependencyHealth(context.Context, string, *time.Time, int) ([]domain.DependencyHealthCheck, error)
}

type DependencyStatus struct {
	Dependency string     `json:"dependency"`
	Status     string     `json:"status"`
	LatencyMS  int64      `json:"latency_ms,omitempty"`
	CheckedAt  *time.Time `json:"checked_at,omitempty"`
	Stale      bool       `json:"stale"`
}

type DependencyHistoryResponse struct {
	Checks        []domain.DependencyHealthCheck `json:"checks"`
	Limit         int                            `json:"limit"`
	RetentionDays int                            `json:"retention_days"`
}

func (s *Service) WithDependencyHistory(repository DependencyHistoryRepository, expected []string) *Service {
	s.dependencyHistory = repository
	s.expectedDependencies = append([]string(nil), expected...)
	sort.Strings(s.expectedDependencies)
	return s
}

func (s *Service) dependencyStatuses(ctx context.Context) ([]DependencyStatus, bool, bool, error) {
	if s.dependencyHistory == nil {
		return nil, false, false, nil
	}
	latest, err := s.dependencyHistory.LatestDependencyHealth(ctx)
	if err != nil {
		return nil, false, false, err
	}
	byName := make(map[string]domain.DependencyHealthCheck, len(latest))
	for _, check := range latest {
		byName[check.Dependency] = check
	}

	names := append([]string(nil), s.expectedDependencies...)
	if len(names) == 0 {
		for name := range byName {
			names = append(names, name)
		}
		sort.Strings(names)
	}
	if len(names) == 0 {
		return []DependencyStatus{}, false, false, nil
	}

	now := time.Now().UTC()
	statuses := make([]DependencyStatus, 0, len(names))
	unhealthy := 0
	unknown := 0
	for _, name := range names {
		check, ok := byName[name]
		if !ok || now.Sub(check.CheckedAt) > maxDependencySampleAge {
			statuses = append(statuses, DependencyStatus{
				Dependency: name,
				Status:     "unknown",
				Stale:      true,
			})
			unknown++
			continue
		}
		checkedAt := check.CheckedAt
		statuses = append(statuses, DependencyStatus{
			Dependency: name,
			Status:     check.Status,
			LatencyMS:  check.LatencyMS,
			CheckedAt:  &checkedAt,
		})
		if check.Status == "unhealthy" {
			unhealthy++
		}
	}
	return statuses, unhealthy > 0 || unknown > 0, unhealthy == len(statuses), nil
}

func (s *Service) ListDependencyHistory(ctx context.Context, dependency string, since *time.Time, limit int) ([]domain.DependencyHealthCheck, error) {
	if s.dependencyHistory == nil {
		return []domain.DependencyHealthCheck{}, nil
	}
	return s.dependencyHistory.ListDependencyHealth(ctx, dependency, since, limit)
}
