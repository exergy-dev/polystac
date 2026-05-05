package pgstac

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// poolConfig is the small subset of pgxpool knobs PolyStac surfaces via
// configuration. Anything else can be set in the DSN.
type poolConfig struct {
	DSN             string
	MinConns        int32
	MaxConns        int32
	MaxConnLifetime time.Duration
}

// configFromMap pulls poolConfig from the lowercase backend env subtree
// produced by internal/config (POLYSTAC_PG_* → keys without the prefix).
func configFromMap(env map[string]string) (poolConfig, error) {
	pc := poolConfig{
		DSN:             env["dsn"],
		MinConns:        2,
		MaxConns:        20,
		MaxConnLifetime: time.Hour,
	}
	if pc.DSN == "" {
		return pc, errors.New("pgstac: POLYSTAC_PG_DSN is required")
	}
	if v := env["pool_min"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return pc, fmt.Errorf("pgstac: pool_min %q: %w", v, err)
		}
		pc.MinConns = int32(n)
	}
	if v := env["pool_max"]; v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return pc, fmt.Errorf("pgstac: pool_max %q: %w", v, err)
		}
		pc.MaxConns = int32(n)
	}
	if v := env["pool_max_conn_lifetime"]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return pc, fmt.Errorf("pgstac: pool_max_conn_lifetime %q: %w", v, err)
		}
		pc.MaxConnLifetime = d
	}
	return pc, nil
}

// newPool constructs a pgxpool.Pool with the requested settings. The
// caller owns the returned pool and must Close() it on shutdown.
func newPool(ctx context.Context, pc poolConfig) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(pc.DSN)
	if err != nil {
		return nil, fmt.Errorf("pgstac: parse DSN: %w", err)
	}
	cfg.MinConns = pc.MinConns
	cfg.MaxConns = pc.MaxConns
	cfg.MaxConnLifetime = pc.MaxConnLifetime
	return pgxpool.NewWithConfig(ctx, cfg)
}
