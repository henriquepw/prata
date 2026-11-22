package main

import (
	"net"
	"path/filepath"
	"testing"
	"time"
)

func TestRun_ExitsWhenServingFailsWithTheFetchJobOn(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()

	env := []string{"PRATA_DB_PATH=" + filepath.Join(t.TempDir(), "prata.db"), "PRATA_ADDR=" + busy.Addr().String(), "PRATA_ENCRYPTION_KEY=key"}
	code := make(chan int, 1)
	go func() { code <- run(nil, env) }()

	select {
	case got := <-code:
		if got != 1 {
			t.Errorf("run = %d, want 1", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return: it waits for the fetch job before cancelling it")
	}
}
