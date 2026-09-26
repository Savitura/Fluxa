package claimable

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/stellar/go/xdr"
)

// BalanceID derives the claimable balance ID Stellar will assign to a
// CreateClaimableBalance operation, without waiting for Horizon to tell us.
//
// The ID is the SHA-256 of the operation's HashIDPreimage, wrapped in a
// ClaimableBalanceID of type V0 — the 36-byte hex string Horizon returns in its
// `id` field and the form txnbuild.ClaimClaimableBalance expects. Deriving it
// locally is what makes the local row and the on-chain balance reconcilable
// even if the create response is lost in flight; the alternative (reading it
// back from Horizon) is racy and Horizon does not expose the balance ID on the
// create operation resource at all.
//
// sourceAccount is the operation's source account (falling back to the
// transaction source account), seqNum the sequence number consumed by the
// transaction, and opIndex the operation's zero-based position.
func BalanceID(sourceAccount string, seqNum int64, opIndex uint32) (string, error) {
	accountID, err := xdr.AddressToAccountId(sourceAccount)
	if err != nil {
		return "", fmt.Errorf("parse balance id source account %q: %w", sourceAccount, err)
	}

	preimage := xdr.HashIdPreimage{
		Type: xdr.EnvelopeTypeEnvelopeTypeOpId,
		OperationId: &xdr.HashIdPreimageOperationId{
			SourceAccount: accountID,
			SeqNum:        xdr.SequenceNumber(seqNum),
			OpNum:         xdr.Uint32(opIndex),
		},
	}

	encoded, err := preimage.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode balance id preimage: %w", err)
	}

	hash := xdr.Hash(sha256.Sum256(encoded))
	id := xdr.ClaimableBalanceId{
		Type: xdr.ClaimableBalanceIdTypeClaimableBalanceIdTypeV0,
		V0:   &hash,
	}

	marshalled, err := id.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode claimable balance id: %w", err)
	}
	return hex.EncodeToString(marshalled), nil
}
