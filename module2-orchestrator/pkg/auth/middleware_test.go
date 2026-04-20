package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestAuthMiddleware_ValidToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	log := logrus.New()
	middleware := AuthMiddleware(jm, log)

	// Generate valid token
	token, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin"}, 1)
	assert.NoError(t, err)

	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := GetClaimsFromContext(r.Context())
		assert.NoError(t, err)
		assert.NotNil(t, claims)
		assert.Equal(t, "tenant-1", claims.TenantID)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Apply middleware
	handler := middleware(testHandler)

	// Create request
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	w := httptest.NewRecorder()

	// Execute
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAuthMiddleware_MissingToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	log := logrus.New()
	middleware := AuthMiddleware(jm, log)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	// Request without Authorization header
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "Missing or invalid Authorization header")
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	log := logrus.New()
	middleware := AuthMiddleware(jm, log)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	// Request with invalid token
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer invalid.token.here")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid token")
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	// Generate expired token (0 hours = already expired)
	token, err := jm.GenerateToken("tenant-1", "user-1", "user@example.com", []string{"admin"}, 0)
	assert.NoError(t, err)

	log := logrus.New()
	middleware := AuthMiddleware(jm, log)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "Token has expired")
}

func TestAuthMiddleware_MalformedAuthHeader(t *testing.T) {
	os.Setenv("JWT_SECRET", "test-secret-key")
	defer os.Unsetenv("JWT_SECRET")

	jm, err := NewJWTManager("HS256")
	assert.NoError(t, err)

	log := logrus.New()
	middleware := AuthMiddleware(jm, log)

	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := middleware(testHandler)

	// Malformed: missing "Bearer" prefix
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "token-without-bearer-prefix")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetClaimsFromContext(t *testing.T) {
	claims := &Claims{
		TenantID: "tenant-1",
		UserID:   "user-1",
	}

	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	retrievedClaims, err := GetClaimsFromContext(ctx)
	assert.NoError(t, err)
	assert.Equal(t, claims, retrievedClaims)
}

func TestGetClaimsFromContext_Missing(t *testing.T) {
	ctx := context.Background()

	claims, err := GetClaimsFromContext(ctx)
	assert.Error(t, err)
	assert.Nil(t, claims)
}

func TestGetTenantIDFromContext(t *testing.T) {
	claims := &Claims{
		TenantID: "tenant-123",
		UserID:   "user-1",
	}

	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	tenantID, err := GetTenantIDFromContext(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "tenant-123", tenantID)
}

func TestGetUserIDFromContext(t *testing.T) {
	claims := &Claims{
		TenantID: "tenant-1",
		UserID:   "user-456",
	}

	ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

	userID, err := GetUserIDFromContext(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "user-456", userID)
}

// Rate Limiter Tests

func setupTestRedis(t *testing.T) (redis.Cmdable, func()) {
	server, err := miniredis.Run()
	assert.NoError(t, err)

	client := redis.NewClient(&redis.Options{
		Addr: server.Addr(),
	})

	return client, func() {
		server.Close()
	}
}

func TestRateLimiter_AllowUnderLimit(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)

	ctx := context.Background()

	// Should allow requests under limit
	for i := 0; i < 50; i++ {
		allowed, _, err := rl.Allow(ctx, "tenant-1")
		assert.NoError(t, err)
		assert.True(t, allowed, fmt.Sprintf("request %d should be allowed", i+1))
	}
}

func TestRateLimiter_ExceedPerMinuteLimit(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)
	rl.SetLimits(5, 1000) // 5 req/minute for testing

	ctx := context.Background()

	// Allow 5 requests
	for i := 0; i < 5; i++ {
		allowed, _, err := rl.Allow(ctx, "tenant-1")
		assert.NoError(t, err)
		assert.True(t, allowed)
	}

	// 6th request should fail
	allowed, remainingTime, err := rl.Allow(ctx, "tenant-1")
	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.Greater(t, remainingTime, time.Duration(0))
}

func TestRateLimiter_ExceedPerHourLimit(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)
	rl.SetLimits(10000, 10) // 10 req/hour for testing

	ctx := context.Background()

	// Allow 10 requests per hour
	for i := 0; i < 10; i++ {
		allowed, _, err := rl.Allow(ctx, "tenant-2")
		assert.NoError(t, err)
		assert.True(t, allowed)
	}

	// 11th request should fail with hour limit
	allowed, remainingTime, err := rl.Allow(ctx, "tenant-2")
	assert.NoError(t, err)
	assert.False(t, allowed)
	assert.Greater(t, remainingTime, time.Duration(0))
}

func TestRateLimiter_DifferentTenants(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)
	rl.perMinuteLimit = 5

	ctx := context.Background()

	// Tenant 1: 5 requests
	for i := 0; i < 5; i++ {
		allowed, _, err := rl.Allow(ctx, "tenant-1")
		assert.NoError(t, err)
		assert.True(t, allowed)
	}

	// Tenant 2: should still have quota
	allowed, _, err := rl.Allow(ctx, "tenant-2")
	assert.NoError(t, err)
	assert.True(t, allowed)

	// Tenant 1: 6th should fail
	allowed, _, err = rl.Allow(ctx, "tenant-1")
	assert.NoError(t, err)
	assert.False(t, allowed)

	// Tenant 2: more requests should still work
	for i := 0; i < 4; i++ {
		allowed, _, err := rl.Allow(ctx, "tenant-2")
		assert.NoError(t, err)
		assert.True(t, allowed)
	}
}

func TestRateLimiter_ResetTenantLimit(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)
	rl.perMinuteLimit = 5

	ctx := context.Background()

	// Exhaust limit
	for i := 0; i < 5; i++ {
		rl.Allow(ctx, "tenant-1")
	}

	// Verify limit exceeded
	allowed, _, err := rl.Allow(ctx, "tenant-1")
	assert.NoError(t, err)
	assert.False(t, allowed)

	// Reset limit
	err = rl.ResetTenantLimit(ctx, "tenant-1")
	assert.NoError(t, err)

	// Should be allowed again
	allowed, _, err = rl.Allow(ctx, "tenant-1")
	assert.NoError(t, err)
	assert.True(t, allowed)
}

func TestRateLimiter_EmptyTenantID(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)

	ctx := context.Background()

	allowed, _, err := rl.Allow(ctx, "")
	assert.Error(t, err)
	assert.False(t, allowed)
}

func TestRateLimiter_GetLimits(t *testing.T) {
	redisClient, cleanup := setupTestRedis(t)
	defer cleanup()

	log := logrus.New()
	rl := NewRateLimiter(redisClient, log)

	perMin, perHour := rl.GetLimits()
	assert.Equal(t, int64(100), perMin)
	assert.Equal(t, int64(1000), perHour)

	rl.SetLimits(50, 500)
	perMin, perHour = rl.GetLimits()
	assert.Equal(t, int64(50), perMin)
	assert.Equal(t, int64(500), perHour)
}
