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

1. **Encryption keys**: Generate `MASTER_ENCRYPTION_KEY` using `openssl rand -hex 32`. Never reuse keys across environments.
2. **Database**: Use SSL/TLS connections (`sslmode=require` or `sslmode=verify-full`).
3. **Redis**: Enable authentication and use TLS in production.
4. **API keys**: Store `sk_live_*` keys securely; they grant full access to tenant resources.
5. **Webhook secrets**: Verify signatures on every delivery. Reject requests older than 5 minutes.
6. **Network**: Run the API behind a reverse proxy with TLS termination. Never expose PostgreSQL or Redis to the public internet.
7. **Compliance**: Enable `COMPLIANCE_ENABLED=true` in production to screen transfers against OFAC sanctions lists.

## Acknowledgments

We thank the security researchers who help keep Fluxa and its users safe.
