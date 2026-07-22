package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/domain"
)

func TestJWTGenerationAndParsing(t *testing.T) {
	secret := []byte("test-jwt-secret-key-32-bytes-long!")

	token, err := auth.GenerateToken("user-123", "tenant-456", domain.RoleOwner, "dev@example.com", "access", secret, 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	claims, err := auth.ParseToken(token, secret)
	if err != nil {
		t.Fatalf("ParseToken failed: %v", err)
	}

	if claims.Sub != "user-123" {
		t.Errorf("expected Sub user-123, got %s", claims.Sub)
	}
	if claims.TenantID != "tenant-456" {
		t.Errorf("expected TenantID tenant-456, got %s", claims.TenantID)
	}
	if claims.Role != domain.RoleOwner {
		t.Errorf("expected Role owner, got %s", claims.Role)
	}
	if claims.Email != "dev@example.com" {
		t.Errorf("expected Email dev@example.com, got %s", claims.Email)
	}
	if claims.TokenType != "access" {
		t.Errorf("expected TokenType access, got %s", claims.TokenType)
	}
}

func TestExpiredJWT(t *testing.T) {
	secret := []byte("test-jwt-secret-key-32-bytes-long!")

	token, err := auth.GenerateToken("user-123", "tenant-456", domain.RoleDeveloper, "dev@example.com", "access", secret, -1*time.Second)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	_, err = auth.ParseToken(token, secret)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestInvalidJWTSecret(t *testing.T) {
	secret := []byte("test-jwt-secret-key-32-bytes-long!")
	wrongSecret := []byte("wrong-secret-key-32-bytes-long!!")

	token, err := auth.GenerateToken("user-123", "tenant-456", domain.RoleDeveloper, "dev@example.com", "access", secret, 1*time.Hour)
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}

	_, err = auth.ParseToken(token, wrongSecret)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}
