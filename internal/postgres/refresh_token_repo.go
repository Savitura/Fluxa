package postgres

import (
	"context"
	"crypto/sha256"
	"time"
)

type RefreshTokenRepo struct {
	db DB
}

func NewRefreshTokenRepo(db DB) *RefreshTokenRepo {
	return &RefreshTokenRepo{db: db}
}

// RevokeIfActive atomically marks a refresh token as revoked. The insert is
// deliberately idempotent so concurrent refresh requests cannot both rotate
// the same token successfully.
func (r *RefreshTokenRepo) RevokeIfActive(ctx context.Context, token string, expiresAt time.Time) (bool, error) {
	hash := sha256.Sum256([]byte(token))
	result, err := r.db.Exec(ctx,
		`INSERT INTO revoked_refresh_tokens (token_hash, expires_at)
		 VALUES ($1, $2)
		 ON CONFLICT (token_hash) DO NOTHING`,
		hash[:], expiresAt,
	)
	if err != nil {
		return false, err
	}
	return result.RowsAffected() == 1, nil
}
