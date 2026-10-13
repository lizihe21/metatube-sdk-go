package requestpolicy

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	ErrInvalidConfig  = errors.New("request policy: invalid configuration")
	ErrConfigConflict = errors.New("request policy: conflicting policy for shared scope")
	ErrWaitTimeout    = errors.New("request policy: queue wait exceeded")
)

// Config applies to each actual outbound HTTP attempt, not each business operation.
// Zero MaxAttempts means one attempt; retries are only performed for safe methods
// and transient HTTP statuses. There is no hidden background retry loop.
type Config struct {
	MinInterval       time.Duration
	Jitter            time.Duration
	MaxConcurrency    int
	MaxWait           time.Duration
	RateLimitCooldown time.Duration
	ForbiddenCooldown time.Duration
	MaxAttempts       int
}

func (c Config) normalized() (Config, error) {
	if c.MinInterval < 0 || c.Jitter < 0 || c.MaxWait < 0 ||
		c.RateLimitCooldown < 0 || c.ForbiddenCooldown < 0 ||
		c.MaxConcurrency < 0 || c.MaxAttempts < 0 {
		return Config{}, ErrInvalidConfig
	}
	if c.MaxConcurrency > 128 || c.MinInterval > 24*time.Hour || c.Jitter > 24*time.Hour {
		return Config{}, ErrInvalidConfig
	}
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 1
	}
	if c.MaxWait == 0 {
		c.MaxWait = 15 * time.Second
	}
	if c.MaxAttempts == 0 {
		c.MaxAttempts = 1
	}
	if c.MaxAttempts > 3 {
		return Config{}, fmt.Errorf("%w: max attempts must not exceed 3", ErrInvalidConfig)
	}
	if c.RateLimitCooldown == 0 {
		c.RateLimitCooldown = 30 * time.Second
	}
	return c, nil
}

type Governor struct {
	config        Config
	slots         chan struct{}
	mu            sync.Mutex
	nextStart     time.Time
	cooldownUntil time.Time
}

func New(cfg Config) (*Governor, error) {
	normalized, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	return &Governor{config: normalized, slots: make(chan struct{}, normalized.MaxConcurrency)}, nil
}

var shared = struct {
	sync.Mutex
	states map[string]*Governor
}{states: make(map[string]*Governor)}

// Shared coordinates independent collectors and fetchers targeting the same scope
// in this process. Different process/container instances need external coordination.
func Shared(scope string, cfg Config) (*Governor, error) {
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		return nil, fmt.Errorf("%w: empty scope", ErrInvalidConfig)
	}
	normalized, err := cfg.normalized()
	if err != nil {
		return nil, err
	}
	shared.Lock()
	defer shared.Unlock()
	if state, ok := shared.states[scope]; ok {
		if state.config != normalized {
			return nil, fmt.Errorf("%w: %s", ErrConfigConflict, scope)
		}
		return state, nil
	}
	state, err := New(normalized)
	if err != nil {
		return nil, err
	}
	shared.states[scope] = state
	return state, nil
}

func (g *Governor) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waitCtx := ctx
	cancel := func() {}
	if g.config.MaxWait > 0 {
		waitCtx, cancel = context.WithTimeout(ctx, g.config.MaxWait)
	}
	defer cancel()

	select {
	case g.slots <- struct{}{}:
	case <-waitCtx.Done():
		return nil, waitError(ctx, waitCtx)
	}
	release := func() { <-g.slots }
	for {
		if err := waitCtx.Err(); err != nil {
			release()
			return nil, waitError(ctx, waitCtx)
		}
		g.mu.Lock()
		now := time.Now()
		eligible := g.nextStart
		if g.cooldownUntil.After(eligible) {
			eligible = g.cooldownUntil
		}
		if !now.Before(eligible) {
			spacing := g.config.MinInterval
			if g.config.Jitter > 0 {
				spacing += time.Duration(rand.Int63n(int64(g.config.Jitter) + 1))
			}
			g.nextStart = now.Add(spacing)
			g.mu.Unlock()
			return release, nil
		}
		g.mu.Unlock()
		timer := time.NewTimer(time.Until(eligible))
		select {
		case <-timer.C:
		case <-waitCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			release()
			return nil, waitError(ctx, waitCtx)
		}
	}
}

func waitError(parent, wait context.Context) error {
	if err := parent.Err(); err != nil {
		return err
	}
	if errors.Is(wait.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %v", ErrWaitTimeout, wait.Err())
	}
	return wait.Err()
}

// ObserveResponse imposes a shared cooldown without shortening Retry-After.
func (g *Governor) ObserveResponse(status int, header http.Header) {
	var until time.Time
	switch status {
	case http.StatusTooManyRequests:
		until = time.Now().Add(g.config.RateLimitCooldown)
		if header != nil {
			if retryAt, ok := parseRetryAfter(header.Get("Retry-After"), time.Now()); ok {
				if retryAt.After(until) {
					until = retryAt
				}
			}
		}
	case http.StatusForbidden:
		if g.config.ForbiddenCooldown <= 0 {
			return
		}
		until = time.Now().Add(g.config.ForbiddenCooldown)
	default:
		return
	}
	g.mu.Lock()
	if until.After(g.cooldownUntil) {
		g.cooldownUntil = until
	}
	g.mu.Unlock()
}

func parseRetryAfter(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds < 0 {
			return time.Time{}, false
		}
		// Reject unreasonably long durations rather than overflowing time.Duration.
		if seconds > 60*60*24*365 {
			seconds = 60 * 60 * 24 * 365
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	if when, err := http.ParseTime(value); err == nil {
		return when, true
	}
	return time.Time{}, false
}
