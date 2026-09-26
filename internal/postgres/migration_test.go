package postgres_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/jackc/pgx/v5"
)

func TestMigrations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping migration test in short mode")
	}

	// Start ephemeral postgres container
	cmd := exec.Command("docker", "run", "--rm", "-d", "-e", "POSTGRES_PASSWORD=fluxa", "-P", "postgres:15-alpine")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	containerID := strings.TrimSpace(string(out))
	t.Cleanup(func() {
		_ = exec.Command("docker", "stop", containerID).Run()
	})

	// Get the bound port
	portCmd := exec.Command("docker", "port", containerID, "5432/tcp")
	var port string
	for i := 0; i < 20; i++ {
		out, err = portCmd.Output()
		if err == nil && len(out) > 0 {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			parts := strings.Split(lines[0], ":")
			if len(parts) > 1 {
				port = parts[len(parts)-1]
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if port == "" {
		t.Fatalf("could not determine bound port for postgres container")
	}

	dbURL := fmt.Sprintf("postgres://postgres:fluxa@localhost:%s/postgres?sslmode=disable", port)

	// Wait for db to be ready
	var ready bool
	for i := 0; i < 20; i++ {
		conn, err := pgx.Connect(context.Background(), dbURL)
		if err == nil {
			conn.Close(context.Background())
			ready = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ready {
		t.Fatalf("database did not become ready in time")
	}

	// 1. Run migrations to completion
	err = postgres.RunMigrations(dbURL, "../../db/migrations")
	if err != nil {
		t.Fatalf("first migration run failed: %v", err)
	}

	// 2. Rerun migrations with no changes
	err = postgres.RunMigrations(dbURL, "../../db/migrations")
	if err != nil {
		t.Fatalf("second migration run failed: %v", err)
	}

	// 3. Verify schema_migrations is not dirty
	conn, err := pgx.Connect(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("failed to connect to db to check schema_migrations: %v", err)
	}
	defer conn.Close(context.Background())

	var dirty bool
	err = conn.QueryRow(context.Background(), "SELECT dirty FROM schema_migrations LIMIT 1").Scan(&dirty)
	if err != nil {
		t.Fatalf("failed to query schema_migrations: %v", err)
	}
	if dirty {
		t.Fatalf("schema_migrations is dirty after migration")
	}
}
