package api

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"

	"github.com/mstefanko/cartledger/internal/auth"
	"github.com/mstefanko/cartledger/internal/ws"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// CheckOrigin allows connections from any origin. In production, the reverse
	// proxy handles origin validation; the JWT token provides authentication.
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// WSHandler handles WebSocket upgrade requests.
type WSHandler struct {
	Hub       *ws.Hub
	JWTSecret string
}

// HandleWS upgrades the HTTP connection to a WebSocket after validating the JWT
// token provided as a query parameter (browser WebSocket API cannot send headers).
func (h *WSHandler) HandleWS(c echo.Context) error {
	tokenStr := c.QueryParam("token")
	if tokenStr == "" {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "missing token query parameter"})
	}

	claims, err := auth.ValidateAuthToken(h.JWTSecret, tokenStr)
	if err != nil {
		return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid token"})
	}

	conn, err := upgrader.Upgrade(c.Response(), c.Request(), nil)
	if err != nil {
		log.Printf("ws: upgrade error: %v", err)
		return nil // Upgrade already wrote a response on error.
	}

	ws.ServeWS(h.Hub, conn, claims.HouseholdID, claims.UserID)
	return nil
}
