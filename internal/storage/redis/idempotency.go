package redis

import (
	"context"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// SetNXIfNotExists 使用 Redis SETNX 实现幂等（设计文档 11.5 / 12.3）。
// 返回 true 表示首次设置（应继续处理），false 表示键已存在（应跳过重复处理）。
func SetNXIfNotExists(ctx context.Context, client *goredis.Client, key, value string, ttl time.Duration) (bool, error) {
	ok, err := client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}
