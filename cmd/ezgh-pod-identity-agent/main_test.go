package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestListenRetriesUntilCancelled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	// TEST-NET-1 is never on this machine: the bind fails like 169.254.170.23 before ipamd adds it.
	if ln, err := listen(ctx, "192.0.2.1:0", quiet); err == nil {
		ln.Close()
		t.Fatal("want no listener")
	}
}

func TestServeAnswersAndDrains(t *testing.T) {
	probe, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	probe.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, addr, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }), quiet)
	}()
	var resp *http.Response
	for range 50 {
		if resp, err = http.Get("http://" + addr + "/v1/credentials"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("%v %v", resp, err)
	}
	resp.Body.Close()
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
