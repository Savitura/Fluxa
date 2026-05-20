package domain

import "time"

type Wallet struct {
	ID              string
	PublicKey       string
	EncryptedSecret string
	CreatedAt       time.Time
}
