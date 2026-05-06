package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

func TestNotifyRepoSyncViaGateway_Live(t *testing.T) {
	repoID := "github:acme/service"
	wantPath := "/api/v1/repos/github:acme/service/sync"
	token := "test-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method: got %s want POST", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Fatalf("path: got %s want %s", r.URL.Path, wantPath)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Fatalf("auth header: got %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portStr, ok := strings.Cut(u.Host, ":")
	if !ok {
		t.Fatalf("split host/port failed for %q", u.Host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := &config.Config{}
	cfg.Gateway.Host = host
	cfg.Gateway.Port = port
	cfg.Gateway.Token = token

	mode, err := notifyRepoSyncViaGateway(cfg, repoID)
	if err != nil {
		t.Fatalf("notify error: %v", err)
	}
	if mode != "live" {
		t.Fatalf("mode: got %q want live", mode)
	}
}

func TestNotifyRepoSyncViaGateway_NotFoundFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	u, _ := url.Parse(srv.URL)
	host, portStr, _ := strings.Cut(u.Host, ":")
	port, _ := strconv.Atoi(portStr)

	cfg := &config.Config{}
	cfg.Gateway.Host = host
	cfg.Gateway.Port = port

	mode, err := notifyRepoSyncViaGateway(cfg, "github:acme/service")
	if err != nil {
		t.Fatalf("notify error: %v", err)
	}
	if mode != "fallback" {
		t.Fatalf("mode: got %q want fallback", mode)
	}
}

func TestNotifyRepoSyncViaGateway_PrefersLoopbackUnderFunnelPublicURL(t *testing.T) {
	repoID := "github:acme/service"
	wantPath := "/api/v1/repos/github:acme/service/sync"
	var gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Fatalf("method: got %s want POST", r.Method)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, portStr, ok := strings.Cut(u.Host, ":")
	if !ok {
		t.Fatalf("split host/port failed for %q", u.Host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	cfg := &config.Config{}
	cfg.Gateway.Port = port
	cfg.Gateway.Host = "machine.example.ts.net"
	cfg.Gateway.Tailscale.Mode = "funnel"

	mode, err := notifyRepoSyncViaGateway(cfg, repoID)
	if err != nil {
		t.Fatalf("notify error: %v", err)
	}
	if mode != "live" {
		t.Fatalf("mode: got %q want live", mode)
	}
	if gotPath != wantPath {
		t.Fatalf("path: got %q want %s", gotPath, wantPath)
	}
}
