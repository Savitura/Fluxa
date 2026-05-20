package stellar

import (
	"fmt"

	"github.com/stellar/go/keypair"
)

// GenerateKeypair creates a new random Stellar keypair.
// Returns the public key (address) and secret key (seed).
func GenerateKeypair() (publicKey, secretKey string, err error) {
	kp, err := keypair.Random()
	if err != nil {
		return "", "", fmt.Errorf("generate stellar keypair: %w", err)
	}
	return kp.Address(), kp.Seed(), nil
}
