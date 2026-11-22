package pluggy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTransactions_StopsWhenTheCursorNeverEnds(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		fmt.Fprintf(w, `{"next": "?page=%d", "results": []}`, pages)
	}))
	defer server.Close()

	_, err := Client{URL: server.URL}.Transactions(context.Background(), "key", "acc", "2025-01-01")
	if err == nil {
		t.Fatal("err = nil, want an error for a cursor that never ends")
	}
}

func TestTransactions_StopsWhenTheCursorRepeats(t *testing.T) {
	pages := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		fmt.Fprint(w, `{"next": "?page=1", "results": []}`)
	}))
	defer server.Close()

	_, err := Client{URL: server.URL}.Transactions(context.Background(), "key", "acc", "2025-01-01")
	if err == nil || pages > 3 {
		t.Fatalf("err = %v after %d pages, want an error for a repeated cursor", err, pages)
	}
}
