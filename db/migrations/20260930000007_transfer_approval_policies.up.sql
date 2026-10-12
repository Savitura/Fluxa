ALTER TYPE transaction_status ADD VALUE IF NOT EXISTS 'approval_pending';

CREATE TABLE transfer_approval_policies (
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    asset TEXT NOT NULL,
    threshold NUMERIC(30, 7) NOT NULL CHECK (threshold > 0),
    required_approvals SMALLINT NOT NULL CHECK (required_approvals BETWEEN 1 AND 10),
    expires_after INTERVAL NOT NULL DEFAULT INTERVAL '24 hours'
        CHECK (expires_after > INTERVAL '0' AND expires_after <= INTERVAL '30 days'),
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, asset)
);

CREATE TABLE transfer_approval_requests (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    transaction_id UUID NOT NULL UNIQUE REFERENCES transactions(id) ON DELETE CASCADE,
    creator_id UUID,
    asset TEXT NOT NULL,
    amount NUMERIC(30, 7) NOT NULL,
    required_approvals SMALLINT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected', 'expired')),
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at TIMESTAMPTZ
);

CREATE INDEX idx_transfer_approval_requests_tenant_status
    ON transfer_approval_requests (tenant_id, status, created_at DESC);
CREATE INDEX idx_transfer_approval_requests_expiry
    ON transfer_approval_requests (expires_at) WHERE status = 'pending';

CREATE TABLE transfer_approval_votes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id UUID NOT NULL REFERENCES transfer_approval_requests(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    actor_id UUID NOT NULL,
    decision TEXT NOT NULL CHECK (decision IN ('approved', 'rejected')),
    note TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (request_id, actor_id)
);

CREATE INDEX idx_transfer_approval_votes_request ON transfer_approval_votes(request_id, created_at);
