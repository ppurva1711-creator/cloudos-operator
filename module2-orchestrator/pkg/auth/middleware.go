package auth

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

// Context keys for storing values
type contextKey string

const (
	ClaimsContextKey contextKey = "claims"
)

// AuthMiddleware validates JWT tokens and injects claims into request context
// Returns 401 if token missing, 403 if token invalid/expired
func AuthMiddleware(jm *JWTManager, log *logrus.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString := extractBearerToken(r)

			// Missing token
			if tokenString == "" {
				log.Warnf("Request from %s missing authorization token", r.RemoteAddr)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write((&ErrorResponse{
					Error:  "Missing or invalid Authorization header (expected: Authorization: Bearer <token>)",
					Status: http.StatusUnauthorized,
				}).ToJSON())
				return
			}

			// Validate token
			claims, err := jm.ValidateToken(tokenString)
			if err != nil {
				statusCode := http.StatusForbidden
				errorMsg := "Invalid token"

				switch err {
				case ErrTokenExpired:
					errorMsg = "Token has expired"
				case ErrTokenInvalid:
					errorMsg = "Token is invalid or malformed"
				}

				log.Warnf("Token validation failed for %s (tenant: %s): %v", r.RemoteAddr, extractTenantID(r), err)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(statusCode)
				w.Write((&ErrorResponse{
					Error:  errorMsg,
					Status: statusCode,
				}).ToJSON())
				return
			}

			// Token valid - inject claims into context
			ctx := context.WithValue(r.Context(), ClaimsContextKey, claims)
			log.Debugf("User %s (tenant: %s) authenticated from %s", claims.UserID, claims.TenantID, r.RemoteAddr)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractBearerToken extracts JWT token from Authorization: Bearer header
func extractBearerToken(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ""
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	return parts[1]
}

// extractTenantID attempts to extract tenant ID from token for logging
// Returns empty string if token parsing fails
func extractTenantID(r *http.Request) string {
	tokenString := extractBearerToken(r)
	if tokenString == "" {
		return ""
	}

	// Decode without verification just for logging
	var claims Claims
	_, _, err := jwt.NewParser().ParseUnverified(tokenString, &claims)
	if err != nil {
		return ""
	}
	return claims.TenantID
}

// GetClaimsFromContext extracts claims from request context
func GetClaimsFromContext(ctx context.Context) (*Claims, error) {
	claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
	if !ok {
		return nil, fmt.Errorf("claims not found in context")
	}
	return claims, nil
}

// GetTenantIDFromContext extracts tenant ID from request context
func GetTenantIDFromContext(ctx context.Context) (string, error) {
	claims, err := GetClaimsFromContext(ctx)
	if err != nil {
		return "", err
	}
	return claims.TenantID, nil
}

// GetUserIDFromContext extracts user ID from request context
func GetUserIDFromContext(ctx context.Context) (string, error) {
	claims, err := GetClaimsFromContext(ctx)
	if err != nil {
		return "", err
	}
	return claims.UserID, nil
}
