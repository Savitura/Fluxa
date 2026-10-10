package fiat

import (
	"encoding/json"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
)

// AnchorHandler exposes the unified, anchor-agnostic fiat endpoints backed
// by AnchorFiatService: POST /deposit, POST /withdraw and
// GET /transactions/{id}.
type AnchorHandler struct {
	svc *AnchorFiatService
}

func NewAnchorHandler(svc *AnchorFiatService) *AnchorHandler {
	return &AnchorHandler{svc: svc}
}

func (h *AnchorHandler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/deposit", h.deposit)
		r.Post("/withdraw", h.withdraw)
		r.Get("/transactions/{id}", h.getTransaction)
	}
}

type anchorDepositReq struct {
	WalletID  string `json:"wallet_id" validate:"required"`
	AssetCode string `json:"asset_code" validate:"required"`
	Amount    string `json:"amount"`
	Email     string `json:"email"`
}

type anchorTransferResp struct {
	Type           string                        `json:"type"`
	Instructions   *anchorDepositInstructionsDTO `json:"instructions,omitempty"`
	Withdrawal     *anchorWithdrawalDTO          `json:"withdrawal,omitempty"`
	InteractiveURL string                        `json:"interactive_url,omitempty"`
	TransactionID  string                        `json:"transaction_id"`
}

type anchorDepositInstructionsDTO struct {
	ID           string                 `json:"id"`
	How          string                 `json:"how,omitempty"`
	ETA          int                    `json:"eta,omitempty"`
	Instructions map[string]interface{} `json:"instructions,omitempty"`
	ExtraInfo    map[string]interface{} `json:"extra_info,omitempty"`
}

type anchorWithdrawalDTO struct {
	ID        string `json:"id"`
	AccountID string `json:"account_id,omitempty"`
	MemoType  string `json:"memo_type,omitempty"`
	Memo      string `json:"memo,omitempty"`
}

func toAnchorTransferResp(res *AnchorTransferResult) anchorTransferResp {
	out := anchorTransferResp{
		Type:           res.Type,
		InteractiveURL: res.InteractiveURL,
		TransactionID:  res.TransactionID,
	}
	if res.Instructions != nil {
		fields := make(map[string]interface{}, len(res.Instructions.Instructions))
		for k, v := range res.Instructions.Instructions {
			fields[k] = v
		}
		out.Instructions = &anchorDepositInstructionsDTO{
			ID:           res.Instructions.ID,
			How:          res.Instructions.How,
			ETA:          res.Instructions.ETA,
			Instructions: fields,
			ExtraInfo:    res.Instructions.ExtraInfo,
		}
	}
	if res.Withdrawal != nil {
		out.Withdrawal = &anchorWithdrawalDTO{
			ID:        res.Withdrawal.ID,
			AccountID: res.Withdrawal.AccountID,
			MemoType:  res.Withdrawal.MemoType,
			Memo:      res.Withdrawal.Memo,
		}
	}
	return out
}

func (h *AnchorHandler) deposit(w http.ResponseWriter, r *http.Request) {
	var req anchorDepositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	res, err := h.svc.InitiateDeposit(r.Context(), AnchorDepositRequest(req))
	if err != nil {
		log.Error().Err(err).Msg("anchor deposit failed")
		api.Error(w, http.StatusBadGateway, "ANCHOR_DEPOSIT_FAILED", "anchor deposit failed")
		return
	}

	api.JSON(w, http.StatusOK, toAnchorTransferResp(res))
}

type anchorWithdrawReq struct {
	WalletID  string `json:"wallet_id" validate:"required"`
	AssetCode string `json:"asset_code" validate:"required"`
	Amount    string `json:"amount"`
	Dest      string `json:"dest"`
}

func (h *AnchorHandler) withdraw(w http.ResponseWriter, r *http.Request) {
	var req anchorWithdrawReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	res, err := h.svc.InitiateWithdrawal(r.Context(), AnchorWithdrawRequest(req))
	if err != nil {
		log.Error().Err(err).Msg("anchor withdrawal failed")
		api.Error(w, http.StatusBadGateway, "ANCHOR_WITHDRAWAL_FAILED", "anchor withdrawal failed")
		return
	}

	api.JSON(w, http.StatusOK, toAnchorTransferResp(res))
}

type anchorTransactionResp struct {
	ID           string  `json:"id"`
	WalletID     string  `json:"wallet_id"`
	AnchorID     string  `json:"anchor_id"`
	ExternalTxID string  `json:"external_transaction_id"`
	Asset        string  `json:"asset"`
	Amount       string  `json:"amount"`
	Type         string  `json:"type"`
	Status       string  `json:"status"`
	CreatedAt    string  `json:"created_at"`
	CompletedAt  *string `json:"completed_at,omitempty"`
}

func toAnchorTransactionResp(t *domain.AnchorTransaction) anchorTransactionResp {
	out := anchorTransactionResp{
		ID: t.ID, WalletID: t.WalletID, AnchorID: t.AnchorID, ExternalTxID: t.ExternalTxID,
		Asset: t.Asset, Amount: t.Amount, Type: t.Type, Status: t.Status,
		CreatedAt: t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if t.CompletedAt != nil {
		s := t.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
		out.CompletedAt = &s
	}
	return out
}

func (h *AnchorHandler) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if id == "" {
		api.BadRequest(w, "transaction id is required")
		return
	}

	t, err := h.svc.GetTransaction(r.Context(), id)
	if err != nil {
		api.Error(w, http.StatusNotFound, "ANCHOR_TRANSACTION_NOT_FOUND", err.Error())
		return
	}

	api.JSON(w, http.StatusOK, toAnchorTransactionResp(t))
}
