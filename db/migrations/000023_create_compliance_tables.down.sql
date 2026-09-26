DROP INDEX IF EXISTS idx_transactions_from_wallet_created;
DROP INDEX IF EXISTS idx_transactions_to_wallet_created;
DROP INDEX IF EXISTS idx_transactions_tenant_created;

DROP TABLE IF EXISTS sanctions_entities;
DROP TABLE IF EXISTS sanctions_list_updates;
DROP TABLE IF EXISTS compliance_blocks;
DROP TABLE IF EXISTS compliance_reviews;
