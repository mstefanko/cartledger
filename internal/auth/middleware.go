package auth

import (
	"net/http"

	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
	echojwt "github.com/labstack/echo-jwt/v4"
)

// Context keys for extracting user info from echo.Context.
const (
	ContextKeyUserID      = "user_id"
	ContextKeyHouseholdID = "household_id"
)

// JWTMiddleware returns an Echo middleware that validates auth JWTs from the
// Authorization header and injects user_id and household_id into the context.
func JWTMiddleware(secret string) echo.MiddlewareFunc {
	config := echojwt.Config{
		SigningKey:  []byte(secret),
		SigningMethod: jwt.SigningMethodHS256.Alg(),
		NewClaimsFunc: func(c echo.Context) jwt.Claims {
			return &Claims{}
		},
		ErrorHandler: func(c echo.Context, err error) error {
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "invalid or expired token",
			})
		},
		SuccessHandler: func(c echo.Context) {
			token, ok := c.Get("user").(*jwt.Token)
			if !ok {
				return
			}
			claims, ok := token.Claims.(*Claims)
			if !ok {
				return
			}
			if claims.Type != TokenTypeAuth {
				return
			}
			c.Set(ContextKeyUserID, claims.UserID)
			c.Set(ContextKeyHouseholdID, claims.HouseholdID)
		},
	}
	return echojwt.WithConfig(config)
}

// UserIDFrom extracts the user_id from the echo context.
func UserIDFrom(c echo.Context) string {
	v, _ := c.Get(ContextKeyUserID).(string)
	return v
}

// HouseholdIDFrom extracts the household_id from the echo context.
func HouseholdIDFrom(c echo.Context) string {
	v, _ := c.Get(ContextKeyHouseholdID).(string)
	return v
}
