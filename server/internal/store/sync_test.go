package store_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/henriquepw/prata/server/internal/store"
	_ "modernc.org/sqlite"
)

func newActor(t *testing.T) (*store.Store, store.Actor) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "prata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	key, err := db.CreateAccount(ctx, store.Signup{
		Email: "ana@example.com", Password: "secret",
		AccountID: "01JXQ8Z4000000000000000000", OwnerID: "01JXQ8Z5000000000000000000", DeviceID: "01JXQ8Z6000000000000000000",
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	actor, err := db.ActorByAPIKey(ctx, key)
	if err != nil {
		t.Fatal(err)
	}

	return db, actor
}

func commit(t *testing.T, db *store.Store, actor store.Actor, id, kind, payload string) {
	t.Helper()
	results, err := db.CommitOps(context.Background(), actor, []store.Op{{
		ID: id, Entity: "CATEGORY", EntityID: "01JXQ8Z7000000000000000000", Kind: kind, Payload: json.RawMessage(payload),
		ProtocolVersion: store.ProtocolVersion, DeviceID: actor.DeviceID, OwnerID: actor.UserID, CreatedAt: "2026-08-24T10:00:00Z",
	}})
	if err != nil || results[0].Reason != "" {
		t.Fatalf("commit %s = %+v, %v; want accepted", kind, results, err)
	}
}

func TestCommitOps_OverlaysDomainRow(t *testing.T) {
	db, actor := newActor(t)
	type category struct {
		Name         string  `db:"name"`
		Color        string  `db:"color"`
		MonthlyLimit int64   `db:"monthly_limit"`
		AccountID    string  `db:"account_id"`
		DeletedAt    *string `db:"deleted_at"`
		ServerSeq    int64   `db:"server_seq"`
	}
	read := func() category {
		var row category
		if err := db.Get(&row, "SELECT name, color, monthly_limit, account_id, deleted_at, server_seq FROM categories"); err != nil {
			t.Fatal(err)
		}

		return row
	}

	commit(t, db, actor, "01JXQ8ZB000000000000000001", "CREATE",
		`{"name":"Mercado","color":"GREEN","monthly_limit":500,"created_at":"2026-08-23","updated_at":"2026-08-23","account_id":"forged"}`)
	if got := read(); got.Name != "Mercado" || got.MonthlyLimit != 500 || got.AccountID != actor.AccountID || got.DeletedAt != nil {
		t.Fatalf("after CREATE row = %+v, want the whole row in the actor's account", got)
	}

	commit(t, db, actor, "01JXQ8ZB000000000000000002", "UPDATE", `{"name":"Feira"}`)
	if got := read(); got.Name != "Feira" || got.Color != "GREEN" || got.MonthlyLimit != 500 {
		t.Fatalf("after UPDATE row = %+v, want only name changed", got)
	}

	commit(t, db, actor, "01JXQ8ZB000000000000000003", "DELETE", ``)
	if got := read(); got.DeletedAt == nil || *got.DeletedAt != "2026-08-24T10:00:00Z" || got.Name != "Feira" || got.ServerSeq != 3 {
		t.Errorf("after DELETE row = %+v, want a tombstone at the op's created_at with server_seq 3", got)
	}
}

func TestPull_RecordsRequestedCursorAndLastSeen(t *testing.T) {
	db, actor := newActor(t)
	commit(t, db, actor, "01JXQ8ZB000000000000000001", "CREATE",
		`{"name":"Mercado","color":"GREEN","created_at":"2026-08-23","updated_at":"2026-08-23"}`)

	page, err := db.Pull(context.Background(), actor, 0, 500)
	if err != nil {
		t.Fatal(err)
	}

	var device struct {
		Cursor     int64   `db:"cursor"`
		LastSeenAt *string `db:"last_seen_at"`
	}
	if err := db.Get(&device, "SELECT cursor, last_seen_at FROM devices WHERE id = ?", actor.DeviceID); err != nil {
		t.Fatal(err)
	}

	if page.NextCursor != 1 || device.Cursor != 0 || device.LastSeenAt == nil {
		t.Errorf("device = %+v after pull to %d, want cursor 0 (only the next request confirms) and last_seen_at set", device, page.NextCursor)
	}
}
