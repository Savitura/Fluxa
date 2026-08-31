package status

import (
	"context"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockRepository struct {
	mock.Mock
}

func (m *MockRepository) Create(ctx context.Context, inc *domain.Incident) error {
	args := m.Called(ctx, inc)
	inc.ID = "test-id"
	return args.Error(0)
}

func (m *MockRepository) GetByID(ctx context.Context, id string) (*domain.Incident, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Incident), args.Error(1)
}

func (m *MockRepository) List(ctx context.Context, limit int) ([]domain.Incident, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]domain.Incident), args.Error(1)
}

func (m *MockRepository) Update(ctx context.Context, inc *domain.Incident) error {
	args := m.Called(ctx, inc)
	return args.Error(0)
}

func TestService_GetStatus_Operational(t *testing.T) {
	repo := new(MockRepository)
	repo.On("List", mock.Anything, 20).Return([]domain.Incident{}, nil)

	svc := NewService(repo)
	res, err := svc.GetStatus(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "operational", res.Status)
	assert.Equal(t, "v1", res.APIVersion)
	repo.AssertExpectations(t)
}

func TestService_GetStatus_Outage(t *testing.T) {
	repo := new(MockRepository)
	repo.On("List", mock.Anything, 20).Return([]domain.Incident{
		{ID: "1", Title: "API Down", Severity: string(domain.SeverityCritical), Status: string(domain.StatusInvestigating)},
	}, nil)

	svc := NewService(repo)
	res, err := svc.GetStatus(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, "outage", res.Status)
	repo.AssertExpectations(t)
}

func TestService_IncidentEscalationLifecycle(t *testing.T) {
	repo := new(MockRepository)
	inc := &domain.Incident{
		ID:          "inc-1",
		Title:       "Gateway Latency",
		Description: "Investigating high latency",
		Severity:    string(domain.SeverityMajor),
		Status:      string(domain.StatusInvestigating),
	}

	repo.On("GetByID", mock.Anything, "inc-1").Return(inc, nil)
	repo.On("Update", mock.Anything, mock.Anything).Return(nil)

	svc := NewService(repo)

	// Transition: investigating -> identified
	statusIdentified := string(domain.StatusIdentified)
	updated, err := svc.UpdateIncident(context.Background(), "inc-1", domain.UpdateIncidentRequest{Status: &statusIdentified})
	assert.NoError(t, err)
	assert.Equal(t, string(domain.StatusIdentified), updated.Status)

	// Transition: identified -> monitoring
	statusMonitoring := string(domain.StatusMonitoring)
	repo.On("GetByID", mock.Anything, "inc-1").Return(updated, nil)
	updated, err = svc.UpdateIncident(context.Background(), "inc-1", domain.UpdateIncidentRequest{Status: &statusMonitoring})
	assert.NoError(t, err)
	assert.Equal(t, string(domain.StatusMonitoring), updated.Status)

	// Transition: monitoring -> resolved
	statusResolved := string(domain.StatusResolved)
	repo.On("GetByID", mock.Anything, "inc-1").Return(updated, nil)
	updated, err = svc.UpdateIncident(context.Background(), "inc-1", domain.UpdateIncidentRequest{Status: &statusResolved})
	assert.NoError(t, err)
	assert.Equal(t, string(domain.StatusResolved), updated.Status)
	assert.NotNil(t, updated.ResolvedAt)

	repo.AssertExpectations(t)
}
