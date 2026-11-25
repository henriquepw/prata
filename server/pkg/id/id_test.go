package id_test

import (
	"encoding/json"
	"testing"

	"github.com/henriquepw/prata/server/pkg/id"
)

func TestIDMarshaling(t *testing.T) {
	want := struct {
		ID   id.ID  `json:"id"`
		Name string `json:"name"`
	}{ID: id.New(), Name: "Jose"}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := want
	got.ID = ""
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	if _, err := json.Marshal(id.ID("not-a-ulid")); err == nil {
		t.Fatal("marshaling an invalid id should fail")
	}
}

func TestIDScan(t *testing.T) {
	valid := id.New()
	tests := []struct {
		name    string
		value   any
		want    id.ID
		wantErr bool
	}{
		{name: "null", value: nil, want: ""},
		{name: "id", value: valid, want: valid},
		{name: "string", value: string(valid), want: valid},
		{name: "bytes", value: []byte(valid), want: valid},
		{name: "invalid type", value: 123, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got id.ID
			err := got.Scan(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
