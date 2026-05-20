package stellar

import (
	"fmt"

	"github.com/fluxa/fluxa/internal/crypto"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

// Signer signs a Stellar transaction. The abstraction boundary that separates
// key management from transaction building — replace EnvSigner with an HSM or
// KMS-backed implementation without touching the settlement engine.
type Signer interface {
	Sign(tx *txnbuild.Transaction, encryptedSecret string) (*txnbuild.Transaction, error)
}

// EnvSigner decrypts the wallet secret from AES-256-GCM storage and signs in-process.
type EnvSigner struct {
	masterKey       []byte
	networkPassphrase string
}

func NewEnvSigner(masterKey []byte, stellarNetwork string) *EnvSigner {
	passphrase := network.TestNetworkPassphrase
	if stellarNetwork == "mainnet" || stellarNetwork == "public" {
		passphrase = network.PublicNetworkPassphrase
	}
	return &EnvSigner{masterKey: masterKey, networkPassphrase: passphrase}
}

func (s *EnvSigner) Sign(tx *txnbuild.Transaction, encryptedSecret string) (*txnbuild.Transaction, error) {
	secretBytes, err := crypto.Decrypt([]byte(encryptedSecret), s.masterKey)
	if err != nil {
		return nil, fmt.Errorf("decrypt secret: %w", err)
	}

	kp, err := keypair.ParseFull(string(secretBytes))
	if err != nil {
		return nil, fmt.Errorf("parse keypair: %w", err)
	}

	signed, err := tx.Sign(s.networkPassphrase, kp)
	if err != nil {
		return nil, fmt.Errorf("sign transaction: %w", err)
	}

	return signed, nil
}
