package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/sarkar-ai-taken/hoofprint/internal/store/storetest"
)

// TestConformance runs the shared store suite against a real database.
// Set HOOFPRINT_TEST_DATABASE_URL to run it, e.g.
//
//	docker run -d --rm -p 5432:5432 -e POSTGRES_PASSWORD=pw postgres:16
//	HOOFPRINT_TEST_DATABASE_URL=postgres://postgres:pw@localhost:5432/postgres go test ./internal/store/postgres
func TestConformance(t *testing.T) {
	dsn := os.Getenv("HOOFPRINT_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("HOOFPRINT_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = s.pool.Exec(ctx, `DROP TABLE IF EXISTS evidence, verdicts, deploys, agents, schema_migrations`)
		_ = s.Close()
	})
	_, _ = s.pool.Exec(ctx, `DROP TABLE IF EXISTS evidence, verdicts, deploys, agents, schema_migrations`)
	if err := migrate(ctx, s.pool); err != nil {
		t.Fatal(err)
	}
	storetest.Run(t, s)
}
