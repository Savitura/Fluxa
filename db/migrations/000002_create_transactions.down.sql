DROP INDEX IF EXISTS idx_transactions_created_at;
DROP INDEX IF EXISTS idx_transactions_status;
DROP INDEX IF EXISTS idx_transactions_to_wallet;
DROP INDEX IF EXISTS idx_transactions_from_wallet;
DROP TABLE IF EXISTS transactions;
DROP TYPE IF EXISTS transaction_type;
DROP TYPE IF EXISTS transaction_status;
