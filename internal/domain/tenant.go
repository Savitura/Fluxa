package domain

import "time"

type Tenant struct {
	ID        string
	Name      string
	Email     string
	CreatedAt time.Time
}
