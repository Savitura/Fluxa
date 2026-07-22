package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"time"
)

type RegisterRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	Name        string `json:"name"`
	AccountType string `json:"account_type"` // individual | organization
	OrgName     string `json:"org_name"`      // required if account_type == organization
}

type AuthResponse struct {
	User         *domain.User   `json:"user"`
	Tenant       *domain.Tenant `json:"tenant"`
	Role         string         `json:"role"`
	AccessToken  string         `json:"access_token"`
	RefreshToken string         `json:"refresh_token"`
}

type Service interface {
	Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error)
	Login(ctx context.Context, email, password string) (*AuthResponse, error)
	RefreshToken(ctx context.Context, refreshTokenStr string) (*AuthResponse, error)
}

type service struct {
	userRepo   *postgres.UserRepo
	tenantRepo *postgres.TenantRepo
	orgRepo    *postgres.OrgRepo
	jwtSecret  []byte
}

func NewService(
	userRepo *postgres.UserRepo,
	tenantRepo *postgres.TenantRepo,
	orgRepo *postgres.OrgRepo,
	jwtSecret []byte,
) Service {
	return &service{
		userRepo:   userRepo,
		tenantRepo: tenantRepo,
		orgRepo:    orgRepo,
		jwtSecret:  jwtSecret,
	}
}

func (s *service) Register(ctx context.Context, req RegisterRequest) (*AuthResponse, error) {
	if req.Email == "" || req.Password == "" || req.Name == "" {
		return nil, errors.New("email, password, and name are required")
	}

	if req.AccountType == "" {
		req.AccountType = domain.AccountTypeIndividual
	}
	if req.AccountType != domain.AccountTypeIndividual && req.AccountType != domain.AccountTypeOrganization {
		return nil, errors.New("account_type must be 'individual' or 'organization'")
	}
	if req.AccountType == domain.AccountTypeOrganization && req.OrgName == "" {
		return nil, errors.New("org_name is required for organization account registration")
	}

	// Check if email is already taken
	existing, err := s.userRepo.GetByEmail(ctx, req.Email)
	if err == nil && existing != nil {
		return nil, domain.ErrUserAlreadyExists
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}

	userID := uuid.New().String()
	tenantID := uuid.New().String()
	now := time.Now().UTC()

	user := &domain.User{
		ID:           userID,
		Email:        req.Email,
		PasswordHash: string(hash),
		Name:         req.Name,
		CreatedAt:    now,
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}

	tenantName := req.Name + "'s Tenant"
	if req.AccountType == domain.AccountTypeOrganization {
		tenantName = req.OrgName
	}

	t := &domain.Tenant{
		ID:          tenantID,
		Name:        tenantName,
		Email:       req.Email,
		AccountType: req.AccountType,
		CreatedAt:   now,
	}

	if err := s.tenantRepo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("create tenant: %w", err)
	}

	member := &domain.OrgMember{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		UserID:    userID,
		Role:      domain.RoleOwner,
		CreatedAt: now,
	}

	if err := s.orgRepo.AddMember(ctx, member); err != nil {
		return nil, fmt.Errorf("add owner member: %w", err)
	}

	accessToken, err := GenerateToken(userID, tenantID, domain.RoleOwner, user.Email, "access", s.jwtSecret, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := GenerateToken(userID, tenantID, domain.RoleOwner, user.Email, "refresh", 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &AuthResponse{
		User:         user,
		Tenant:       t,
		Role:         domain.RoleOwner,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *service) Login(ctx context.Context, email, password string) (*AuthResponse, error) {
	if email == "" || password == "" {
		return nil, domain.ErrInvalidCredentials
	}

	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, domain.ErrInvalidCredentials
	}

	member, err := s.orgRepo.GetUserActiveMember(ctx, user.ID)
	if err != nil {
		return nil, fmt.Errorf("get user active tenant: %w", err)
	}

	t, err := s.tenantRepo.GetByID(ctx, member.TenantID)
	if err != nil {
		return nil, fmt.Errorf("get tenant: %w", err)
	}

	accessToken, err := GenerateToken(user.ID, t.ID, member.Role, user.Email, "access", s.jwtSecret, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := GenerateToken(user.ID, t.ID, member.Role, user.Email, "refresh", 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &AuthResponse{
		User:         user,
		Tenant:       t,
		Role:         member.Role,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

func (s *service) RefreshToken(ctx context.Context, refreshTokenStr string) (*AuthResponse, error) {
	claims, err := ParseToken(refreshTokenStr, s.jwtSecret)
	if err != nil || claims.TokenType != "refresh" {
		return nil, errors.New("invalid or expired refresh token")
	}

	user, err := s.userRepo.GetByID(ctx, claims.Sub)
	if err != nil {
		return nil, domain.ErrUserNotFound
	}

	member, err := s.orgRepo.GetMember(ctx, claims.TenantID, claims.Sub)
	if err != nil {
		return nil, domain.ErrOrgMemberNotFound
	}

	t, err := s.tenantRepo.GetByID(ctx, claims.TenantID)
	if err != nil {
		return nil, errors.New("tenant not found")
	}

	accessToken, err := GenerateToken(user.ID, t.ID, member.Role, user.Email, "access", s.jwtSecret, 24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	newRefreshToken, err := GenerateToken(user.ID, t.ID, member.Role, user.Email, "refresh", 7*24*time.Hour)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	return &AuthResponse{
		User:         user,
		Tenant:       t,
		Role:         member.Role,
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
	}, nil
}
