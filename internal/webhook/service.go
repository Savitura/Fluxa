package webhook

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type WebhookEndpoint struct {
	ID     string   `json:"id" db:"id"`
	URL    string   `json:"url" db:"url"`
	Events []string `json:"events" db:"events"`
	Active bool     `json:"active" db:"active"`
}

type WebhookDelivery struct {
	ID         string    `json:"id" db:"id"`
	EndpointID string    `json:"endpoint_id" db:"endpoint_id"`
	Status     string    `json:"status" db:"status"`
	StatusCode int       `json:"status_code" db:"status_code"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
}

type Service struct {
	endpoints  map[string][]WebhookEndpoint
	deliveries map[string][]WebhookDelivery
	secrets    map[string]string
}

func NewService() *Service {
	return &Service{
		endpoints:  make(map[string][]WebhookEndpoint),
		deliveries: make(map[string][]WebhookDelivery),
		secrets:    make(map[string]string),
	}
}

func (s *Service) ListEndpoints(ctx context.Context, orgID string) ([]WebhookEndpoint, error) {
	return s.endpoints[orgID], nil
}

func (s *Service) RegisterEndpoint(ctx context.Context, orgID, url string, events []string) (WebhookEndpoint, error) {
	ep := WebhookEndpoint{
		ID:     uuid.New().String(),
		URL:    url,
		Events: events,
		Active: true,
	}
	s.endpoints[orgID] = append(s.endpoints[orgID], ep)
	return ep, nil
}

func (s *Service) DeleteEndpoint(ctx context.Context, id string) error {
	for orgID, eps := range s.endpoints {
		for i, ep := range eps {
			if ep.ID == id {
				s.endpoints[orgID] = append(eps[:i], eps[i+1:]...)
				return nil
			}
		}
	}
	return nil
}

func (s *Service) ListDeliveries(ctx context.Context, endpointID string) ([]WebhookDelivery, error) {
	return s.deliveries[endpointID], nil
}

func (s *Service) GetSigningSecret(ctx context.Context, orgID string) (string, error) {
	if secret, ok := s.secrets[orgID]; ok {
		return secret, nil
	}
	secret, err := generateSigningSecret()
	if err != nil {
		return "", err
	}
	s.secrets[orgID] = secret
	return secret, nil
}

func (s *Service) RotateSigningSecret(ctx context.Context, orgID string) (string, error) {
	secret, err := generateSigningSecret()
	if err != nil {
		return "", err
	}
	s.secrets[orgID] = secret
	return secret, nil
}

func generateSigningSecret() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "whsec_" + hex.EncodeToString(bytes), nil
}
