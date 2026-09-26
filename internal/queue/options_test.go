package queue

import (
	"testing"

	"github.com/hibiken/asynq"
)

func TestAsynqRedisOptionsUsesSentinelWhenConfigured(t *testing.T) {
	opt, err := AsynqRedisOptions("redis://ignored:6379", "mymaster", []string{"sentinel-1:26379", "sentinel-2:26379"}, "secret")
	if err != nil {
		t.Fatal(err)
	}
	failover, ok := opt.(asynq.RedisFailoverClientOpt)
	if !ok {
		t.Fatalf("expected failover options, got %T", opt)
	}
	if failover.MasterName != "mymaster" || len(failover.SentinelAddrs) != 2 || failover.SentinelPassword != "secret" {
		t.Fatalf("unexpected failover options: %#v", failover)
	}
}

func TestAsynqRedisOptionsRejectsPartialSentinelConfig(t *testing.T) {
	if _, err := AsynqRedisOptions("redis://localhost:6379", "mymaster", nil, ""); err == nil {
		t.Fatal("expected partial Sentinel configuration to fail")
	}
}
