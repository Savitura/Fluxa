# Security Policy

Fluxa handles financial transactions and cryptographic keys. Security is critical.

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

To report a security issue, please email **security@savitura.com** with:

1. A description of the vulnerability
2. Steps to reproduce
3. Potential impact
4. Any suggested fixes (optional)

### What to expect

- **Acknowledgment**: We will acknowledge receipt within 48 hours.
- **Assessment**: We will assess the report and respond with our evaluation within 7 days.
- **Resolution**: Critical issues will be patched as soon as possible. We will coordinate disclosure timing with you.
- **Credit**: We will credit reporters in the release notes (unless you prefer to remain anonymous).

## Scope

The following are in scope:

- API authentication bypass
- Unauthorized access to wallets or funds
- Cryptographic key exposure
- SQL injection, XSS, or other injection attacks
- Stellar transaction manipulation
- Webhook signature bypass
- Compliance screening bypass

The following are out of scope:

- Denial of service attacks
- Social engineering
- Issues in dependencies (report upstream)
- Issues requiring physical access

## Security Best Practices for Operators

When deploying Fluxa:

1. **Encryption keys**: Generate `MASTER_ENCRYPTION_KEY` using `openssl rand -hex 32`. Never reuse keys across environments. Follow [Key Rotation Runbook](docs/key-rotation.md) when rotating keys.
2. **Database**: Use SSL/TLS connections (`sslmode=require` or `sslmode=verify-full`).
3. **Redis**: Enable authentication and use TLS in production.
4. **API keys**: Store `sk_live_*` keys securely; they grant full access to tenant resources.
5. **Webhook secrets**: Verify signatures on every delivery. Reject requests older than 5 minutes.
6. **Network**: Run the API behind a reverse proxy with TLS termination. Never expose PostgreSQL or Redis to the public internet.
7. **Compliance**: Enable `COMPLIANCE_ENABLED=true` in production to screen transfers against OFAC sanctions lists.

## Custodial Secret Envelope Encryption & Key Rotation

Tenant custodial wallet secret seeds, webhook signing secrets, and cached idempotency payloads are encrypted with **AES-256-GCM envelope encryption** using a versioned header format (`v<version>::<nonce><ciphertext>`).

- **Multi-Version Keyring**: The application loads the active key (`MASTER_ENCRYPTION_KEY_VERSION` and `MASTER_ENCRYPTION_KEY`) as well as retired keys for existing data (`MASTER_ENCRYPTION_KEYS_ROTATED`).
- **Zero-Downtime Rotation**: New records are encrypted with the active version, while older records remain decryptable.
- **Rotation Procedure**: See [docs/key-rotation.md](docs/key-rotation.md) for the step-by-step key migration and retirement runbook.

## Treasury Signing Key: Risk Analysis & Remote KMS Migration Plan

### Risk Analysis

Currently, `TREASURY_SECRET_KEY` is loaded from an environment variable as a raw Stellar secret seed. This architecture entails specific operational and cryptographic risks:

1. **Host & Environment Exposure**: Any process inspection, accidental environment logging, container dump, or crash log could compromise the treasury private key.
2. **Lack of Hardware Protection**: Private key bytes are held in application memory rather than inside a Hardware Security Module (HSM) or tamper-resistant enclave.
3. **Absence of Hardware Audit Trails**: Signatures occur in-process, meaning signing events cannot be independently verified or audited at the hardware layer.
4. **Catastrophic Blast Radius**: If the single treasury seed leaks, all treasury reserves are immediately vulnerable.

### Current Mitigations

- **Bounded Reserve Float**: The treasury reserve cache and worker enforce balance caps (`TREASURY_BASE_RESERVE`), sweeping excess funds to `COLD_STORAGE_ADDRESS`.
- **Environment Isolation**: Production deployments isolate environment injection via orchestration secret managers (e.g. AWS Secrets Manager, GCP Secret Manager).
- **Entropy & Address Verification**: Server startup rejects weak or well-known seeds and verifies address checksums.

### Architecture & Migration Plan

To eliminate plain environment variable risk, Fluxa is executing a three-phase transition:

1. **Phase 1: Abstracted Signer Interface (`stellar.Signer`)**
   - The codebase has isolated transaction signing behind the `Signer` interface (`internal/stellar/signer.go`).
2. **Phase 2: Remote KMS / Vault Provider Integration**
   - Implement `KeyManagementService` and `Signer` wrappers for cloud hardware security modules:
     - **AWS KMS**: Asymmetric signing key using `ECC_ED25519` key spec. Private keys never leave AWS HSMs.
     - **GCP Cloud KMS**: Asymmetric sign using Cloud KMS ED25519 crypto keys.
     - **HashiCorp Vault**: Transit Secrets Engine providing `ed25519` signing via authenticated RPC.
3. **Phase 3: On-Chain Soroban Multisig & Time-Locks**
   - Migrate treasury custody to on-chain Soroban contract wallets (Issue #315 / #419), requiring multi-sig (four-eyes principle) and spending limits for treasury disbursements, eliminating single-seed dependency entirely.

## Acknowledgments

We thank the security researchers who help keep Fluxa and its users safe.

