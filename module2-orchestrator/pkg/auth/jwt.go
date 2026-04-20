package auth

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Custom error types for JWT validation
var (
	ErrTokenExpired  = errors.New("token has expired")
	ErrTokenInvalid  = errors.New("token is invalid or malformed")
	ErrTokenMissing  = errors.New("token is missing from request")
	ErrKeyNotFound   = errors.New("signing key not found in environment")
	ErrAlgorithmType = errors.New("unsupported signing algorithm")
)

// Claims extends JWT standard claims with custom fields
type Claims struct {
	TenantID string   `json:"tenant_id"`
	UserID   string   `json:"user_id"`
	Email    string   `json:"email"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// JWTManager handles JWT token validation and claims extraction
// Supports both HS256 (symmetric) and RS256 (asymmetric) algorithms
type JWTManager struct {
	secretKey  []byte
	publicKey  *rsa.PublicKey
	algorithm  string
}

// NewJWTManager creates a new JWTManager instance
// Reads JWT_SECRET for HS256 or JWT_PUBLIC_KEY for RS256 from environment
func NewJWTManager(algorithm string) (*JWTManager, error) {
	if algorithm == "" {
		algorithm = "HS256" // default
	}

	jm := &JWTManager{
		algorithm: algorithm,
	}

	switch algorithm {
	case "HS256":
		// HS256: Symmetric shared secret
		secret := os.Getenv("JWT_SECRET")
		if secret == "" {
			return nil, fmt.Errorf("JWT_SECRET environment variable not set: %w", ErrKeyNotFound)
		}
		jm.secretKey = []byte(secret)

	case "RS256":
		// RS256: Asymmetric public key
		publicKeyPEM := os.Getenv("JWT_PUBLIC_KEY")
		if publicKeyPEM == "" {
			return nil, fmt.Errorf("JWT_PUBLIC_KEY environment variable not set: %w", ErrKeyNotFound)
		}
		
		publicKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("failed to parse RSA public key: %w", err)
		}
		jm.publicKey = publicKey

	default:
		return nil, fmt.Errorf("unsupported algorithm %s: %w", algorithm, ErrAlgorithmType)
	}

	return jm, nil
}

// GenerateToken generates a new JWT token (HS256 only, for testing)
func (jm *JWTManager) GenerateToken(tenantID, userID, email string, roles []string, expirationHours int) (string, error) {
	if jm.algorithm != "HS256" {
		return "", fmt.Errorf("GenerateToken only supports HS256; use RSA key pair for RS256")
	}

	now := time.Now()
	claims := &Claims{
		TenantID: tenantID,
		UserID:   userID,
		Email:    email,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(expirationHours) * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			Issuer:    "cloudtask-orchestrator",
			Subject:   userID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jm.secretKey)
	if err != nil {
		return "", fmt.Errorf("failed to sign token: %w", err)
	}

	return tokenString, nil
}

// ValidateToken validates a JWT token and returns the claims
func (jm *JWTManager) ValidateToken(tokenString string) (*Claims, error) {
	if tokenString == "" {
		return nil, ErrTokenMissing
	}

	keyFunc := func(token *jwt.Token) (interface{}, error) {
		switch jm.algorithm {
		case "HS256":
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jm.secretKey, nil

		case "RS256":
			if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jm.publicKey, nil
		}
		return nil, ErrAlgorithmType
	}

	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, keyFunc)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, fmt.Errorf("%w: %w", ErrTokenInvalid, err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrTokenInvalid
	}

	// Validate required fields
	if claims.TenantID == "" || claims.UserID == "" {
		return nil, fmt.Errorf("missing required claims (tenant_id, user_id): %w", ErrTokenInvalid)
	}

	return claims, nil
}

// ErrorResponse for JSON error responses
type ErrorResponse struct {
	Error  string `json:"error"`
	Status int    `json:"status"`
}

// ToJSON converts error to JSON format
func (e *ErrorResponse) ToJSON() []byte {
	data, _ := json.Marshal(e)
	return data
}
