package org

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

type InviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

type AcceptInviteRequest struct {
	Token    string `json:"token"`
	Name     string `json:"name,omitempty"`     // required if new user
	Password string `json:"password,omitempty"` // required if new user
}

type Service interface {
	InviteMember(ctx context.Context, email, role string) (*domain.OrgInvite, error)
	AcceptInvite(ctx context.Context, req AcceptInviteRequest) (*auth.AuthResponse, error)
	ListMembers(ctx context.Context) ([]*domain.OrgMember, error)
	UpdateRole(ctx context.Context, targetUserID, newRole string) error
	RemoveMember(ctx context.Context, targetUserID string) error
}

type service struct {
	orgRepo    *postgres.OrgRepo
	userRepo   *postgres.UserRepo
	tenantRepo *postgres.TenantRepo
	jwtSecret  []byte
}

func NewService(
	orgRepo *postgres.OrgRepo,
	userRepo *postgres.UserRepo,
	tenantRepo *postgres.TenantRepo,
	jwtSecret []byte,
) Service {
	return &service{
		orgRepo:    orgRepo,
		userRepo:   userRepo,
		tenantRepo: tenantRepo,
		jwtSecret:  jwtSecret,
	}
}

func (s *service) InviteMember(ctx context.Context, email, role string) (*domain.OrgInvite, error) {
	tenantID := tenant.IDFromContext(ctx)
	inviterID := tenant.UserIDFromContext(ctx)
	if tenantID == "" {
		return nil, errors.New("tenant not found in context")
	}

	if email == "" {
		return nil, errors.New("email is required")
	}

	switch role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleDeveloper, domain.RoleViewer:
	default:
		return nil, errors.New("invalid role; must be owner, admin, developer, or viewer")
	}

	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, fmt.Errorf("generate invite token: %w", err)
	}
	token := hex.EncodeToString(tokenBytes)

	now := time.Now().UTC()
	var inviterPtr *string
	if inviterID != "" {
		inviterPtr = &inviterID
	}

	inv := &domain.OrgInvite{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		Email:     email,
		Role:      role,
		Token:     token,
		Status:    "pending",
		InvitedBy: inviterPtr,
		CreatedAt: now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
	}

	if err := s.orgRepo.CreateInvite(ctx, inv); err != nil {
		return nil, fmt.Errorf("create invite: %w", err)
	}

	return inv, nil
}

func (s *service) AcceptInvite(ctx context.Context, req AcceptInviteRequest) (*auth.AuthResponse, error) {
	if req.Token == "" {
		return nil, errors.New("invite token is required")
	}

	inv, err := s.orgRepo.GetInviteByToken(ctx, req.Token)
	if err != nil {
		return nil, domain.ErrInviteNotFound
	}

	if inv.Status != "pending" || time.Now().UTC().After(inv.ExpiresAt) {
		return nil, errors.New("invite is invalid, already used, or expired")
	}

	var user *domain.User
	existingUser, err := s.userRepo.GetByEmail(ctx, inv.Email)
	if err == nil && existingUser != nil {
		user = existingUser
	} else {
		if req.Name == "" || req.Password == "" {
			return nil, errors.New("name and password are required to register new user from invite")
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("hash password: %w", err)
		}

		userID := uuid.New().String()
		now := time.Now().UTC()
		user = &domain.User{
			ID:           userID,
			Email:        inv.Email,
			PasswordHash: string(hash),
			Name:         req.Name,
			CreatedAt:    now,
		}

		if err := s.userRepo.Create(ctx, user); err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
	}

	member := &domain.OrgMember{
		ID:        uuid.New().String(),
		TenantID:  inv.TenantID,
		UserID:    user.ID,
		Role:      inv.Role,
		InvitedBy: inv.InvitedBy,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.orgRepo.AddMember(ctx, member); err != nil {
		return nil, fmt.Errorf("add org member: %w", err)
	}

	_ = s.orgRepo.UpdateInviteStatus(ctx, inv.ID, "accepted")

	t, err := s.tenantRepo.GetByID(ctx, inv.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	accessToken, err := auth.GenerateToken(user.ID, t.ID, member.Role, user.Email, "access", s.jwtSecret, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := auth.GenerateToken(user.ID, t.ID, member.Role, user.Email, "refresh", 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &auth.AuthResponse{
		User:         user,
		Tenant:       t,
		Role:         member.Role,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *service) ListMembers(ctx context.Context) ([]*domain.OrgMember, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return nil, errors.New("tenant not found in context")
	}
	return s.orgRepo.ListMembers(ctx, tenantID)
}

func (s *service) UpdateRole(ctx context.Context, targetUserID, newRole string) error {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return errors.New("tenant not found in context")
	}

	switch newRole {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleDeveloper, domain.RoleViewer:
	default:
		return errors.New("invalid role")
	}

	return s.orgRepo.UpdateMemberRole(ctx, tenantID, targetUserID, newRole)
}

func (s *service) RemoveMember(ctx context.Context, targetUserID string) error {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return errors.New("tenant not found in context")
	}

	return s.orgRepo.RemoveMember(ctx, tenantID, targetUserID)
}
