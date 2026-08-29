package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"
)

func TestSetNXIfNotExists(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run() error = %v", err)
	}
	defer mr.Close()

	client := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	defer client.Close()

	ctx := context.Background()

	ok, err := SetNXIfNotExists(ctx, client, "gooncall:event:evt_1", "1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("first SetNX = (%v, %v), want (true, nil)", ok, err)
	}

	ok, err = SetNXIfNotExists(ctx, client, "gooncall:event:evt_1", "1", time.Hour)
	if err != nil || ok {
		t.Fatalf("second SetNX = (%v, %v), want (false, nil)", ok, err)
	}

	// 不同 key 不受影响
	ok, _ = SetNXIfNotExists(ctx, client, "gooncall:event:evt_2", "1", time.Hour)
	if !ok {
		t.Fatal("different key should succeed")
	}
}
