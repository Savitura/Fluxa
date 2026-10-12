DROP TABLE IF EXISTS transfer_approval_votes;
DROP TABLE IF EXISTS transfer_approval_requests;
DROP TABLE IF EXISTS transfer_approval_policies;
-- PostgreSQL does not support removing an enum value; approval_pending remains unused after rollback.
