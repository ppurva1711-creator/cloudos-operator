package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
)

// RateLimiter implements per-tenant rate limiting using Redis sliding window algorithm
type RateLimiter struct {
	redisClient    redis.Cmdable
	log            *logrus.Logger
	perMinuteLimit int64
	perHourLimit   int64
}

// NewRateLimiter creates a new RateLimiter instance
// Default: 100 req/minute, 1000 req/hour per tenant
func NewRateLimiter(redisClient redis.Cmdable, log *logrus.Logger) *RateLimiter {
	if log == nil {
		log = logrus.New()
	}

	return &RateLimiter{
		redisClient:    redisClient,
		log:            log,
		perMinuteLimit: 100,
		perHourLimit:   1000,
	}
}

// Allow checks if tenant can make a request (sliding window algorithm)
// Returns (allowed bool, remainingTime time.Duration, err error)
func (rl *RateLimiter) Allow(ctx context.Context, tenantID string) (bool, time.Duration, error) {
	if tenantID == "" {
		return false, 0, fmt.Errorf("tenant ID cannot be empty")
	}

	now := time.Now()
	nowUnix := now.Unix()

	// Sliding window keys
	minuteWindow := fmt.Sprintf("ratelimit:minute:%s:%d", tenantID, nowUnix/60)
	hourWindow := fmt.Sprintf("ratelimit:hour:%s:%d", tenantID, nowUnix/3600)

	// Use Redis pipeline for atomic operations
	pipe := rl.redisClient.Pipeline()

	// Increment counters
	pipe.Incr(ctx, minuteWindow)
	pipe.Incr(ctx, hourWindow)

	// Set TTL on keys (cleanup old entries)
	pipe.Expire(ctx, minuteWindow, 2*time.Minute)
	pipe.Expire(ctx, hourWindow, 2*time.Hour)

	// Execute pipeline
	results, err := pipe.Exec(ctx)
	if err != nil {
		rl.log.Errorf("Failed to check rate limit for tenant %s: %v", tenantID, err)
		return false, 0, err
	}

	// Extract counters from results
	minuteCount := results[0].(*redis.IntCmd).Val()
	hourCount := results[1].(*redis.IntCmd).Val()

	// Check limits
	if minuteCount > rl.perMinuteLimit {
		remainingTime := time.Until(now.Truncate(time.Minute).Add(time.Minute))
		rl.log.Warnf("Rate limit exceeded for tenant %s (minute: %d/%d)", tenantID, minuteCount, rl.perMinuteLimit)
		return false, remainingTime, nil
	}

	if hourCount > rl.perHourLimit {
		remainingTime := time.Until(now.Truncate(time.Hour).Add(time.Hour))
		rl.log.Warnf("Rate limit exceeded for tenant %s (hour: %d/%d)", tenantID, hourCount, rl.perHourLimit)
		return false, remainingTime, nil
	}

	return true, 0, nil
}

// SetLimits allows customization of rate limits per tenant
func (rl *RateLimiter) SetLimits(perMinute, perHour int64) {
	rl.perMinuteLimit = perMinute
	rl.perHourLimit = perHour
	rl.log.Infof("Rate limits updated: %d req/min, %d req/hour", perMinute, perHour)
}

// GetLimits returns current rate limits
func (rl *RateLimiter) GetLimits() (perMinute, perHour int64) {
	return rl.perMinuteLimit, rl.perHourLimit
}

// GetTenantStats returns current request count for a tenant
func (rl *RateLimiter) GetTenantStats(ctx context.Context, tenantID string) (minuteCount, hourCount int64, err error) {
	now := time.Now()
	nowUnix := now.Unix()

	minuteWindow := fmt.Sprintf("ratelimit:minute:%s:%d", tenantID, nowUnix/60)
	hourWindow := fmt.Sprintf("ratelimit:hour:%s:%d", tenantID, nowUnix/3600)

	pipe := rl.redisClient.Pipeline()
	pipe.Get(ctx, minuteWindow)
	pipe.Get(ctx, hourWindow)

	results, err := pipe.Exec(ctx)
	if err != nil {
		return 0, 0, err
	}

	minuteCmd := results[0].(*redis.StringCmd)
	hourCmd := results[1].(*redis.StringCmd)

	minuteVal, err := minuteCmd.Int64()
	if err != nil && err != redis.Nil {
		return 0, 0, err
	}

	hourVal, err := hourCmd.Int64()
	if err != nil && err != redis.Nil {
		return 0, 0, err
	}

	return minuteVal, hourVal, nil
}

// ResetTenantLimit resets rate limit counters for a tenant (useful for testing)
func (rl *RateLimiter) ResetTenantLimit(ctx context.Context, tenantID string) error {
	pattern := fmt.Sprintf("ratelimit:*:%s:*", tenantID)

	// Scan for keys matching pattern
	var cursor uint64
	for {
		keys, nextCursor, err := rl.redisClient.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return err
		}

		if len(keys) > 0 {
			if err := rl.redisClient.Del(ctx, keys...).Err(); err != nil {
				return err
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}

	rl.log.Infof("Rate limit reset for tenant %s", tenantID)
	return nil
}
