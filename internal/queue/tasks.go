package queue

const (
	TypeProcessTransfer = "transfer:process"
	TypeConfirmTx       = "transfer:confirm"
	TypeSyncLedger      = "indexer:sync"
)

type ProcessTransferPayload struct {
	TransactionID string `json:"transaction_id"`
}

type ConfirmTxPayload struct {
	TransactionID string `json:"transaction_id"`
	TxHash        string `json:"tx_hash"`
}

type SyncLedgerPayload struct {
	WalletID string `json:"wallet_id"`
	Cursor   string `json:"cursor"`
}
