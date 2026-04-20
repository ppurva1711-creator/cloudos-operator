package auth

import (
	"os"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
)

func TestNewJWTManager_HS256(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)
	assert.NotNil(t, jm)
	assert.Equal(t, "HS256", jm.algorithm)
}

func TestNewJWTManager_MissingSecret(t *testing.T) {
	os.Unsetenv("JWT_SECRET")
	os.Unsetenv("JWT_PUBLIC_KEY")

	jm, err := NewJWTManager("HS256")
	assert.Error(t, err)
	assert.Nil(t, jm)
	assert.ErrorIs(t, err, ErrKeyNotFound)
}

func TestGenerateToken_ValidToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	token, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin"}, 1)
	assert.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestGenerateToken_HS256Only(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	token, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin"}, 1)
	assert.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestValidateToken_ValidToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	// Generate token
	tokenString, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin"}, 1)
	assert.NoError(t, err)

	// Validate token
	claims, err := jm.ValidateToken(tokenString)
	assert.NoError(t, err)
	assert.NotNil(t, claims)
	assert.Equal(t, "tenant-1", claims.TenantID)
	assert.Equal(t, "user-1", claims.UserID)
	assert.Equal(t, "user@example.com", claims.Email)
	assert.Contains(t, claims.Roles, "admin")
}

func TestValidateToken_EmptyToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	claims, err := jm.ValidateToken("")
	assert.Error(t, err)
	assert.Nil(t, claims)
	assert.ErrorIs(t, err, ErrTokenMissing)
}

func TestValidateToken_ExpiredToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	// Create expired token manually
	now := time.Now()
	claims := &Claims{
		TenantID: "tenant-1",
		UserID:   "user-1",
		Email:    "user@example.com",
		Roles:    []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(-1 * time.Hour)), // Expired 1 hour ago
			IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
			Issuer:    "cloudtask-orchestrator",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte("test-secret-key"))
	assert.NoError(t, err)

	// Validate should fail with expired error
	validatedClaims, err := jm.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Nil(t, validatedClaims)
	assert.ErrorIs(t, err, ErrTokenExpired)
}

func TestValidateToken_InvalidSignature(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	// Generate token with different secret
	now := time.Now()
	claims := &Claims{
		TenantID: "tenant-1",
		UserID:   "user-1",
		Email:    "user@example.com",
		Roles:    []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "cloudtask-orchestrator",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte("different-secret"))
	assert.NoError(t, err)

	// Validate should fail
	validatedClaims, err := jm.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Nil(t, validatedClaims)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestValidateToken_MissingRequiredClaims(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	// Create token missing TenantID
	now := time.Now()
	claims := &Claims{
		TenantID: "", // Missing
		UserID:   "user-1",
		Email:    "user@example.com",
		Roles:    []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(now),
			Issuer:    "cloudtask-orchestrator",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte("test-secret-key"))
	assert.NoError(t, err)

	validatedClaims, err := jm.ValidateToken(tokenString)
	assert.Error(t, err)
	assert.Nil(t, validatedClaims)
	assert.ErrorIs(t, err, ErrTokenInvalid)
}

func TestValidateToken_MultipleRoles(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	tokenString, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin", "developer", "viewer"}, 1)
	assert.NoError(t, err)

	claims, err := jm.ValidateToken(tokenString)
	assert.NoError(t, err)
	assert.Len(t, claims.Roles, 3)
	assert.Contains(t, claims.Roles, "admin")
	assert.Contains(t, claims.Roles, "developer")
	assert.Contains(t, claims.Roles, "viewer")
}

func TestValidateToken_NoRoles(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	tokenString, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{}, 1)
	assert.NoError(t, err)

	claims, err := jm.ValidateToken(tokenString)
	assert.NoError(t, err)
	assert.NotNil(t, claims.Roles) // Should be empty slice, not nil
	assert.Len(t, claims.Roles, 0)
}

func TestErrorResponse_ToJSON(t *testing.T) {
	errResp := &ErrorResponse{
		Error:  "test error",
		Status: 401,
	}

	jsonBytes := errResp.ToJSON()
	assert.NotEmpty(t, jsonBytes)
	assert.Contains(t, string(jsonBytes), "test error")
	assert.Contains(t, string(jsonBytes), "401")
}
