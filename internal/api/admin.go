package api

import (
	"database/sql"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
	"github.com/mstefanko/cartledger/internal/llm"
)

// AdminHandler serves household-scoped administrative endpoints that are not
// part of the regular CRUD surface. Currently just the LLM usage/budget view;
// future additions (e.g. per-household purge, debug dumps) belong here.
type AdminHandler struct {
	DB    *sql.DB
	Cfg   *config.Config
	Guard *llm.GuardedExtractor // holds breaker + budget config
}

// RegisterRoutes mounts admin endpoints onto the protected (JWT-required) group.
// Every route is household-scoped via the JWT claim — there is no cross-household
// admin view by design.
func (h *AdminHandler) RegisterRoutes(protected *echo.Group) {
	protected.GET("/admin/usage", h.Usage)
}

// usageResponse mirrors the JSON contract documented in task 2.4.
type usageResponse struct {
	HouseholdID  string `json:"household_id"`
	Month        string `json:"month"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
	Budget       int64  `json:"budget"`
	Remaining    int64  `json:"remaining"`
	CircuitOpen  bool   `json:"circuit_open"`
}

// Usage returns the caller's household monthly LLM usage + budget + breaker state.
// GET /api/v1/admin/usage
func (h *AdminHandler) Usage(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)
	if householdID == "" {
		return echo.NewHTTPError(http.StatusUnauthorized, "missing household")
	}

	ym := llm.CurrentYearMonth()
	row, err := llm.GetMonthlyUsage(h.DB, householdID, ym)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	var budget int64
	var circuitOpen bool
	if h.Guard != nil {
		budget = h.Guard.Budget()
		if b := h.Guard.Breaker(); b != nil {
			circuitOpen = b.IsOpen()
		}
	}

	remaining := int64(-1) // -1 indicates "no cap"
	if budget > 0 {
		remaining = budget - (row.InputTokens + row.OutputTokens)
		if remaining < 0 {
			remaining = 0
		}
	}

	return c.JSON(http.StatusOK, usageResponse{
		HouseholdID:  householdID,
		Month:        ym,
		InputTokens:  row.InputTokens,
		OutputTokens: row.OutputTokens,
		Budget:       budget,
		Remaining:    remaining,
		CircuitOpen:  circuitOpen,
	})
}
