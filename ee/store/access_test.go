package store

import (
	"context"
	"os"
	"testing"
	"time"

	cestore "github.com/zybuu-ai/abhed/store"
)

// Integration test against a real Postgres, skipped without ABHED_TEST_DSN
// like the Community store's own. It pins the two things this package
// promises on top of the event store: the tables are created by this
// package's migration, versioned apart from the event store's, and a grant
// recorded under one tenant is invisible under another.
func TestAccessRecordsAreTenantScopedAndSelfMigrated(t *testing.T) {
	dsn := os.Getenv("ABHED_TEST_DSN")
	if dsn == "" {
		t.Skip("set ABHED_TEST_DSN to run store integration tests")
	}
	ctx := context.Background()
	open := func(tenant string) *Access {
		cfg := cestore.DefaultConfig(dsn)
		cfg.Tenant = tenant
		pg, err := cestore.Open(ctx, cfg)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		t.Cleanup(pg.Close)
		return NewAccess(pg)
	}
	mine, theirs := open("t-access-a"), open("t-access-b")

	g, err := mine.RecordRequest(ctx, Grant{Email: "A@Example.com", Name: "Ada"})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := mine.GrantAccess(ctx, g.ID, "admin", "code-1", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("grant: %v", err)
	}
	got, err := mine.GrantByID(ctx, g.ID)
	if err != nil || !got.Active() || got.Email != "a@example.com" {
		t.Errorf("grant readback: %+v err=%v", got, err)
	}
	if _, err := theirs.GrantByID(ctx, g.ID); err == nil {
		t.Error("another tenant read the grant — row-level security is not applied to access_grants")
	}

	var n int
	if err := mine.pool.QueryRow(ctx,
		`SELECT count(*) FROM access_schema_version`).Scan(&n); err != nil || n == 0 {
		t.Errorf("access schema has no version row of its own: n=%d err=%v", n, err)
	}
}
