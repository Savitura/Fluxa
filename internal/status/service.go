package status

import (
	"context"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

type StatusResponse struct {
	APIVersion     string            `json:"api_version"`
	Status         string            `json:"status"` // operational, degraded, outage
	Message        string            `json:"message"`
	RecentIncidents []domain.Incident `json:"recent_incidents"`
}

func (s *Service) GetStatus(ctx context.Context) (*StatusResponse, error {
	incidents, err := s.repo.List(ctx, 20)
	if err != nil {
		return nil, err
	}

	operationalStatus := "operational"
	message := "All systems operational"

	// Check active incidents
	var activeIncidents []domain.Incident
	for _, inc := range incidents {
		if inc.Status != string(domain.StatusResolved) {
			activeIncidents = append(activeIncidents, inc)
		}
	}

	if len(activeIncidents) > 0 {
		highestSeverity := domain.SeverityMinor
		for _, inc := range activeIncidents {
			switch domain.IncidentSeverity(inc.Severity) {
			case domain.SeverityCritical:
				highestSeverity = domain.SeverityCritical
			case domain.SeverityMajor:
				if highestSeverity != domain.SeverityCritical {
					highestSeverity = domain.SeverityMajor
				}
			}
		}

		if highestSeverity == domain.SeverityCritical {
			operationalStatus = "outage"
			message = "Critical incident affecting API availability"
		} else if highestSeverity == domain.SeverityMajor {
			operationalStatus = "degraded"
			message = "Major incident affecting system performance"
		} else {
			operationalStatus = "degraded"
			message = "Minor incident reported"
		}
	}

	// Get recent incidents (last 5)
	recent := incidents
	if len(recent) > 5 {
		recent = recent[:5]
	}

	return &StatusResponse{
		APIVersion:      "v1",
		Status:          operationalStatus,
		Message:         message,
		RecentIncidents: recent,
	}, nil
}

func (s *Service) ListIncidents(ctx context.Context) ([]domain.Incident, error) {
	return s.repo.List(ctx, 50)
}

func (s *Service) CreateIncident(ctx context.Context, req domain.CreateIncidentRequest) (*domain.Incident, error) {
	inc := &domain.Incident{
		Title:       req.Title,
		Description: req.Description,
		Severity:    req.Severity,
		Status:      string(domain.StatusInvestigating),
		CreatedAt:   time.Now(),
	}
	err := s.repo.Create(ctx, inc)
	return inc, err
}

func (s *Service) UpdateIncident(ctx context.Context, id string, req domain.UpdateIncidentRequest) (*domain.Incident, error) {
	inc, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	if req.Title != nil {
		inc.Title = *req.Title
	}
	if req.Description != nil {
		inc.Description = *req.Description
	}
	if req.Severity != nil {
		inc.Severity = *req.Severity
	}
	if req.Status != nil {
		newStatus := *req.Status
		if newStatus == string(domain.StatusResolved) && inc.Status != string(domain.StatusResolved) {
			now := time.Now()
			inc.ResolvedAt = &now
		} else if newStatus != string(domain.StatusResolved) {
			inc.ResolvedAt = nil
		}
		inc.Status = newStatus
	}

	err = s.repo.Update(ctx, inc)
	return inc, err
}
