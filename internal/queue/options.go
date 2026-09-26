package queue

import (
	"fmt"
	"strings"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

func AsynqRedisOptions(redisURL, master string, sentinelAddrs []string, sentinelPassword string) (asynq.RedisConnOpt, error) {
	if master != "" || len(sentinelAddrs) > 0 {
		if master == "" || len(sentinelAddrs) == 0 {
			return nil, fmt.Errorf("REDIS_SENTINEL_MASTER_NAME and REDIS_SENTINEL_ADDRS must be configured together")
		}
		return asynq.RedisFailoverClientOpt{MasterName: master, SentinelAddrs: sentinelAddrs, SentinelPassword: sentinelPassword}, nil
	}
	return asynq.ParseRedisURI(redisURL)
}

func RedisOptions(redisURL, master string, sentinelAddrs []string, sentinelPassword string) (*redis.UniversalOptions, error) {
	if master != "" || len(sentinelAddrs) > 0 {
		if master == "" || len(sentinelAddrs) == 0 {
			return nil, fmt.Errorf("REDIS_SENTINEL_MASTER_NAME and REDIS_SENTINEL_ADDRS must be configured together")
		}
		return &redis.UniversalOptions{MasterName: master, Addrs: sentinelAddrs, SentinelPassword: sentinelPassword, MaxRetries: 10}, nil
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &redis.UniversalOptions{Addrs: []string{opt.Addr}, Username: opt.Username, Password: opt.Password, DB: opt.DB, DialTimeout: opt.DialTimeout, ReadTimeout: opt.ReadTimeout, WriteTimeout: opt.WriteTimeout, MaxRetries: 10, PoolSize: opt.PoolSize}, nil
}

func SentinelConfigured(master string, addrs []string) bool {
	return master != "" && strings.TrimSpace(strings.Join(addrs, "")) != ""
}
