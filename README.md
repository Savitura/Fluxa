# Fluxa
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT) [![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](http://makeapullrequest.com)


**Cross-border payment infrastructure for emerging markets.**

Fluxa is a programmable payments API built on the [Stellar](https://stellar.org) network. It gives fintech products and developers the primitives to move value across borders — wallet management, internal transfers, FX conversion via Stellar path payments, and settlement — behind a clean REST API.

[![Run in Postman](https://img.shields.io/badge/Run%20in%20Postman-FF6C37?style=for-the-badge&logo=postman&logoColor=white)](docs/fluxa.postman_collection.json)
[![Environment](https://img.shields.io/badge/Environment-FF6C37?style=for-the-badge&logo=postman&logoColor=white)](docs/fluxa.postman_environment.json)
[![Quickstart](https://img.shields.io/badge/Quickstart-36C5F0?style=for-the-badge&logo=readthedocs&logoColor=white)](docs/quickstart.md)
[![Errors](https://img.shields.io/badge/Error%20Reference-CD5C5C?style=for-the-badge&logo=readthedocs&logoColor=white)](docs/errors.md)

> **Status**: Active development — testnet only.

---

## Webhook Signature Verification

Every outbound webhook delivery includes headers:
- `X-Fluxa-Signature`: `sha256=<hex HMAC-SHA256 signature>`
- `X-Fluxa-Timestamp`: Unix epoch seconds at delivery time

### Verification in Go
```go
import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "strconv"
    "time"
)

func Verify(secret, timestamp, body, signature string) bool {
    ts, err := strconv.ParseInt(timestamp, 10, 64)
    if err != nil || abs(time.Now().Unix() - ts) >= 300 {
        return false
    }
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write([]byte(timestamp + "." + body))
    expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(expected), []byte(signature))
}
```

### Verification in TypeScript
```typescript
import { createHmac, timingSafeEqual } from "node:crypto";

export function verifyWebhook(secret: string, timestamp: string, body: string, signature: string): boolean {
    if (Math.abs(Math.floor(Date.now() / 1000) - Number(timestamp)) >= 300) return false;
    const expected = "sha256=" + createHmac("sha256", secret).update(`${timestamp}.${body}`).digest("hex");
    const p = Buffer.from(signature);
    const e = Buffer.from(expected);
    return p.length === e.length && timingSafeEqual(p, e);
}
```

### Verification via curl
```bash
curl -X POST http://localhost:3000/v1/webhooks/verify \
  -H "Content-Type: application/json" \
  -d '{"secret":"whsec_...","timestamp":"1700000000","body":"{}","signature":"sha256=..."}'
```
