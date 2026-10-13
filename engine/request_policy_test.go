package engine

import (
	"testing"
	"time"

	"github.com/metatube-community/metatube-sdk-go/internal/envconfig"
)

func TestReadRequestPolicyConfig(t *testing.T) {
	cfg := envconfig.NewConfig()
	cfg.Set("MIN_INTERVAL", "5s")
	cfg.Set("MAX_CONCURRENCY", "1")
	cfg.Set("MAX_ATTEMPTS", "2")
	cfg.Set("MAX_WAIT", "12s")
	if !hasRequestPolicyConfig(cfg) {
		t.Fatal("did not detect settings")
	}
	policy, err := readRequestPolicyConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if policy.MinInterval != 5*time.Second || policy.MaxConcurrency != 1 ||
		policy.MaxAttempts != 2 || policy.MaxWait != 12*time.Second {
		t.Fatalf("unexpected parsed policy: %+v", policy)
	}
}

func TestReadRequestPolicyConfigRejectsMalformedDuration(t *testing.T) {
	cfg := envconfig.NewConfig()
	cfg.Set("MIN_INTERVAL", "not-a-duration")
	if _, err := readRequestPolicyConfig(cfg); err == nil {
		t.Fatal("accepted malformed interval")
	}
}
