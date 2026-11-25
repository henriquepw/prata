package main

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/henriquepw/prata/server/pkg/id"
)

// seededOps is how many ops the first account's signup leaves in the oplog: its initial categories.
const seededOps = 4

const (
	categoryID    = "01JXQ8Z7000000000000000000"
	transactionID = "01JXQ8Z8000000000000000000"
)

type op struct {
	ServerSeq       int64          `json:"server_seq,omitempty"`
	ID              string         `json:"id"`
	Entity          string         `json:"entity"`
	EntityID        string         `json:"entity_id"`
	Kind            string         `json:"kind"`
	Payload         map[string]any `json:"payload"`
	ProtocolVersion int            `json:"protocol_version"`
	DeviceID        string         `json:"device_id"`
	OwnerID         string         `json:"owner_id"`
	CreatedAt       string         `json:"created_at"`
}

type pushResult struct {
	Acks []struct {
		ID        string `json:"id"`
		ServerSeq int64  `json:"server_seq"`
	} `json:"acks"`
	Rejected []struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	} `json:"rejected"`
}

type pullResult struct {
	Ops        []op  `json:"ops"`
	NextCursor int64 `json:"next_cursor"`
	UpToDate   bool  `json:"up_to_date"`
}

type client struct {
	t        *testing.T
	handler  http.Handler
	key      string
	deviceID string
	ownerID  string
}

func newClient(t *testing.T, handler http.Handler, body signup) *client {
	return &client{t: t, handler: handler, key: signupKey(t, handler, body), deviceID: body.DeviceID, ownerID: body.OwnerID}
}

// op builds an op from this device with a fresh ULID.
func (c *client) op(entity, entityID, kind string, payload map[string]any) op {
	return op{ID: id.New().String(), Entity: entity, EntityID: entityID, Kind: kind, Payload: payload, ProtocolVersion: 1,
		DeviceID: c.deviceID, OwnerID: c.ownerID, CreatedAt: "2026-08-23T14:02:11Z"}
}

func (c *client) do(req *http.Request) *httptest.ResponseRecorder {
	req.Header.Set("Authorization", "Bearer "+c.key)
	response := httptest.NewRecorder()
	c.handler.ServeHTTP(response, req)

	return response
}

func (c *client) pushAs(deviceID string, ops ...op) *httptest.ResponseRecorder {
	c.t.Helper()
	payload, err := json.Marshal(map[string]any{"device_id": deviceID, "protocol_version": 1, "ops": ops})
	if err != nil {
		c.t.Fatal(err)
	}

	return c.do(httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(payload)))
}

func (c *client) push(ops ...op) pushResult {
	c.t.Helper()
	response := c.pushAs(c.deviceID, ops...)
	if response.Code != http.StatusOK {
		c.t.Fatalf("push status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var result pushResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.t.Fatal(err)
	}

	return result
}

func (c *client) pullAs(deviceID string, cursor int64, limit int) *httptest.ResponseRecorder {
	query := url.Values{"device_id": {deviceID}, "cursor": {strconv.FormatInt(cursor, 10)}, "limit": {strconv.Itoa(limit)}}

	return c.do(httptest.NewRequest(http.MethodGet, "/api/sync/pull?"+query.Encode(), nil))
}

func (c *client) pull(cursor int64, limit int) pullResult {
	c.t.Helper()
	response := c.pullAs(c.deviceID, cursor, limit)
	if response.Code != http.StatusOK {
		c.t.Fatalf("pull status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var result pullResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.t.Fatal(err)
	}

	return result
}

func newCategory(c *client) op {
	return c.op("CATEGORY", categoryID, "CREATE", map[string]any{
		"name": "Mercado", "color": "GREEN", "monthly_limit": 50000,
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

func newTransaction(c *client) op {
	return c.op("TRANSACTION", transactionID, "CREATE", map[string]any{
		"category_id": categoryID, "kind": "OUTCOME", "amount": 4590, "occurred_at": "2026-08-23",
		"created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})
}

func TestPush_AcksOpsThatComeBackInPull(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	transaction, category := newTransaction(c), newCategory(c)

	pushed := c.push(transaction, category)
	if len(pushed.Rejected) != 0 || len(pushed.Acks) != 2 {
		t.Fatalf("push = %+v, want two acks (transaction before its category is fine)", pushed)
	}
	if pushed.Acks[0].ID != transaction.ID || pushed.Acks[1].ID != category.ID || pushed.Acks[0].ServerSeq >= pushed.Acks[1].ServerSeq {
		t.Fatalf("acks = %+v, want in push order with increasing server_seq", pushed.Acks)
	}

	pulled := c.pull(seededOps, 500)
	if !pulled.UpToDate || pulled.NextCursor != pushed.Acks[1].ServerSeq || len(pulled.Ops) != 2 {
		t.Fatalf("pull = %+v, want both ops, up to date at the last seq", pulled)
	}
	if got := pulled.Ops[0]; got.ID != transaction.ID || got.ServerSeq != pushed.Acks[0].ServerSeq || got.Payload["amount"] != float64(4590) {
		t.Errorf("first pulled op = %+v, want the transaction with its server_seq", got)
	}
}

func TestPush_ResendReturnsSameSeqWithoutDuplicating(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	category := newCategory(c)
	first := c.push(category)

	again := c.push(category)
	if len(again.Acks) != 1 || again.Acks[0].ServerSeq != first.Acks[0].ServerSeq {
		t.Fatalf("resend acks = %+v, want server_seq %d", again.Acks, first.Acks[0].ServerSeq)
	}
	if got := len(c.pull(seededOps, 500).Ops); got != 1 {
		t.Errorf("pulled %d ops, want 1", got)
	}
}

func TestPush_RejectsOpWithoutDroppingBatch(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	c.push(newCategory(c))
	tests := []struct {
		name   string
		op     func() op
		reason string
	}{
		{name: "unknown protocol", reason: "UNSUPPORTED_PROTOCOL", op: func() op {
			o := newCategory(c)
			o.ProtocolVersion = 99
			return o
		}},
		{name: "owner is not the key's user", reason: "NOT_OWNER", op: func() op {
			o := c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "Feira"})
			o.OwnerID = "01JXQ9Z5000000000000000000"
			return o
		}},
		{name: "unknown entity", reason: "INVALID_OPERATION", op: func() op {
			return c.op("BUDGET", categoryID, "CREATE", map[string]any{"name": "x"})
		}},
		{name: "wrong field type", reason: "INVALID_OPERATION", op: func() op {
			return c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"monthly_limit": "lots"})
		}},
		{name: "value outside check", reason: "INVALID_OPERATION", op: func() op {
			return c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"color": "PINK"})
		}},
		{name: "create missing required field", reason: "INVALID_OPERATION", op: func() op {
			return c.op("TAG", "01JXQ8ZA000000000000000000", "CREATE", map[string]any{"name": "Viagem"})
		}},
		{name: "created_at not RFC3339", reason: "INVALID_OPERATION", op: func() op {
			o := c.op("CATEGORY", categoryID, "DELETE", nil)
			o.CreatedAt = "yesterday"
			return o
		}},
		{name: "update of missing row", reason: "INVALID_OPERATION", op: func() op {
			return c.op("TAG", "01JXQ8ZA000000000000000000", "UPDATE", map[string]any{"name": "Viagem"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bad, good := tt.op(), c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "Feira"})

			pushed := c.push(bad, good)
			if len(pushed.Rejected) != 1 || pushed.Rejected[0].ID != bad.ID || pushed.Rejected[0].Reason != tt.reason {
				t.Errorf("rejected = %+v, want %s for %s", pushed.Rejected, tt.reason, bad.ID)
			}
			if len(pushed.Acks) != 1 || pushed.Acks[0].ID != good.ID {
				t.Errorf("acks = %+v, want the good op %s", pushed.Acks, good.ID)
			}
		})
	}
}

func TestPush_RejectsOnlyOpWithWrongEnvelopeType(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	first, second := newCategory(c), c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "Feira"})
	bad := c.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "x"})
	badJSON, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(badJSON, &raw); err != nil {
		t.Fatal(err)
	}

	raw["protocol_version"] = "1"
	payload, err := json.Marshal(map[string]any{"device_id": c.deviceID, "protocol_version": 1, "ops": []any{first, raw, second}})
	if err != nil {
		t.Fatal(err)
	}

	response := c.do(httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(payload)))
	var pushed pushResult
	if err := json.NewDecoder(response.Body).Decode(&pushed); err != nil || response.Code != http.StatusOK {
		t.Fatalf("push = %d, %v; want 200", response.Code, err)
	}

	if len(pushed.Rejected) != 1 || pushed.Rejected[0].ID != bad.ID || pushed.Rejected[0].Reason != "INVALID_OPERATION" {
		t.Errorf("rejected = %+v, want INVALID_OPERATION for %s", pushed.Rejected, bad.ID)
	}

	if len(pushed.Acks) != 2 || pushed.Acks[0].ID != first.ID || pushed.Acks[1].ID != second.ID || pushed.Acks[0].ServerSeq >= pushed.Acks[1].ServerSeq {
		t.Errorf("acks = %+v, want %s then %s", pushed.Acks, first.ID, second.ID)
	}
}

func TestPull_PreservesUnknownPayloadFields(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	category := newCategory(c)
	category.Payload["icon"] = map[string]any{"name": "cart"}
	c.push(category)

	got := c.pull(seededOps, 500).Ops[0].Payload["icon"]
	if icon, ok := got.(map[string]any); !ok || icon["name"] != "cart" {
		t.Errorf("pulled icon = %v, want the original field", got)
	}
}

func TestPull_PagesByLimitUntilUpToDate(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	c.push(newCategory(c), newTransaction(c), c.op("CATEGORY", categoryID, "DELETE", nil))

	var seqs []int64
	cursor, pages := int64(seededOps), 0
	for {
		page := c.pull(cursor, 2)
		pages++
		for _, o := range page.Ops {
			seqs = append(seqs, o.ServerSeq)
		}

		cursor = page.NextCursor
		if page.UpToDate {
			break
		}
	}

	// the category's delete is preceded by the move of its limit and of its transaction to the default category
	if pages != 3 || len(seqs) != 5 || !slices.IsSorted(seqs) || cursor != seqs[4] {
		t.Errorf("pulled seqs %v in %d pages ending at %d, want 5 increasing seqs in 3 pages", seqs, pages, cursor)
	}
	if page := c.pull(cursor, 2); len(page.Ops) != 0 || !page.UpToDate || page.NextCursor != cursor {
		t.Errorf("pull at the end = %+v, want empty and up to date at %d", page, cursor)
	}
}

func TestSync_RejectsDeviceIDOtherThanTheKeys(t *testing.T) {
	c := newClient(t, newHandler(openStore(t), config{}), newSignup())
	other := "01JXQ9Z6000000000000000000"

	if got := c.pushAs(other, newCategory(c)).Code; got != http.StatusBadRequest {
		t.Errorf("push as other device status = %d, want %d", got, http.StatusBadRequest)
	}
	if got := c.pullAs(other, 0, 500).Code; got != http.StatusBadRequest {
		t.Errorf("pull as other device status = %d, want %d", got, http.StatusBadRequest)
	}

	o := newCategory(c)
	o.DeviceID = other
	if pushed := c.push(o); len(pushed.Rejected) != 1 || pushed.Rejected[0].Reason != "INVALID_OPERATION" {
		t.Errorf("op from other device = %+v, want INVALID_OPERATION", pushed)
	}
}

func TestSync_IsolatesAccounts(t *testing.T) {
	handler := newHandler(openStore(t), config{isSignupAllowed: true})
	ana := newClient(t, handler, newSignup())
	bia := newClient(t, handler, signup{
		Email: "bia@example.com", Password: "another secret",
		AccountID: "01JXQ9Z4000000000000000000", OwnerID: "01JXQ9Z5000000000000000000", DeviceID: "01JXQ9Z6000000000000000000",
	})
	ana.push(newCategory(ana))

	if page := bia.pull(0, 500); len(page.Ops) != seededOps || slices.ContainsFunc(page.Ops, func(o op) bool { return o.EntityID == categoryID }) {
		t.Errorf("bia pulled %+v, want only her own initial categories", page.Ops)
	}
	if pushed := bia.push(bia.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "Roubada"})); len(pushed.Rejected) != 1 || pushed.Rejected[0].Reason != "NOT_OWNER" {
		t.Errorf("bia updating ana's row = %+v, want NOT_OWNER", pushed)
	}
	if pushed := bia.push(newCategory(bia)); len(pushed.Rejected) != 1 || pushed.Rejected[0].Reason != "NOT_OWNER" {
		t.Errorf("bia creating over ana's row id = %+v, want NOT_OWNER", pushed)
	}
}

// device is a minimal offline-first client: base holds server state applied in seq order,
// pending holds local ops not yet acked, and state rebases pending on top of base.
type device struct {
	*client
	cursor  int64
	base    map[string]map[string]any
	pending []op
}

func apply(rows map[string]map[string]any, o op) {
	key := o.Entity + "/" + o.EntityID
	switch o.Kind {
	case "CREATE":
		rows[key] = maps.Clone(o.Payload)
	case "UPDATE":
		if row, ok := rows[key]; ok {
			maps.Copy(row, o.Payload)
		}
	case "DELETE":
		if row, ok := rows[key]; ok {
			row["deleted_at"] = o.CreatedAt
		}
	}
}

func (d *device) mutate(o op) {
	d.pending = append(d.pending, o)
}

func (d *device) state() map[string]map[string]any {
	rows := map[string]map[string]any{}
	for key, row := range d.base {
		rows[key] = maps.Clone(row)
	}

	for _, o := range d.pending {
		apply(rows, o)
	}

	return rows
}

func (d *device) pullAll() {
	for {
		page := d.pull(d.cursor, 2)
		for _, o := range page.Ops {
			apply(d.base, o)
		}

		d.cursor = page.NextCursor
		if page.UpToDate {
			return
		}
	}
}

// sync pulls, pushes pending ops (acked and rejected ones both leave pending) and pulls the acked ones back.
func (d *device) sync() {
	d.pullAll()
	if len(d.pending) > 0 {
		d.push(d.pending...)
		d.pending = nil
	}

	d.pullAll()
}

// signinClient signs a second device into c's account and returns it.
func signinClient(t *testing.T, c *client, deviceID string) *client {
	t.Helper()
	body := newSignin()
	body.DeviceID = deviceID
	got := signinAs(t, c.handler, body)

	return &client{t: t, handler: c.handler, key: got.APIKey, deviceID: deviceID, ownerID: got.OwnerID}
}

type snapshotResult struct {
	Categories   []map[string]any `json:"categories"`
	Tags         []map[string]any `json:"tags"`
	Transactions []map[string]any `json:"transactions"`
	Recurrences  []map[string]any `json:"recurrences"`
	Cursor       int64            `json:"cursor"`
}

func (c *client) snapshotAs(deviceID string) *httptest.ResponseRecorder {
	return c.do(httptest.NewRequest(http.MethodGet, "/api/sync/snapshot?device_id="+deviceID, nil))
}

func (c *client) snapshot() snapshotResult {
	c.t.Helper()
	response := c.snapshotAs(c.deviceID)
	if response.Code != http.StatusOK {
		c.t.Fatalf("snapshot status = %d, want %d: %s", response.Code, http.StatusOK, response.Body)
	}

	var result snapshotResult
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		c.t.Fatal(err)
	}

	return result
}

func TestSnapshot_ReturnsAccountStateWithTombstones(t *testing.T) {
	db := openStore(t)
	handler := newHandler(db, config{isSignupAllowed: true})
	laptop := newClient(t, handler, newSignup())
	pushed := laptop.push(newCategory(laptop), newTransaction(laptop), laptop.op("CATEGORY", categoryID, "DELETE", nil))
	bia := newClient(t, handler, signup{
		Email: "bia@example.com", Password: "another secret",
		AccountID: "01JXQ9Z4000000000000000000", OwnerID: "01JXQ9Z5000000000000000000", DeviceID: "01JXQ9Z6000000000000000000",
	})
	if tag := bia.push(bia.op("TAG", "01JXQ9ZA000000000000000000", "CREATE", map[string]any{
		"name": "Viagem", "start_at": "2026-08-01", "created_at": "2026-08-23T14:02:11Z", "updated_at": "2026-08-23T14:02:11Z",
	})); len(tag.Acks) != 1 {
		t.Fatalf("bia's tag push = %+v, want acked", tag)
	}

	phone := signinClient(t, laptop, "01JXQ8Z9000000000000000000")

	got := phone.snapshot()
	i := slices.IndexFunc(got.Categories, func(c map[string]any) bool { return c["id"] == categoryID })
	if len(got.Categories) != seededOps+1 || i < 0 || got.Categories[i]["deleted_at"] != "2026-08-23T14:02:11Z" {
		t.Errorf("categories = %v, want the initial ones and the deleted category as a tombstone", got.Categories)
	}
	if len(got.Transactions) != 1 || got.Transactions[0]["amount"] != float64(4590) {
		t.Errorf("transactions = %v, want the transaction", got.Transactions)
	}
	if got.Tags == nil || len(got.Tags) != 0 || got.Recurrences == nil || len(got.Recurrences) != 0 {
		t.Errorf("tags = %v, recurrences = %v, want empty arrays without bia's tag", got.Tags, got.Recurrences)
	}
	if got.Cursor != pushed.Acks[2].ServerSeq {
		t.Errorf("cursor = %d, want the account's max seq %d", got.Cursor, pushed.Acks[2].ServerSeq)
	}

	var cursor int64
	if err := db.GetContext(context.Background(), &cursor, "SELECT cursor FROM devices WHERE id = ?", phone.deviceID); err != nil {
		t.Fatal(err)
	}
	if cursor != 0 {
		t.Errorf("device cursor after snapshot = %d, want 0", cursor)
	}
	if page := phone.pull(got.Cursor, 500); len(page.Ops) != 0 || !page.UpToDate {
		t.Errorf("pull from snapshot cursor = %+v, want empty and up to date", page)
	}
	if code := phone.snapshotAs(laptop.deviceID).Code; code != http.StatusBadRequest {
		t.Errorf("snapshot as other device status = %d, want %d", code, http.StatusBadRequest)
	}
}

func TestSync_TwoDevicesConverge(t *testing.T) {
	laptop := newClient(t, newHandler(openStore(t), config{}), newSignup())
	a := &device{client: laptop, base: map[string]map[string]any{}}
	b := &device{client: signinClient(t, laptop, "01JXQ8Z9000000000000000000"), base: map[string]map[string]any{}}

	// Out of causal order: the transaction is pushed before its category.
	a.mutate(newTransaction(a.client))
	a.mutate(newCategory(a.client))
	a.sync()
	b.sync()

	// Same field edited on both sides, delete concurrent with update, and an unknown field from a newer client.
	lostAck := a.op("TRANSACTION", transactionID, "UPDATE", map[string]any{"amount": 100})
	a.mutate(lostAck)
	a.mutate(a.op("CATEGORY", categoryID, "DELETE", nil))
	b.mutate(b.op("TRANSACTION", transactionID, "UPDATE", map[string]any{"amount": 200, "note": "from a newer app"}))
	b.mutate(b.op("CATEGORY", categoryID, "UPDATE", map[string]any{"name": "Feira"}))
	a.sync()

	// b's ops are still pending while it pulls a's: they must be rebased on top, not overwritten.
	b.pullAll()
	if got := b.state()["TRANSACTION/"+transactionID]["amount"]; got != 200 {
		t.Fatalf("b's pending amount after pull = %v, want 200 rebased over a's 100", got)
	}
	b.sync()

	// The same op arriving twice must not reapply a's older amount over b's.
	a.push(lostAck)
	a.sync()
	b.sync()

	if !reflect.DeepEqual(a.state(), b.state()) {
		t.Fatalf("devices diverged:\na = %v\nb = %v", a.state(), b.state())
	}

	transaction, category := a.state()["TRANSACTION/"+transactionID], a.state()["CATEGORY/"+categoryID]
	if transaction["amount"] != float64(200) || transaction["note"] != "from a newer app" {
		t.Errorf("transaction = %v, want b's later amount and the unknown field", transaction)
	}
	if category["deleted_at"] == nil || category["name"] != "Feira" {
		t.Errorf("category = %v, want the update applied without resurrecting the tombstone", category)
	}
}
