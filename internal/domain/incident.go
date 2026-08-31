package domain

import (
	"time"
)

type IncidentSeverity string

const (
	SeverityMinor    IncidentSeverity = "minor"
	SeverityMajor    IncidentSeverity = "major"
	SeverityCritical IncidentSeverity = "critical"
)

type IncidentStatus string

const (
	StatusInvestigating IncidentStatus = "investigating"
	StatusIdentified    IncidentStatus = "identified"
	StatusMonitoring    IncidentStatus = "monitoring"
	StatusResolved      IncidentStatus = "resolved"
)

type Incident struct {
	ID          string     `json:"id" db:"id"`
	Title       string     `json:"title" db:"title"`
	Description string     `json:"description" db:"description"`
	Severity    string     `json:"severity" db:"severity"`
	Status      string     `json:"status" db:"status"`
	CreatedAt   time.Time  `json:"created_at" db:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at" db:"resolved_at"`
}

type CreateIncidentRequest struct {
	Title       string `json:"title" validate:"required"`
	Description string `json:"description" validate:"required"`
	Severity    string `json:"severity" validate:"required,oneof=minor major critical"`
}

type UpdateIncidentRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	Severity    *string `json:"severity" validate:"omitempty,oneof=minor major critical"`
	Status      *string `json:"status" validate:"omitempty,oneof=investigating identified monitoring resolved"`
}
