// Package redis owns shared Redis client initialization.
package redis

import (
	"context"
	"encoding/json"
	"strconv"

	goredis "github.com/redis/go-redis/v9"
	"go.uber.org/fx"
)

type Config struct {
	Addr     string `json:"addr" toml:"addr" yaml:"addr"`
	Password string `json:"password" toml:"password" yaml:"password"`
	DB       int    `json:"db" toml:"db" yaml:"db"`
}

func (c *Config) UnmarshalJSON(data []byte) error {
	var raw struct {
		Addr     string          `json:"addr"`
		Password string          `json:"password"`
		DB       json.RawMessage `json:"db"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	db, err := parseDB(raw.DB)
	if err != nil {
		return err
	}
	*c = Config{
		Addr:     raw.Addr,
		Password: raw.Password,
		DB:       db,
	}
	return nil
}

// NewClient constructs a Redis client and binds shutdown to the fx lifecycle.
func NewClient(lc fx.Lifecycle, cfg Config) *goredis.Client {
	client := goredis.NewClient(&goredis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
	})
	lc.Append(fx.Hook{OnStop: func(context.Context) error {
		return client.Close()
	}})
	return client
}

func parseDB(data json.RawMessage) (int, error) {
	if len(data) == 0 || string(data) == "null" {
		return 0, nil
	}

	var db int
	if err := json.Unmarshal(data, &db); err == nil {
		return db, nil
	}

	var dbString string
	if err := json.Unmarshal(data, &dbString); err != nil {
		return 0, err
	}
	if dbString == "" {
		return 0, nil
	}
	return strconv.Atoi(dbString)
}
