# Key Rotation Runbook & Procedure

## Overview

Fluxa encrypts sensitive custodial materials—including tenant custodial wallet secret seeds, webhook signing secrets, and idempotency cache bodies—using **AES-256-GCM authenticated envelope encryption** (defined in `internal/crypto`).

All ciphertexts are stored with an explicit version header:
```text
v<version>::<nonce_hex><ciphertext_hex>
```
Example:
```text
v1::8f92a1...49b2
v2::1a2b3c...88ee
```

This versioned envelope format enables **zero-downtime key rotation** and allows gradual in-place re-encryption of data without taking the platform offline.

---

## Keyring Configuration

The encryption system is driven by the following environment variables:

| Variable | Description | Default | Example |
|---|---|---|---|
| `MASTER_ENCRYPTION_KEY` | Hex-encoded 32-byte key for the active version. Used for all new encryptions. | (Required) | `openssl rand -hex 32` |
| `MASTER_ENCRYPTION_KEY_VERSION` | Integer identifier for the active key version. | `1` | `2` |
| `MASTER_ENCRYPTION_KEYS_ROTATED` | Comma-separated list of `<version>:<hexKey>` pairs for previous keys still needed to decrypt existing data. | `""` | `1:0001020304...` |

---

## Step-by-Step Rotation Procedure

### Step 1: Generate New Key Material

Generate a cryptographically secure 32-byte (64 hex characters) key for the new version (e.g. `v2`):

```bash
NEW_KEY=$(openssl rand -hex 32)
echo "Generated V2 Key: $NEW_KEY"
```

### Step 2: Configure Dual-Read / New-Write

Update your secrets manager / environment configuration with the new active key version while retaining the old key in `MASTER_ENCRYPTION_KEYS_ROTATED`:

```env
# New active key for writes and v2 reads
MASTER_ENCRYPTION_KEY=<NEW_KEY_HEX>
MASTER_ENCRYPTION_KEY_VERSION=2

# Retained keys for decrypting existing records
MASTER_ENCRYPTION_KEYS_ROTATED=1:<OLD_KEY_HEX>
```

Deploy the updated configuration across API and worker instances. At this stage:
- All **new** wallet seeds and secrets are encrypted with Version 2 (`v2::...`).
- All **existing** Version 1 records (`v1::...`) continue to be decrypted seamlessly using the retained `v1` key.

### Step 3: Re-encrypt Existing Records

Run the re-encryption utility or migration script across the target database tables:
1. `wallets` (`encrypted_seed`)
2. `webhook_signing_secrets` (`encrypted_secret`)
3. `idempotency_keys` (`response_body`)

For each row containing `v1::` prefix:
1. Decrypt using the keyring (`Keyring.Decrypt()`).
2. Re-encrypt using the active version (`Keyring.Encrypt()`), resulting in a `v2::` prefix.
3. Update the record atomically in a transaction.

Example query pattern:
```sql
-- Identify remaining v1 records
SELECT id FROM wallets WHERE encrypted_seed LIKE 'v1::%' LIMIT 100;
```

### Step 4: Verify Migration Completeness

Verify that no unmigrated records remain in the database:

```sql
SELECT count(*) FROM wallets WHERE encrypted_seed LIKE 'v1::%';
SELECT count(*) FROM webhook_signing_secrets WHERE encrypted_secret LIKE 'v1::%';
```

Both queries must return `0`.

### Step 5: Retire the Deprecated Key

Once verification confirms zero remaining references to `v1`:

1. Remove the retired key from `MASTER_ENCRYPTION_KEYS_ROTATED`:
   ```env
   MASTER_ENCRYPTION_KEYS_ROTATED=
   ```
2. Redeploy the services.
3. Archive or safely purge the retired key from your secret store according to organizational retention policies.
