package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/rs/zerolog/log"
)

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ValidationErrorDetail describes a single invalid field in a request.
type ValidationErrorDetail struct {
	Row    int    `json:"row"`
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// validationErrorResponse includes per-row errors alongside the top-level error.
type validationErrorResponse struct {
	Error            errorDetail          `json:"error"`
	ValidationErrors []ValidationErrorDetail `json:"validation_errors"`
}

func JSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, errorResponse{
		Error: errorDetail{Code: code, Message: message},
	})
}

func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, "BAD_REQUEST", message)
}

// BadRequestWithValidationErrors returns a 400 with per-row error details.
func BadRequestWithValidationErrors(w http.ResponseWriter, message string, errs []ValidationErrorDetail) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(validationErrorResponse{
		Error:            errorDetail{Code: "BAD_REQUEST", Message: message},
		ValidationErrors: errs,
	})
}

func NotFound(w http.ResponseWriter, message string) {
	Error(w, http.StatusNotFound, "NOT_FOUND", message)
}

func UnprocessableEntity(w http.ResponseWriter, code, message string) {
	Error(w, http.StatusUnprocessableEntity, code, message)
}

func InternalError(w http.ResponseWriter, err error) {
	// The client deliberately gets an opaque message, but the cause has to go
	// somewhere — without this an unexpected failure leaves no trace at all.
	log.Error().Err(err).Msg("api: unhandled internal error")
	Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
}

func HandleDomainError(w http.ResponseWriter, err error) {
	var noTrustlineErr *domain.ErrNoTrustline
	if errors.As(err, &noTrustlineErr) {
		UnprocessableEntity(w, "MISSING_TRUSTLINE", err.Error())
		return
	}

	switch {
	case errors.Is(err, domain.ErrWalletNotFound), errors.Is(err, domain.ErrTransactionNotFound),

		errors.Is(err, domain.ErrWebhookNotFound), errors.Is(err, domain.ErrWebhookDeliveryNotFound),
		errors.Is(err, domain.ErrBatchNotFound), errors.Is(err, domain.ErrScheduleNotFound),
		errors.Is(err, domain.ErrUserNotFound), errors.Is(err, domain.ErrOrgMemberNotFound),
		errors.Is(err, domain.ErrInviteNotFound):
		NotFound(w, err.Error())
	case errors.Is(err, domain.ErrSelfTransfer), errors.Is(err, domain.ErrInvalidAsset),
		errors.Is(err, domain.ErrInsufficientBalance), errors.Is(err, domain.ErrSlippageExceeded),
		errors.Is(err, domain.ErrFeeScheduleNotFound), errors.Is(err, domain.ErrBatchTooLarge),
		errors.Is(err, domain.ErrBatchEmpty), errors.Is(err, domain.ErrWalletLimitReached),
		errors.Is(err, domain.ErrTransferLimitReached), errors.Is(err, domain.ErrWebhookLimitReached),
		errors.Is(err, domain.ErrInvalidQuoteAmount):
		BadRequest(w, err.Error())
	case errors.Is(err, domain.ErrUserAlreadyExists):
		Error(w, http.StatusConflict, "CONFLICT", err.Error())
	case errors.Is(err, domain.ErrInvalidCredentials):
		Error(w, http.StatusUnauthorized, "UNAUTHORIZED", err.Error())
	case errors.Is(err, domain.ErrForbidden), errors.Is(err, domain.ErrQuoteOwnershipMismatch):
		Error(w, http.StatusForbidden, "FORBIDDEN", err.Error())
	case errors.Is(err, domain.ErrQuoteExpired):
		UnprocessableEntity(w, "QUOTE_EXPIRED", err.Error())
	case errors.Is(err, domain.ErrQuoteAlreadyUsed):
		UnprocessableEntity(w, "QUOTE_ALREADY_USED", err.Error())
	case errors.Is(err, domain.ErrInsufficientSweepableBalance):
		Error(w, http.StatusBadRequest, "INSUFFICIENT_SWEEPABLE_BALANCE", err.Error())
	case errors.Is(err, domain.ErrTreasuryConfigNotFound):
		NotFound(w, err.Error())
	case errors.Is(err, domain.ErrComplianceReviewNotFound):
		NotFound(w, err.Error())
	case errors.Is(err, domain.ErrReviewNotPending):
		Error(w, http.StatusConflict, "REVIEW_ALREADY_DECIDED", err.Error())
	// Sanctions blocks get their own 403 code rather than the generic
	// FORBIDDEN above: callers must be able to tell "you may not do this"
	// from "this counterparty is sanctioned", and the latter is terminal —
	// retrying with the same destination will always fail.
	case errors.Is(err, domain.ErrTransferBlockedSanctions):
		Error(w, http.StatusForbidden, "TRANSFER_BLOCKED_SANCTIONS", err.Error())
	default:
		InternalError(w, err)
	}
}
