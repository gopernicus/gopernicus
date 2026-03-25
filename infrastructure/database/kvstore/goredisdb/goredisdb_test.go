package goredisdb

import (
	"testing"
)

func TestIsCacheReadOp(t *testing.T) {
	tests := []struct {
		operation string
		expected  bool
	}{
		// Cache read operations - should return true
		{"GET", true},
		{"MGET", true},
		{"HGET", true},
		{"HGETALL", true},
		{"HMGET", true},

		// Write operations - should return false
		{"SET", false},
		{"MSET", false},
		{"HSET", false},
		{"DEL", false},
		{"EXPIRE", false},
		{"INCR", false},
		{"LPUSH", false},
		{"RPUSH", false},

		// EXISTS returns 0/1, not redis.Nil - should return false
		{"EXISTS", false},

		// Other read-like operations that don't return redis.Nil on miss
		{"KEYS", false},
		{"SCAN", false},
		{"LRANGE", false},
		{"SMEMBERS", false},

		// Edge cases
		{"", false},
		{"get", false},  // Case sensitive - operation should be uppercase
		{"Get", false},
		{"PING", false},
	}

	for _, tt := range tests {
		t.Run(tt.operation, func(t *testing.T) {
			result := isCacheReadOp(tt.operation)
			if result != tt.expected {
				t.Errorf("isCacheReadOp(%q) = %v, want %v", tt.operation, result, tt.expected)
			}
		})
	}
}
