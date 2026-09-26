-- Membership audit trail for organization role changes and removals.
--
-- Records who tried what to whom and how it ended, including attempts the
-- last-owner guard refused. A refused demotion or removal is exactly the event an
-- operator needs to reconstruct, so `outcome` distinguishes applied, refused and
-- missed attempts instead of only recording the successes.
--
-- Both user references are ON DELETE SET NULL: the trail has to outlive the
-- accounts it describes. `actor_user_id` is nullable because the tenant
-- middleware does not attach a user on every path, and `target_user_id` is
-- nullable because the target of a missed removal is not a user of this tenant,
-- so no user row exists to point at.
CREATE TABLE organization_member_audit (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    target_user_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    action          VARCHAR(32) NOT NULL,
    previous_role   VARCHAR(32) NOT NULL DEFAULT '',
    new_role        VARCHAR(32) NOT NULL DEFAULT '',
    outcome         VARCHAR(16) NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT ck_org_member_audit_outcome CHECK (outcome IN ('ok', 'rejected', 'not_found'))
);

CREATE INDEX idx_org_member_audit_tenant ON organization_member_audit(tenant_id, created_at DESC);
CREATE INDEX idx_org_member_audit_target ON organization_member_audit(target_user_id) WHERE target_user_id IS NOT NULL;
