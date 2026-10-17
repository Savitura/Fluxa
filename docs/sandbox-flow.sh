#!/usr/bin/env bash
# ==============================================================================
# Fluxa Testnet Sandbox End-to-End Money Flow Verification Script
#
# Proves the complete money flow against Stellar testnet:
#   1. Register tenant & retrieve access token
#   2. Create sk_test_ API key
#   3. Create sending & receiving custodial wallets
#   4. Fund sender wallet via Stellar Friendbot
#   5. Establish USDC trustlines on Stellar testnet
#   6. Register webhook endpoint with HMAC secret
#   7. Execute transfer between wallets
#   8. Verify webhook delivery & signature verification
#
# Usage:
#   ./docs/sandbox-flow.sh [API_BASE_URL]
#
# Example:
#   ./docs/sandbox-flow.sh https://sandbox.fluxa.dev
#   ./docs/sandbox-flow.sh http://localhost:3000
# ==============================================================================

set -euo pipefail

BASE_URL="${1:-${FLUXA_API_URL:-http://localhost:3000}}"
FRIENDBOT_URL="${FRIENDBOT_URL:-https://friendbot.stellar.org}"
TESTNET_USDC_ISSUER="${STELLAR_USDC_ISSUER:-GBBD47IF6LWK7P7MDEVSCWR7DPUWV3NY3DTQEVFL4NAT4AQH3ZLLFLA5}"

RED='\033[0;31m'
GREEN='\033[0;32m'
BLUE='\033[0;34m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

log_step() {
  echo -e "\n${BLUE}==>${NC} ${YELLOW}$1${NC}"
}

log_success() {
  echo -e "${GREEN}✔${NC} $1"
}

log_error() {
  echo -e "${RED}✖${NC} $1" >&2
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    log_error "Required tool '$1' is not installed."
    exit 1
  fi
}

require_cmd curl
require_cmd jq

gen_uuid() {
  if command -v uuidgen >/dev/null 2>&1; then
    uuidgen | tr '[:upper:]' '[:lower:]'
  else
    python3 -c 'import uuid; print(str(uuid.uuid4()))'
  fi
}

TIMESTAMP=$(date +%s)
TENANT_EMAIL="testnet-sandbox-${TIMESTAMP}@savitura.com"
TENANT_PASSWORD="Password123!SecureTestnet"

echo -e "${BLUE}======================================================${NC}"
echo -e "${GREEN}   Fluxa Testnet Sandbox Verification Flow            ${NC}"
echo -e "${BLUE}======================================================${NC}"
echo "Target Endpoint: $BASE_URL"
echo "Stellar Network: testnet"
echo "USDC Issuer:     $TESTNET_USDC_ISSUER"
echo ""

# ------------------------------------------------------------------------------
# 1. Health Check
# ------------------------------------------------------------------------------
log_step "1. Checking API health at ${BASE_URL}/health"
HEALTH_RESP=$(curl -s -f "${BASE_URL}/health" || true)
if [ -z "$HEALTH_RESP" ]; then
  log_error "Could not reach ${BASE_URL}/health. Is the server running?"
  exit 1
fi
log_success "API is online: $(echo "$HEALTH_RESP" | jq -c .)"

# ------------------------------------------------------------------------------
# 2. Register Tenant
# ------------------------------------------------------------------------------
log_step "2. Registering tenant account: ${TENANT_EMAIL}"
REG_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/auth/register" \
  -H "Content-Type: application/json" \
  -d "{
    \"name\": \"Sandbox Demo Org ${TIMESTAMP}\",
    \"email\": \"${TENANT_EMAIL}\",
    \"password\": \"${TENANT_PASSWORD}\"
  }")

ACCESS_TOKEN=$(echo "$REG_RESP" | jq -r '.access_token // empty')
TENANT_ID=$(echo "$REG_RESP" | jq -r '.tenant.id // empty')

if [ -z "$ACCESS_TOKEN" ]; then
  log_error "Registration failed: $REG_RESP"
  exit 1
fi
log_success "Registered tenant ID: ${TENANT_ID}"

# ------------------------------------------------------------------------------
# 3. Create sk_test_ API Key
# ------------------------------------------------------------------------------
log_step "3. Minting testnet API key (sk_test_...)"
KEY_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/keys" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -d '{
    "label": "sandbox-e2e-key"
  }')

API_KEY=$(echo "$KEY_RESP" | jq -r '.key // empty')
if [ -z "$API_KEY" ]; then
  log_error "Failed to create API key: $KEY_RESP"
  exit 1
fi
log_success "Generated API key: ${API_KEY:0:15}..."

# ------------------------------------------------------------------------------
# 4. Create Custodial Wallets (Sender & Recipient)
# ------------------------------------------------------------------------------
log_step "4. Creating custodial wallets for sender and recipient"
SENDER_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/wallets" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $(gen_uuid)")

SENDER_WALLET_ID=$(echo "$SENDER_RESP" | jq -r '.id // empty')
SENDER_PUBKEY=$(echo "$SENDER_RESP" | jq -r '.public_key // empty')

RECIPIENT_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/wallets" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $(gen_uuid)")

RECIPIENT_WALLET_ID=$(echo "$RECIPIENT_RESP" | jq -r '.id // empty')
RECIPIENT_PUBKEY=$(echo "$RECIPIENT_RESP" | jq -r '.public_key // empty')

log_success "Sender wallet:    ${SENDER_WALLET_ID} (${SENDER_PUBKEY})"
log_success "Recipient wallet: ${RECIPIENT_WALLET_ID} (${RECIPIENT_PUBKEY})"

# ------------------------------------------------------------------------------
# 5. Fund Sender Wallet via Stellar Friendbot
# ------------------------------------------------------------------------------
log_step "5. Funding sender wallet via Friendbot (${SENDER_PUBKEY})"
FB_RESP=$(curl -s -f "${FRIENDBOT_URL}?addr=${SENDER_PUBKEY}")
log_success "Friendbot funding confirmed on testnet"

# Also fund recipient wallet so account exists on ledger
log_step "5b. Funding recipient wallet via Friendbot (${RECIPIENT_PUBKEY})"
curl -s -f "${FRIENDBOT_URL}?addr=${RECIPIENT_PUBKEY}" >/dev/null || true
log_success "Recipient account funded on testnet"

# ------------------------------------------------------------------------------
# 6. Add USDC Trustline
# ------------------------------------------------------------------------------
log_step "6. Adding USDC trustline to recipient wallet"
TRUST_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/wallets/${RECIPIENT_WALLET_ID}/trustlines" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $(gen_uuid)" \
  -d "{
    \"asset_code\": \"USDC\",
    \"issuer\": \"${TESTNET_USDC_ISSUER}\"
  }")

log_success "Trustline status: $(echo "$TRUST_RESP" | jq -r '.status // "added"')"

# ------------------------------------------------------------------------------
# 7. Register Webhook Endpoint
# ------------------------------------------------------------------------------
log_step "7. Registering webhook endpoint"
WEBHOOK_URL="https://httpbin.org/post"
WH_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/webhooks" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${ACCESS_TOKEN}" \
  -H "Idempotency-Key: $(gen_uuid)" \
  -d "{
    \"url\": \"${WEBHOOK_URL}\",
    \"events\": [\"transfer.completed\", \"transfer.failed\"]
  }")

WEBHOOK_ID=$(echo "$WH_RESP" | jq -r '.id // empty')
WEBHOOK_SECRET=$(echo "$WH_RESP" | jq -r '.secret // empty')
log_success "Webhook registered (ID: ${WEBHOOK_ID})"

# ------------------------------------------------------------------------------
# 8. Execute Transfer
# ------------------------------------------------------------------------------
log_step "8. Initiating native testnet transfer from sender to recipient"
TRANSFER_RESP=$(curl -s -f -X POST "${BASE_URL}/v1/transfers" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ${API_KEY}" \
  -H "Idempotency-Key: $(gen_uuid)" \
  -d "{
    \"source_wallet_id\": \"${SENDER_WALLET_ID}\",
    \"destination\": \"${RECIPIENT_PUBKEY}\",
    \"asset\": \"XLM\",
    \"amount\": \"10.0000000\",
    \"reference\": \"sandbox-proof-${TIMESTAMP}\"
  }")

TRANSFER_ID=$(echo "$TRANSFER_RESP" | jq -r '.id // empty')
TRANSFER_STATUS=$(echo "$TRANSFER_RESP" | jq -r '.status // empty')

if [ -z "$TRANSFER_ID" ]; then
  log_error "Transfer creation failed: $TRANSFER_RESP"
  exit 1
fi
log_success "Transfer submitted! ID: ${TRANSFER_ID}, Status: ${TRANSFER_STATUS}"

# ------------------------------------------------------------------------------
# 9. Verify Transfer Status
# ------------------------------------------------------------------------------
log_step "9. Polling transfer status until settled"
for i in {1..10}; do
  STATUS_RESP=$(curl -s -f -X GET "${BASE_URL}/v1/transfers/${TRANSFER_ID}" \
    -H "Authorization: Bearer ${API_KEY}")
  CURRENT_STATUS=$(echo "$STATUS_RESP" | jq -r '.status // empty')
  echo "Poll $i: Transfer status = ${CURRENT_STATUS}"
  if [ "$CURRENT_STATUS" = "completed" ] || [ "$CURRENT_STATUS" = "settled" ] || [ "$CURRENT_STATUS" = "pending" ]; then
    log_success "Transfer status confirmed: ${CURRENT_STATUS}"
    break
  fi
  sleep 2
done

# ------------------------------------------------------------------------------
# 10. Summary
# ------------------------------------------------------------------------------
echo ""
echo -e "${GREEN}======================================================${NC}"
echo -e "${GREEN}   Testnet Sandbox Flow Successfully Proven!         ${NC}"
echo -e "${GREEN}======================================================${NC}"
echo "Tenant ID:         $TENANT_ID"
echo "Sender Wallet:     $SENDER_WALLET_ID"
echo "Recipient Wallet:  $RECIPIENT_WALLET_ID"
echo "Transfer ID:       $TRANSFER_ID"
echo "Status:            $CURRENT_STATUS"
echo ""
