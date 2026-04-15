package api

import (
	"database/sql"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/config"
)

// setupMu serializes setup requests to prevent the race condition where two
// concurrent requests both pass the "no users exist" check before either writes.
var setupMu sync.Mutex

// emailRegex is a basic email format check: must contain @ with a dot after it.
var emailRegex = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// AuthHandler holds dependencies for auth-related endpoints.
type AuthHandler struct {
	DB  *sql.DB
	Cfg *config.Config
}

// --- Request / Response types ---

type setupRequest struct {
	HouseholdName string `json:"household_name"`
	UserName      string `json:"user_name"`
	Email         string `json:"email"`
	Password      string `json:"password"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type joinRequest struct {
	Token    string `json:"token"`
	UserName string `json:"user_name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type authResponse struct {
	Token string `json:"token"`
	User  userResponse `json:"user"`
}

type userResponse struct {
	ID          string `json:"id"`
	HouseholdID string `json:"household_id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
}

// RegisterRoutes mounts auth endpoints onto the given Echo groups.
// publicRateLimited applies rate limiting to login/setup/join to prevent brute-force attacks.
func (h *AuthHandler) RegisterRoutes(public *echo.Group, publicRateLimited *echo.Group, protected *echo.Group) {
	public.GET("/status", h.Status)
	public.GET("/invite/:token/validate", h.ValidateInvite)

	// Rate-limited auth endpoints: 10 requests/minute per IP.
	publicRateLimited.POST("/setup", h.Setup)
	publicRateLimited.POST("/login", h.Login)
	publicRateLimited.POST("/join", h.Join)

	protected.POST("/invite", h.CreateInvite)
	protected.GET("/profile", h.GetProfile)
	protected.PUT("/profile", h.UpdateProfile)
	protected.PUT("/household", h.UpdateHousehold)
	protected.DELETE("/household/data", h.DeleteAllData)
}

// Status returns whether the app needs initial setup (no users exist).
// GET /api/v1/status
func (h *AuthHandler) Status(c echo.Context) error {
	var count int
	if err := h.DB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	return c.JSON(http.StatusOK, map[string]bool{"needs_setup": count == 0})
}

// Setup handles first-boot setup: creates household + user in a single transaction.
// POST /api/v1/setup
func (h *AuthHandler) Setup(c echo.Context) error {
	var req setupRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.HouseholdName == "" || req.UserName == "" || req.Email == "" || req.Password == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "all fields are required"})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !emailRegex.MatchString(req.Email) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid email format"})
	}
	if len(req.Password) < 8 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
	}

	// Serialize setup requests with a mutex to prevent the TOCTOU race where
	// SQLite's deferred BEGIN lets two concurrent requests both pass the
	// "no users exist" check before either writes.
	setupMu.Lock()
	defer setupMu.Unlock()

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if count > 0 {
		return c.JSON(http.StatusConflict, map[string]string{"error": "setup already completed"})
	}

	now := time.Now().UTC()
	householdID := uuid.New().String()
	userID := uuid.New().String()

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}

	_, err = tx.Exec(
		"INSERT INTO households (id, name, created_at) VALUES (?, ?, ?)",
		householdID, req.HouseholdName, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create household"})
	}

	_, err = tx.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		userID, householdID, req.Email, req.UserName, passwordHash, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	token, err := auth.CreateAuthToken(h.Cfg.JWTSecret, userID, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
	}

	return c.JSON(http.StatusCreated, authResponse{
		Token: token,
		User: userResponse{
			ID:          userID,
			HouseholdID: householdID,
			Email:       req.Email,
			Name:        req.UserName,
		},
	})
}

// Login authenticates a user by email and password, returning a JWT.
// POST /api/v1/login
func (h *AuthHandler) Login(c echo.Context) error {
	var req loginRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.Email == "" || req.Password == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "email and password are required"})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	var userID, householdID, name, passwordHash string
	err := h.DB.QueryRow(
		"SELECT id, household_id, name, password_hash FROM users WHERE email = ?",
		req.Email,
	).Scan(&userID, &householdID, &name, &passwordHash)
	if err == sql.ErrNoRows {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	if err := auth.CheckPassword(passwordHash, req.Password); err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
	}

	token, err := auth.CreateAuthToken(h.Cfg.JWTSecret, userID, householdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
	}

	return c.JSON(http.StatusOK, authResponse{
		Token: token,
		User: userResponse{
			ID:          userID,
			HouseholdID: householdID,
			Email:       req.Email,
			Name:        name,
		},
	})
}

// CreateInvite generates an invite link with a signed JWT (7-day expiry).
// POST /api/v1/invite (authenticated)
func (h *AuthHandler) CreateInvite(c echo.Context) error {
	userID := auth.UserIDFrom(c)
	householdID := auth.HouseholdIDFrom(c)

	// Look up inviter name for the invite token claims.
	var inviterName string
	if err := h.DB.QueryRow("SELECT name FROM users WHERE id = ?", userID).Scan(&inviterName); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	token, err := auth.CreateInviteToken(h.Cfg.JWTSecret, householdID, userID, inviterName)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create invite token"})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"token":      token,
		"expires_in": "7 days",
	})
}

// ValidateInvite validates an invite JWT and returns household + inviter info.
// GET /api/v1/invite/:token/validate
func (h *AuthHandler) ValidateInvite(c echo.Context) error {
	tokenStr := c.Param("token")
	claims, err := auth.ValidateInviteToken(h.Cfg.JWTSecret, tokenStr)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired invite"})
	}

	var householdName string
	if err := h.DB.QueryRow("SELECT name FROM households WHERE id = ?", claims.HouseholdID).Scan(&householdName); err != nil {
		return c.JSON(http.StatusNotFound, map[string]string{"error": "household not found"})
	}

	return c.JSON(http.StatusOK, map[string]string{
		"household_name": householdName,
		"invited_by":     claims.InviterName,
	})
}

// Join validates an invite JWT, creates a new user, and returns an auth JWT.
// POST /api/v1/join
func (h *AuthHandler) Join(c echo.Context) error {
	var req joinRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.Token == "" || req.UserName == "" || req.Email == "" || req.Password == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "all fields are required"})
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	if !emailRegex.MatchString(req.Email) {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid email format"})
	}
	if len(req.Password) < 8 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
	}

	claims, err := auth.ValidateInviteToken(h.Cfg.JWTSecret, req.Token)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or expired invite"})
	}

	now := time.Now().UTC()
	userID := uuid.New().String()

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to hash password"})
	}

	// Use a transaction to prevent race condition between email check and insert.
	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	// Check for duplicate email inside the transaction.
	var existing int
	if err := tx.QueryRow("SELECT COUNT(*) FROM users WHERE email = ?", req.Email).Scan(&existing); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	if existing > 0 {
		return c.JSON(http.StatusConflict, map[string]string{"error": "email already registered"})
	}

	_, err = tx.Exec(
		"INSERT INTO users (id, household_id, email, name, password_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		userID, claims.HouseholdID, req.Email, req.UserName, passwordHash, now,
	)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create user"})
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	token, err := auth.CreateAuthToken(h.Cfg.JWTSecret, userID, claims.HouseholdID)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create token"})
	}

	return c.JSON(http.StatusCreated, authResponse{
		Token: token,
		User: userResponse{
			ID:          userID,
			HouseholdID: claims.HouseholdID,
			Email:       req.Email,
			Name:        req.UserName,
		},
	})
}

// GetProfile returns the current user's profile and household info.
// GET /api/v1/profile
func (h *AuthHandler) GetProfile(c echo.Context) error {
	userID := auth.UserIDFrom(c)
	householdID := auth.HouseholdIDFrom(c)

	var user userResponse
	err := h.DB.QueryRow(
		"SELECT id, household_id, email, name FROM users WHERE id = ?", userID,
	).Scan(&user.ID, &user.HouseholdID, &user.Email, &user.Name)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	var householdName string
	_ = h.DB.QueryRow("SELECT name FROM households WHERE id = ?", householdID).Scan(&householdName)

	return c.JSON(http.StatusOK, map[string]interface{}{
		"user":           user,
		"household_name": householdName,
	})
}

// UpdateProfile updates the current user's name and/or email.
// PUT /api/v1/profile
func (h *AuthHandler) UpdateProfile(c echo.Context) error {
	userID := auth.UserIDFrom(c)

	var req struct {
		Name  *string `json:"name"`
		Email *string `json:"email"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}

	setClauses := make([]string, 0)
	args := make([]interface{}, 0)
	if req.Name != nil && *req.Name != "" {
		setClauses = append(setClauses, "name = ?")
		args = append(args, *req.Name)
	}
	if req.Email != nil && *req.Email != "" {
		email := strings.ToLower(strings.TrimSpace(*req.Email))
		if !emailRegex.MatchString(email) {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid email format"})
		}
		setClauses = append(setClauses, "email = ?")
		args = append(args, email)
	}
	if len(setClauses) == 0 {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "no fields to update"})
	}

	args = append(args, userID)
	query := "UPDATE users SET " + strings.Join(setClauses, ", ") + " WHERE id = ?"
	if _, err := h.DB.Exec(query, args...); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// UpdateHousehold updates the household name.
// PUT /api/v1/household
func (h *AuthHandler) UpdateHousehold(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	var req struct {
		Name string `json:"name"`
	}
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
	}
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "name is required"})
	}

	if _, err := h.DB.Exec("UPDATE households SET name = ? WHERE id = ?", req.Name, householdID); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}

	return c.JSON(http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteAllData removes all household data except users and the household itself.
// DELETE /api/v1/household/data
func (h *AuthHandler) DeleteAllData(c echo.Context) error {
	householdID := auth.HouseholdIDFrom(c)

	tx, err := h.DB.Begin()
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "database error"})
	}
	defer tx.Rollback()

	// Order matters: delete children before parents.
	tables := []string{
		"shopping_list_items", "shopping_lists",
		"product_prices", "line_items", "receipts",
		"product_images", "product_links", "product_aliases",
		"matching_rules", "unit_conversions",
		"products", "stores",
	}

	for _, table := range tables {
		var query string
		switch table {
		case "shopping_list_items":
			query = "DELETE FROM shopping_list_items WHERE list_id IN (SELECT id FROM shopping_lists WHERE household_id = ?)"
		case "product_prices":
			query = "DELETE FROM product_prices WHERE receipt_id IN (SELECT id FROM receipts WHERE household_id = ?)"
		case "line_items":
			query = "DELETE FROM line_items WHERE receipt_id IN (SELECT id FROM receipts WHERE household_id = ?)"
		case "product_images", "product_links":
			query = "DELETE FROM " + table + " WHERE product_id IN (SELECT id FROM products WHERE household_id = ?)"
		case "product_aliases":
			query = "DELETE FROM product_aliases WHERE product_id IN (SELECT id FROM products WHERE household_id = ?)"
		case "unit_conversions":
			query = "DELETE FROM unit_conversions WHERE product_id IN (SELECT id FROM products WHERE household_id = ?)"
		default:
			query = "DELETE FROM " + table + " WHERE household_id = ?"
		}
		if _, err := tx.Exec(query, householdID); err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to clear " + table})
		}
	}

	if err := tx.Commit(); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to commit"})
	}

	// Clean up receipt image directories.
	dataDir := h.Cfg.DataDir
	receiptsDir := dataDir + "/receipts"
	_ = os.RemoveAll(receiptsDir)

	return c.JSON(http.StatusOK, map[string]string{"status": "all data deleted"})
}
