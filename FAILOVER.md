# Fluxa Multi-Region Failover Runbook

This runbook promotes the secondary region to the active region after the primary is confirmed unavailable. The deployment is **active-passive**: the primary handles writes and runs the worker, while the secondary serves API traffic from a read replica and keeps its worker disabled.

## 1. Verify the incident

Confirm that the primary is unreachable rather than merely slow or isolated from the operator network. Check the primary API, database, Redis Sentinel quorum, and the secondary API endpoints:

```sh
curl -fsS https://primary.example.com/health
curl -fsS https://secondary.example.com/health
curl -fsS https://secondary.example.com/health/ready
curl -fsS https://secondary.example.com/health/live
```

`/health/live` is process-only and should remain `200` while dependencies are unavailable. `/health/ready` must be `503` when the primary database or Redis dependency for that instance is unavailable.

## 2. Promote PostgreSQL

Use the managed PostgreSQL provider’s promotion procedure or set `PROMOTE_REPLICA_CMD` to the provider-specific command. Verify that the promoted database accepts writes, then update the secondary environment so `DATABASE_URL` points at the promoted primary. `REPLICA_DATABASE_URL` may remain set to a separate standby when one is available.

## 3. Promote the application region

Run the failover target from the deployment operator’s workstation:

```sh
PROMOTE_REPLICA_CMD='your-provider promote command' \
UPDATE_DNS_CMD='your-dns-provider update command' \
PRIMARY_ENV=.env.primary \
SECONDARY_ENV=.env.secondary \
make failover
```

The target starts the secondary worker only after the database promotion command succeeds. Redis clients use Sentinel when `REDIS_SENTINEL_MASTER_NAME` and `REDIS_SENTINEL_ADDRS` are configured, so Asynq and application Redis connections rediscover the promoted Redis master automatically.

## 4. Update DNS and verify traffic

Update the API hostname to the secondary region and allow DNS propagation. Validate the following before declaring the failover complete:

```sh
curl -fsS https://api.example.com/health
curl -fsS https://api.example.com/health/ready
curl -fsS https://api.example.com/health/live
```

The component report should show healthy `postgres`, `redis`, `horizon`, and `worker` probes. A temporarily unavailable read replica is acceptable when the primary is healthy: reads fall back to the primary and `/health` reports `degraded` rather than making the API unavailable.

## 5. Recovery

Do not re-enable the former primary worker until its database is rebuilt or re-seeded from the current primary and replication is confirmed. After the former primary is healthy, configure it as the new read replica, verify `REPLICA_DATABASE_URL`, and leave only one worker-enabled region active.

## Deployment variables

| Variable | Primary region | Secondary region |
|---|---:|---:|
| `WORKER_ENABLED` | `true` | `false` until failover |
| `DATABASE_URL` | writable primary | promoted primary after failover |
| `REPLICA_DATABASE_URL` | optional standby | current standby or empty |
| `REDIS_SENTINEL_MASTER_NAME` | required for Sentinel | required for Sentinel |
| `REDIS_SENTINEL_ADDRS` | comma-separated Sentinel addresses | same Sentinel quorum |

The runbook is intentionally provider-neutral. The promotion and DNS commands must be supplied by the operator because they depend on the selected PostgreSQL, Redis, and DNS providers.
