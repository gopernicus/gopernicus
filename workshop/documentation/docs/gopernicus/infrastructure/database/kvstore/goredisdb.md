---
sidebar_position: 1
title: goredisdb
---

# kvstore/goredisdb

Redis via [go-redis/v9](https://github.com/redis/go-redis). Returns `*redis.Client` directly — `Client` is a type alias. Used by `rediscache`, `goredisbus`, and any store that needs Redis operations.

```go
client, err := goredisdb.New(goredisdb.Options{
    Addr:     cfg.RedisAddr,
    Password: cfg.RedisPassword,
    PoolSize: 10,
})
```

Config via environment:

```go
var cfg goredisdb.Options
environment.ParseEnvTags("MYAPP", &cfg)
// reads MYAPP_REDIS_ADDR, MYAPP_REDIS_PASSWORD, etc.
```

Add hooks for tracing:

```go
client, err := goredisdb.New(cfg, goredisdb.WithHook(myOtelHook))
```
