package domain

import (
	"time"
)

type Tenant struct {
	ID            string    `json:"id" db:"id"`
	Name          string    `json:"name" db:"name"`
	SigningSecret string    `json:"signing_secret,omitempty" db:"signing_secret"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}
