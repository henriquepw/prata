package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/oklog/ulid/v2"
)

// ProtocolVersion is the only sync protocol version this server understands.
const ProtocolVersion = 1

// Reason is why CommitOps rejected an op; the value is the wire reason.
type Reason string

// Rejection reasons shared by push and web writes.
const (
	ReasonInvalidOperation    Reason = "INVALID_OPERATION"
	ReasonNotOwner            Reason = "NOT_OWNER"
	ReasonUnsupportedProtocol Reason = "UNSUPPORTED_PROTOCOL"
)

// Op is one oplog operation in wire format. Payload keeps the client's original JSON, unknown fields included.
type Op struct {
	Seq             int64           `db:"seq" json:"server_seq,omitempty"`
	ID              string          `db:"id" json:"id"`
	Entity          string          `db:"entity" json:"entity"`
	EntityID        string          `db:"entity_id" json:"entity_id"`
	Kind            string          `db:"kind" json:"kind"`
	Payload         json.RawMessage `db:"payload" json:"payload"`
	ProtocolVersion int             `db:"protocol_version" json:"protocol_version"`
	DeviceID        string          `db:"device_id" json:"device_id"`
	OwnerID         string          `db:"owner_id" json:"owner_id"`
	CreatedAt       string          `db:"created_at" json:"created_at"`
}

// Result is one op's outcome: Seq when accepted, Reason when rejected.
type Result struct {
	Seq    int64
	Reason Reason
}

// Page is one pull page of an account's oplog.
type Page struct {
	Ops        []Op  `json:"ops"`
	NextCursor int64 `json:"next_cursor"`
	UpToDate   bool  `json:"up_to_date"`
}

// table describes a domain table: its name and which payload fields it knows, true for INTEGER columns.
type table struct {
	name    string
	columns map[string]bool
}

var tables = map[string]table{
	"CATEGORY": {"categories", map[string]bool{
		"name": false, "color": false, "monthly_limit": true,
		"created_at": false, "updated_at": false, "deleted_at": false,
	}},
	"TAG": {"tags", map[string]bool{
		"name": false, "start_at": false, "end_at": false,
		"created_at": false, "updated_at": false, "deleted_at": false,
	}},
	"TRANSACTION": {"transactions", map[string]bool{
		"category_id": false, "tag_id": false, "kind": false, "amount": true, "description": false,
		"occurred_at": false, "recurrence_id": false, "occurrence_date": false,
		"created_at": false, "updated_at": false, "deleted_at": false,
	}},
	"RECURRENCE": {"recurrences", map[string]bool{
		"category_id": false, "kind": false, "amount": true, "description": false, "rule": false,
		"start_at": false, "end_at": false, "created_at": false, "updated_at": false, "deleted_at": false,
	}},
}

// CommitOps is the only write path into the domain tables. In one transaction it records each op
// in sync_operations and overlays it on its domain row. Each op is accepted or rejected on its own,
// in order; an op already recorded for (actor's device, op id) returns its original seq.
func (s *Store) CommitOps(ctx context.Context, actor Actor, ops []Op) ([]Result, error) {
	tx, err := s.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("starting commit: %w", err)
	}
	defer tx.Rollback()

	results := make([]Result, len(ops))
	for i, op := range ops {
		if results[i], err = commitOp(ctx, tx, actor, op); err != nil {
			return nil, err
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing ops: %w", err)
	}

	return results, nil
}

func commitOp(ctx context.Context, tx *sqlx.Tx, actor Actor, op Op) (Result, error) {
	var seq int64
	err := tx.GetContext(ctx, &seq, "SELECT seq FROM sync_operations WHERE device_id = ? AND id = ?", actor.DeviceID, op.ID)
	if err == nil {
		return Result{Seq: seq}, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, fmt.Errorf("looking up op %s: %w", op.ID, err)
	}

	values, reason := validate(actor, op)
	if reason == "" {
		reason, err = checkRow(ctx, tx, actor, op)
		if err != nil {
			return Result{}, err
		}
	}

	if reason != "" {
		return Result{Reason: reason}, nil
	}

	if _, err := tx.ExecContext(ctx, "SAVEPOINT op"); err != nil {
		return Result{}, fmt.Errorf("opening savepoint: %w", err)
	}

	result := Result{}
	result.Seq, err = record(ctx, tx, actor, op, values)
	if isConstraint(err) {
		result = Result{Reason: ReasonInvalidOperation}
		_, err = tx.ExecContext(ctx, "ROLLBACK TO op")
	}

	if err != nil {
		return Result{}, fmt.Errorf("recording op %s: %w", op.ID, err)
	}

	if _, err := tx.ExecContext(ctx, "RELEASE op"); err != nil {
		return Result{}, fmt.Errorf("releasing savepoint: %w", err)
	}

	return result, nil
}

// validate checks the op against the protocol and the actor, and returns the known payload fields as SQL values;
// unknown fields are skipped here and kept in the oplog. NOT NULL and CHECK constraints are left to SQLite
// and surface as INVALID_OPERATION.
func validate(actor Actor, op Op) (map[string]any, Reason) {
	if op.ProtocolVersion != ProtocolVersion {
		return nil, ReasonUnsupportedProtocol
	}

	if op.OwnerID != actor.UserID {
		return nil, ReasonNotOwner
	}

	table, ok := tables[op.Entity]
	if !ok || !isULID(op.ID) || !isULID(op.EntityID) || op.DeviceID != actor.DeviceID || op.CreatedAt == "" {
		return nil, ReasonInvalidOperation
	}

	switch op.Kind {
	case "DELETE":
		return nil, ""
	case "CREATE", "UPDATE":
	default:
		return nil, ReasonInvalidOperation
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(op.Payload, &payload); err != nil || payload == nil {
		return nil, ReasonInvalidOperation
	}

	values := map[string]any{}
	for field, raw := range payload {
		isInteger, known := table.columns[field]
		if !known {
			continue
		}

		value, err := decodeField(raw, isInteger)
		if err != nil {
			return nil, ReasonInvalidOperation
		}

		values[field] = value
	}

	return values, ""
}

// checkRow enforces ownership of an existing row, also across accounts, and requires one for UPDATE and DELETE.
func checkRow(ctx context.Context, tx *sqlx.Tx, actor Actor, op Op) (Reason, error) {
	var row struct {
		AccountID string `db:"account_id"`
		OwnerID   string `db:"owner_id"`
	}
	err := tx.GetContext(ctx, &row, "SELECT account_id, owner_id FROM "+tables[op.Entity].name+" WHERE id = ?", op.EntityID)
	switch {
	case errors.Is(err, sql.ErrNoRows) && op.Kind != "CREATE":
		return ReasonInvalidOperation, nil
	case errors.Is(err, sql.ErrNoRows):
		return "", nil
	case err != nil:
		return "", fmt.Errorf("reading %s %s: %w", op.Entity, op.EntityID, err)
	case row.AccountID != actor.AccountID || row.OwnerID != op.OwnerID:
		return ReasonNotOwner, nil
	}

	return "", nil
}

// record appends the op to the oplog and overlays it: CREATE writes the whole row, UPDATE the given fields,
// DELETE sets deleted_at to the op's created_at.
func record(ctx context.Context, tx *sqlx.Tx, actor Actor, op Op, values map[string]any) (int64, error) {
	payload := op.Payload
	if op.Kind == "DELETE" || len(payload) == 0 {
		payload = json.RawMessage("{}")
	}

	var seq int64
	err := tx.GetContext(ctx, &seq, `INSERT INTO sync_operations
		(id, account_id, device_id, owner_id, entity, entity_id, kind, payload, protocol_version, created_at, received_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING seq`,
		op.ID, actor.AccountID, actor.DeviceID, op.OwnerID, op.Entity, op.EntityID, op.Kind, string(payload),
		op.ProtocolVersion, op.CreatedAt, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}

	name := tables[op.Entity].name
	if op.Kind == "DELETE" {
		values = map[string]any{"deleted_at": op.CreatedAt}
	}
	values["server_seq"] = seq
	columns := slices.Sorted(maps.Keys(values))
	args := make([]any, len(columns))
	sets := make([]string, len(columns))
	for i, column := range columns {
		args[i] = values[column]
		sets[i] = column + " = excluded." + column
	}

	if op.Kind == "CREATE" {
		_, err = tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (id, owner_id, account_id, %s) VALUES (?, ?, ?%s) ON CONFLICT (id) DO UPDATE SET %s",
			name, strings.Join(columns, ", "), strings.Repeat(", ?", len(columns)), strings.Join(sets, ", ")),
			append([]any{op.EntityID, op.OwnerID, actor.AccountID}, args...)...)
	} else {
		_, err = tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s = ? WHERE id = ?", name, strings.Join(columns, " = ?, ")),
			append(args, op.EntityID)...)
	}

	return seq, err
}

// Pull records cursor and last contact on the actor's device, then returns up to limit of the account's ops after cursor.
func (s *Store) Pull(ctx context.Context, actor Actor, cursor int64, limit int) (Page, error) {
	_, err := s.ExecContext(ctx, "UPDATE devices SET cursor = ?, last_seen_at = ? WHERE id = ?",
		cursor, time.Now().UTC().Format(time.RFC3339), actor.DeviceID)
	if err != nil {
		return Page{}, fmt.Errorf("recording cursor: %w", err)
	}

	// CAST to BLOB: database/sql scans a []byte, not a TEXT string, into json.RawMessage.
	ops := []Op{}
	err = s.SelectContext(ctx, &ops, `SELECT seq, id, entity, entity_id, kind, CAST(payload AS BLOB) AS payload, protocol_version, device_id, owner_id, created_at
		FROM sync_operations WHERE account_id = ? AND seq > ? ORDER BY seq LIMIT ?`, actor.AccountID, cursor, limit+1)
	if err != nil {
		return Page{}, fmt.Errorf("reading ops: %w", err)
	}

	page := Page{Ops: ops[:min(len(ops), limit)], NextCursor: cursor, UpToDate: len(ops) <= limit}
	if len(page.Ops) > 0 {
		page.NextCursor = page.Ops[len(page.Ops)-1].Seq
	}

	return page, nil
}

// decodeField accepts null, or an integer for INTEGER columns and a string otherwise.
func decodeField(raw json.RawMessage, isInteger bool) (any, error) {
	if isInteger {
		var n *int64
		err := json.Unmarshal(raw, &n)

		return n, err
	}

	var s *string
	err := json.Unmarshal(raw, &s)

	return s, err
}

func isULID(id string) bool {
	_, err := ulid.ParseStrict(id)

	return err == nil
}
