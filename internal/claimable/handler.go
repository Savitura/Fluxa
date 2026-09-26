package claimable

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc   Service
	guard func(http.Handler) http.Handler
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// WithMutationGate attaches middleware to the state-mutating routes only
// (POST / and POST /{id}/claim). Creating a balance moves real funds out of
// the org's wallet and claiming moves them back in, so both get the same
// Owner/Admin-only gating already applied to /v1/keys and /v1/org, unlike the
// read routes here.
func (h *Handler) WithMutationGate(mw func(http.Handler) http.Handler) *Handler {
	h.guard = mw
	return h
}

// Routes is mounted at /v1/claimable-balances.
func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.list)
		r.Get("/{id}", h.get)

		post := r.Post
		if h.guard != nil {
			post = r.With(h.guard).Post
		}
		post("/", h.create)
		post("/{id}/claim", h.claim)
	}
}

type claimantRequest struct {
	Account   string            `json:"account"   validate:"required"`
	Predicate *predicateRequest `json:"predicate"`
}

type predicateRequest struct {
	Type       string             `json:"type"       validate:"required"`
	Timestamp  int64              `json:"timestamp"`
	Seconds    int64              `json:"seconds"`
	Predicates []predicateRequest `json:"predicates"`
}

type createRequest struct {
	Asset          string            `json:"asset"            validate:"required"`
	Amount         string            `json:"amount"           validate:"required"`
	Claimants      []claimantRequest `json:"claimants"        validate:"required,min=1,dive"`
	SourceWalletID string            `json:"source_wallet_id" validate:"omitempty,uuid"`
	SponsorAccount string            `json:"sponsor_account"`
	RevokeOnExpiry bool              `json:"revoke_on_expiry"`
	ExpiresAt      string            `json:"expires_at"`
}

type claimantResponse struct {
	Account   string                 `json:"account"`
	Predicate *domain.ClaimPredicate `json:"predicate,omitempty"`
}

type balanceResponse struct {
	ID             string             `json:"id"`
	Asset          string             `json:"asset"`
	Amount         string             `json:"amount"`
	Claimants      []claimantResponse `json:"claimants"`
	Sponsor        string             `json:"sponsor,omitempty"`
	Status         string             `json:"status"`
	RevokeOnExpiry bool               `json:"revoke_on_expiry"`
	CreatedAt      string             `json:"created_at"`
	ExpiresAt      string             `json:"expires_at,omitempty"`
	ClaimedAt      string             `json:"claimed_at,omitempty"`
	ClaimedBy      string             `json:"claimed_by,omitempty"`
}

// liveBalanceResponse decorates a stored balance with what Horizon reports and
// whether a claimant could claim right now.
type liveBalanceResponse struct {
	balanceResponse
	Claimable bool                   `json:"claimable"`
	OnChain   map[string]interface{} `json:"on_chain,omitempty"`
}

func toBalanceResponse(b *domain.ClaimableBalance) balanceResponse {
	claimants := make([]claimantResponse, len(b.Claimants))
	for i, c := range b.Claimants {
		claimants[i] = claimantResponse{Account: c.Account, Predicate: c.Predicate}
	}

	resp := balanceResponse{
		ID:             b.ID,
		Asset:          b.Asset,
		Amount:         b.Amount.StringFixed(7),
		Claimants:      claimants,
		Sponsor:        b.Sponsor,
		Status:         string(b.Status),
		RevokeOnExpiry: b.RevokeOnExpiry,
		CreatedAt:      b.CreatedAt.Format(time.RFC3339),
		ClaimedBy:      b.ClaimedBy,
	}
	if b.ExpiresAt != nil {
		resp.ExpiresAt = b.ExpiresAt.Format(time.RFC3339)
	}
	if b.ClaimedAt != nil {
		resp.ClaimedAt = b.ClaimedAt.Format(time.RFC3339)
	}
	return resp
}

func predicateFromRequest(p *predicateRequest) *domain.ClaimPredicate {
	if p == nil {
		return nil
	}
	out := &domain.ClaimPredicate{
		Type:      domain.PredicateType(p.Type),
		Timestamp: p.Timestamp,
		Seconds:   p.Seconds,
	}
	for i := range p.Predicates {
		out.Predicates = append(out.Predicates, *predicateFromRequest(&p.Predicates[i]))
	}
	return out
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.GreaterThan(decimal.Zero) {
		api.BadRequest(w, domain.ErrInvalidAmount.Error())
		return
	}

	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			api.BadRequest(w, "expires_at must be an RFC3339 timestamp")
			return
		}
		expiresAt = &parsed
	}

	claimants := make([]domain.Claimant, 0, len(req.Claimants))
	for _, c := range req.Claimants {
		claimants = append(claimants, domain.Claimant{
			Account:   c.Account,
			Predicate: predicateFromRequest(c.Predicate),
		})
	}

	result, err := h.svc.Create(r.Context(), CreateInput{
		Asset:          req.Asset,
		Amount:         amount,
		Claimants:      claimants,
		SourceWalletID: req.SourceWalletID,
		SponsorAccount: req.SponsorAccount,
		RevokeOnExpiry: req.RevokeOnExpiry,
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := map[string]interface{}{
		"balance_id":       result.BalanceID,
		"reserve_required": result.ReserveRequired.StringFixed(7),
		"sponsored":        result.Sponsored,
		"tx_hash":          result.TxHash,
	}
	if result.ExpiresAt != nil {
		resp["expires_at"] = result.ExpiresAt.Format(time.RFC3339)
	}
	api.JSON(w, http.StatusCreated, resp)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	filter := Filter{
		Asset:    query.Get("asset"),
		Claimant: query.Get("claimant"),
		Status:   domain.ClaimableBalanceStatus(query.Get("status")),
	}
	if raw := query.Get("limit"); raw != "" {
		filter.Limit, _ = strconv.Atoi(raw)
	}
	if raw := query.Get("offset"); raw != "" {
		filter.Offset, _ = strconv.Atoi(raw)
	}
	if raw := query.Get("expires_before"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			api.BadRequest(w, "expires_before must be an RFC3339 timestamp")
			return
		}
		filter.ExpiresBefore = &parsed
	}

	balances, err := h.svc.List(r.Context(), filter)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	responses := make([]balanceResponse, len(balances))
	for i, b := range balances {
		responses[i] = toBalanceResponse(b)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"claimable_balances": responses})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	live, err := h.svc.Get(r.Context(), id)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := liveBalanceResponse{
		balanceResponse: toBalanceResponse(live.Balance),
		Claimable:       live.Claimable,
	}
	if live.OnChain != nil {
		resp.OnChain = map[string]interface{}{
			"id":                   live.OnChain.BalanceID,
			"asset":                live.OnChain.Asset,
			"amount":               live.OnChain.Amount,
			"sponsor":              live.OnChain.Sponsor,
			"claimants":            live.OnChain.Claimants,
			"last_modified_ledger": live.OnChain.LastModifiedLedger,
		}
	}
	api.JSON(w, http.StatusOK, resp)
}

type claimRequest struct {
	ClaimantAccount string `json:"claimant_account"`
}

func (h *Handler) claim(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req claimRequest
	// An empty body is valid: when the balance has exactly one custodied
	// claimant, the claim needs no arguments at all.
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}

	result, err := h.svc.Claim(r.Context(), id, req.ClaimantAccount)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"balance_id": result.BalanceID,
		"claimed_by": result.ClaimedBy,
		"amount":     result.Amount.StringFixed(7),
		"tx_hash":    result.TxHash,
		"status":     string(domain.ClaimableBalanceStatusClaimed),
	})
}
