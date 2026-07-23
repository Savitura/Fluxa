package org_test

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/org"
)

type mockOrgRepo struct {
	members map[string]*domain.OrgMember
	invites map[string]*domain.OrgInvite
}

func newMockOrgRepo() *mockOrgRepo {
	return &mockOrgRepo{
		members: make(map[string]*domain.OrgMember),
		invites: make(map[string]*domain.OrgInvite),
	}
}

func (m *mockOrgRepo) AddMember(ctx context.Context, mem *domain.OrgMember) error {
	m.members[mem.TenantID+":"+mem.UserID] = mem
	return nil
}

func (m *mockOrgRepo) GetMember(ctx context.Context, tenantID, userID string) (*domain.OrgMember, error) {
	mem, ok := m.members[tenantID+":"+userID]
	if !ok {
		return nil, domain.ErrOrgMemberNotFound
	}
	return mem, nil
}

func (m *mockOrgRepo) GetUserActiveMember(ctx context.Context, userID string) (*domain.OrgMember, error) {
	for _, mem := range m.members {
		if mem.UserID == userID {
			return mem, nil
		}
	}
	return nil, domain.ErrOrgMemberNotFound
}

func (m *mockOrgRepo) ListMembers(ctx context.Context, tenantID string) ([]*domain.OrgMember, error) {
	var list []*domain.OrgMember
	for _, mem := range m.members {
		if mem.TenantID == tenantID {
			list = append(list, mem)
		}
	}
	return list, nil
}

func (m *mockOrgRepo) UpdateMemberRole(ctx context.Context, tenantID, userID, newRole string) error {
	mem, ok := m.members[tenantID+":"+userID]
	if !ok {
		return domain.ErrOrgMemberNotFound
	}
	mem.Role = newRole
	return nil
}

func (m *mockOrgRepo) RemoveMember(ctx context.Context, tenantID, userID string) error {
	key := tenantID + ":" + userID
	if _, ok := m.members[key]; !ok {
		return domain.ErrOrgMemberNotFound
	}
	delete(m.members, key)
	return nil
}

func (m *mockOrgRepo) CreateInvite(ctx context.Context, inv *domain.OrgInvite) error {
	m.invites[inv.Token] = inv
	return nil
}

func (m *mockOrgRepo) GetInviteByToken(ctx context.Context, token string) (*domain.OrgInvite, error) {
	inv, ok := m.invites[token]
	if !ok {
		return nil, domain.ErrInviteNotFound
	}
	return inv, nil
}

func (m *mockOrgRepo) UpdateInviteStatus(ctx context.Context, inviteID, status string) error {
	for _, inv := range m.invites {
		if inv.ID == inviteID {
			inv.Status = status
			return nil
		}
	}
	return nil
}
