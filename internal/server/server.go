package server

import (
	"github.com/fluxa/fluxa/internal/status"
	"github.com/fluxa/fluxa/internal/postgres"
	// ... other imports ...
)

// Within Server struct initialization or Router setup, wire status handler:
// incidentRepo := postgres.NewIncidentRepository(db)
// statusSvc := status.NewService(incidentRepo)
// statusHandler := status.NewHandler(statusSvc)
// statusHandler.RegisterRoutes(r)
// statusHandler.RegisterAdminRoutes(r)
