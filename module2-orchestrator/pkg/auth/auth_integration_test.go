package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testJWTSecret = "test-secret-key-for-jwt-integration-tests-minimum-32-chars"
)

// ---- End-to-End: Generate JWT → Call API → Verify Response ----

func TestJWTIntegration_EndToEndFlow(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err, "Failed to create JWT manager")

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Protected handler that returns claims as JSON
	protectedHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := GetClaimsFromContext(r.Context())
		if err != nil {
			http.Error(w, "no claims in context", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"tenant_id": claims.TenantID,
			"user_id":   claims.UserID,
			"email":     claims.Email,
			"roles":     claims.Roles,
		})
	})

	// Wrap with real AuthMiddleware
	server := httptest.NewServer(AuthMiddleware(manager, log)(protectedHandler))
	defer server.Close()

	client := &http.Client{}

	// Step 1: Generate token via JWTManager
	token, err := manager.GenerateToken("tenant-a", "user-001", "user@example.com", []string{"admin", "viewer"}, 1)
	require.NoError(t, err, "GenerateToken should succeed")
	assert.NotEmpty(t, token)

	// Step 2: Call API with valid token → 200 + correct claims
	req, err := http.NewRequest("GET", server.URL+"/protected", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Valid token should return 200")

	var body map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&body)
	require.NoError(t, err)
	assert.Equal(t, "tenant-a", body["tenant_id"])
	assert.Equal(t, "user-001", body["user_id"])
	assert.Equal(t, "user@example.com", body["email"])

	// Step 3: No token → 401
	req2, _ := http.NewRequest("GET", server.URL+"/protected", nil)
	resp2, err := client.Do(req2)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp2.StatusCode, "Missing token should return 401")

	// Step 4: Invalid token → 403
	req3, _ := http.NewRequest("GET", server.URL+"/protected", nil)
	req3.Header.Set("Authorization", "Bearer this-is-not-a-valid-jwt-token")
	resp3, err := client.Do(req3)
	require.NoError(t, err)
	defer resp3.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp3.StatusCode, "Invalid token should return 403")

	// Step 5: Expired token → 403
	expiredClaims := &Claims{
		TenantID: "tenant-a",
		UserID:   "user-001",
		Email:    "user@example.com",
		Roles:    []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	expiredToken := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims)
	expiredTokenStr, err := expiredToken.SignedString([]byte(testJWTSecret))
	require.NoError(t, err)

	req4, _ := http.NewRequest("GET", server.URL+"/protected", nil)
	req4.Header.Set("Authorization", "Bearer "+expiredTokenStr)
	resp4, err := client.Do(req4)
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp4.StatusCode, "Expired token should return 403")

	// Step 6: Malformed Authorization header (not "Bearer <token>")
	req5, _ := http.NewRequest("GET", server.URL+"/protected", nil)
	req5.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	resp5, err := client.Do(req5)
	require.NoError(t, err)
	defer resp5.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp5.StatusCode, "Non-bearer auth should return 401")
}

// ---- Rate Limiting Across Multiple Requests ----

func TestJWTIntegration_RateLimiting(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err)

	// Start miniredis for rate limiter
	mr, err := miniredis.Run()
	require.NoError(t, err)
	defer mr.Close()

	redisClient := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer redisClient.Close()

	log := logrus.New()
	log.SetLevel(logrus.WarnLevel)

	rateLimiter := NewRateLimiter(redisClient, log)
	rateLimiter.SetLimits(5, 1000) // 5 per minute, 1000 per hour

	// Handler with auth + rate limiting
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := GetClaimsFromContext(r.Context())
		if err != nil {
			http.Error(w, "no claims", http.StatusInternalServerError)
			return
		}

		allowed, retryAfter, rlErr := rateLimiter.Allow(r.Context(), claims.TenantID)
		if rlErr != nil {
			http.Error(w, "rate limit error", http.StatusInternalServerError)
			return
		}
		if !allowed {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", retryAfter.Seconds()))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write((&ErrorResponse{Error: "Rate limit exceeded", Status: http.StatusTooManyRequests}).ToJSON())
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	server := httptest.NewServer(AuthMiddleware(manager, log)(handler))
	defer server.Close()

	token, err := manager.GenerateToken("tenant-a", "user-001", "user@example.com", []string{"admin"}, 1)
	require.NoError(t, err)

	client := &http.Client{}
	successCount := 0
	rateLimitedCount := 0

	// Send 10 rapid requests — first 5 should pass, next 5 rate-limited
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest("GET", server.URL+"/api", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := client.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		if resp.StatusCode == http.StatusOK {
			successCount++
		} else if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	assert.Equal(t, 5, successCount, "First 5 requests should succeed")
	assert.Equal(t, 5, rateLimitedCount, "Remaining 5 should be rate limited")

	// Verify Retry-After header is set on rate limited responses
	req, _ := http.NewRequest("GET", server.URL+"/api", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("Retry-After"), "Retry-After header should be set")

	// Verify tenant stats
	minuteCount, hourCount, err := rateLimiter.GetTenantStats(context.Background(), "tenant-a")
	require.NoError(t, err)
	assert.Greater(t, minuteCount, int64(0), "Minute count should be > 0")
	assert.Greater(t, hourCount, int64(0), "Hour count should be > 0")
}

// ---- Tenant Isolation: Tenant-A Cannot Access Tenant-B Resources ----

func TestJWTIntegration_TenantIsolation(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// In-memory resource store: tenant -> [resources]
	resources := make(map[string]map[string]string)
	mu := sync.RWMutex{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, err := GetClaimsFromContext(r.Context())
		if err != nil {
			http.Error(w, "no claims", http.StatusInternalServerError)
			return
		}

		switch r.Method {
		case http.MethodPost:
			// Create resource scoped to tenant
			mu.Lock()
			if resources[claims.TenantID] == nil {
				resources[claims.TenantID] = make(map[string]string)
			}
			resources[claims.TenantID]["resource-1"] = claims.TenantID
			mu.Unlock()

			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{"status": "created", "owner": claims.TenantID})

		case http.MethodGet:
			targetTenant := r.URL.Query().Get("tenant")
			if targetTenant == "" {
				targetTenant = claims.TenantID
			}

			// Enforce isolation: can only access own resources
			if targetTenant != claims.TenantID {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				w.Write((&ErrorResponse{Error: "Access denied to other tenant resources", Status: http.StatusForbidden}).ToJSON())
				return
			}

			mu.RLock()
			tenantResources, exists := resources[targetTenant]
			mu.RUnlock()

			if !exists {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(tenantResources)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	server := httptest.NewServer(AuthMiddleware(manager, log)(handler))
	defer server.Close()

	client := &http.Client{}

	// Generate tokens for two tenants
	tokenA, err := manager.GenerateToken("tenant-a", "user-a", "a@example.com", []string{"admin"}, 1)
	require.NoError(t, err)
	tokenB, err := manager.GenerateToken("tenant-b", "user-b", "b@example.com", []string{"admin"}, 1)
	require.NoError(t, err)

	// Tenant A creates a resource
	reqPost, _ := http.NewRequest("POST", server.URL+"/resource", nil)
	reqPost.Header.Set("Authorization", "Bearer "+tokenA)
	resp, err := client.Do(reqPost)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode, "Tenant A should create resource")

	// Tenant A can access own resource
	reqGetOwn, _ := http.NewRequest("GET", server.URL+"/resource?tenant=tenant-a", nil)
	reqGetOwn.Header.Set("Authorization", "Bearer "+tokenA)
	resp2, err := client.Do(reqGetOwn)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "Tenant A should access own resources")

	// Tenant B CANNOT access Tenant A's resources
	reqCross, _ := http.NewRequest("GET", server.URL+"/resource?tenant=tenant-a", nil)
	reqCross.Header.Set("Authorization", "Bearer "+tokenB)
	resp3, err := client.Do(reqCross)
	require.NoError(t, err)
	resp3.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp3.StatusCode, "Tenant B should NOT access Tenant A resources")

	// Tenant B can access their own (empty) namespace without error
	reqGetB, _ := http.NewRequest("GET", server.URL+"/resource?tenant=tenant-b", nil)
	reqGetB.Header.Set("Authorization", "Bearer "+tokenB)
	resp4, err := client.Do(reqGetB)
	require.NoError(t, err)
	resp4.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp4.StatusCode, "Tenant B has no resources yet")
}

// ---- Concurrent Requests ----

func TestJWTIntegration_ConcurrentRequests(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := GetClaimsFromContext(r.Context())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"tenant": claims.TenantID})
	})

	server := httptest.NewServer(AuthMiddleware(manager, log)(handler))
	defer server.Close()

	token, _ := manager.GenerateToken("tenant-a", "user-001", "user@example.com", []string{"admin"}, 1)

	numRequests := 50
	var wg sync.WaitGroup
	successCount := int32(0)
	mu := sync.Mutex{}

	client := &http.Client{}

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest("GET", server.URL, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}()
	}

	wg.Wait()
	assert.Equal(t, int32(numRequests), successCount, "All concurrent requests should succeed")
}

// ---- Role-Based Access ----

func TestJWTIntegration_MultipleRoles(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Role-gated handler
	mux := http.NewServeMux()

	authMW := AuthMiddleware(manager, log)

	mux.Handle("/admin", authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := GetClaimsFromContext(r.Context())
		hasAdmin := false
		for _, role := range claims.Roles {
			if role == "admin" {
				hasAdmin = true
				break
			}
		}
		if !hasAdmin {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"access": "admin"})
	})))

	mux.Handle("/user", authMW(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"access": "user"})
	})))

	server := httptest.NewServer(mux)
	defer server.Close()

	client := &http.Client{}

	// Admin token can access /admin
	adminToken, _ := manager.GenerateToken("tenant-a", "admin-001", "admin@example.com", []string{"admin", "user"}, 1)
	reqAdmin, _ := http.NewRequest("GET", server.URL+"/admin", nil)
	reqAdmin.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := client.Do(reqAdmin)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Admin should access /admin")

	// User token CANNOT access /admin
	userToken, _ := manager.GenerateToken("tenant-a", "user-001", "user@example.com", []string{"user"}, 1)
	reqUser, _ := http.NewRequest("GET", server.URL+"/admin", nil)
	reqUser.Header.Set("Authorization", "Bearer "+userToken)
	resp2, err := client.Do(reqUser)
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusForbidden, resp2.StatusCode, "Non-admin should NOT access /admin")

	// User token CAN access /user
	reqUser2, _ := http.NewRequest("GET", server.URL+"/user", nil)
	reqUser2.Header.Set("Authorization", "Bearer "+userToken)
	resp3, err := client.Do(reqUser2)
	require.NoError(t, err)
	resp3.Body.Close()
	assert.Equal(t, http.StatusOK, resp3.StatusCode, "User should access /user")
}

// ---- Helpers for context extraction ----

func TestJWTIntegration_ContextHelpers(t *testing.T) {
	os.Setenv("JWT_SECRET", testJWTSecret)
	defer os.Unsetenv("JWT_SECRET")

	manager, err := NewJWTManager("HS256")
	require.NoError(t, err)

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID, err := GetTenantIDFromContext(r.Context())
		require.NoError(t, err)

		userID, err := GetUserIDFromContext(r.Context())
		require.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"tenant_id": tenantID,
			"user_id":   userID,
		})
	})

	server := httptest.NewServer(AuthMiddleware(manager, log)(handler))
	defer server.Close()

	token, _ := manager.GenerateToken("tenant-x", "user-42", "x@test.com", []string{"viewer"}, 1)
	req, _ := http.NewRequest("GET", server.URL, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	assert.Equal(t, "tenant-x", body["tenant_id"])
	assert.Equal(t, "user-42", body["user_id"])

	// Test context helpers with no claims
	_, err = GetClaimsFromContext(context.Background())
	assert.Error(t, err)
	_, err = GetTenantIDFromContext(context.Background())
	assert.Error(t, err)
	_, err = GetUserIDFromContext(context.Background())
	assert.Error(t, err)
}
