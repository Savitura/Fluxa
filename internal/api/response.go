package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/fluxa/fluxa/internal/domain"
)

type errorResponse struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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

func NotFound(w http.ResponseWriter, message string) {
	Error(w, http.StatusNotFound, "NOT_FOUND", message)
}

func UnprocessableEntity(w http.ResponseWriter, code, message string) {
	Error(w, http.StatusUnprocessableEntity, code, message)
}

func InternalError(w http.ResponseWriter, err error) {
	Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
}

func HandleDomainError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrWalletNotFound), errors.Is(err, domain.ErrTransactionNotFound),
		errors.Is(err, domain.ErrWebhookNotFound), errors.Is(err, domain.ErrWebhookDeliveryNotFound),
		errors.Is(err, domain.ErrBatchNotFound), errors.Is(err, domain.ErrScheduleNotFound):
		NotFound(w, err.Error())
	case errors.Is(err, domain.ErrSelfTransfer), errors.Is(err, domain.ErrInvalidAsset),
		errors.Is(err, domain.ErrInsufficientBalance), errors.Is(err, domain.ErrSlippageExceeded),
		errors.Is(err, domain.ErrFeeScheduleNotFound),
		errors.Is(err, domain.ErrBatchTooLarge), errors.Is(err, domain.ErrBatchEmpty):
		BadRequest(w, err.Error())
	case errors.Is(err, domain.ErrQuoteExpired):
		UnprocessableEntity(w, "QUOTE_EXPIRED", err.Error())
	case errors.Is(err, domain.ErrQuoteAlreadyUsed):
		UnprocessableEntity(w, "QUOTE_ALREADY_USED", err.Error())
	default:
		InternalError(w, err)
	}
}
