package batch

import (
	"strings"

	"github.com/fluxa/fluxa/internal/domain"
)

func toCSV(txs []*domain.Transaction) string {
	var sb strings.Builder
	sb.WriteString("to_wallet,asset,amount,reference,status,tx_hash\n")
	for _, tx := range txs {
		sb.WriteString(csvField(tx.ToWallet))
		sb.WriteByte(',')
		sb.WriteString(csvField(tx.Asset))
		sb.WriteByte(',')
		sb.WriteString(csvField(tx.Amount.StringFixed(7)))
		sb.WriteByte(',')
		sb.WriteString(csvField(tx.Reference))
		sb.WriteByte(',')
		sb.WriteString(csvField(string(tx.Status)))
		sb.WriteByte(',')
		sb.WriteString(csvField(tx.TxHash))
		sb.WriteByte('\n')
	}
	return sb.String()
}

func csvField(v string) string {
	if strings.ContainsAny(v, ",\"\n") {
		return `"` + strings.ReplaceAll(v, `"`, `""`) + `"`
	}
	return v
}
