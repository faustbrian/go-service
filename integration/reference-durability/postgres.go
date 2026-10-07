package referencedurability

import (
	"context"

	postgres "github.com/faustbrian/go-postgres/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Native parsing is an explicit fixture-owned acquisition. The disposable
// database scenario intentionally permits pgx environment and TLS file inputs.
func resolvePostgresDSN(ctx context.Context, dsn string) (*postgres.PoolConfig, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return pgxpool.ParseConfig(dsn)
}
