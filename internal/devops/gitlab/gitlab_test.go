package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListMergeRequests(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Fatalf("missing token")
		}
		if !strings.Contains(r.RequestURI, "/api/v4/projects/acme%2Fapi/merge_requests") {
			t.Fatalf("unexpected URI: %s (path=%s)", r.RequestURI, r.URL.Path)
		}
		if r.URL.Query().Get("state") != "opened" {
			t.Fatalf("unexpected state: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode([]MergeRequest{{IID: 11, Title: "feat", State: "opened"}})
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Token: "tok"}, srv.Client())
	mrs, err := c.ListMergeRequests(context.Background(), "acme/api", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(mrs) != 1 || mrs[0].IID != 11 {
		t.Fatalf("unexpected: %+v", mrs)
	}
}

func TestListPipelines(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("ref"); got != "main" {
			t.Fatalf("ref=%q", got)
		}
		_ = json.NewEncoder(w).Encode([]Pipeline{{ID: 99, Ref: "main", Status: "failed"}})
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Token: "t"}, srv.Client())
	ps, err := c.ListPipelines(context.Background(), "acme/api", "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 || ps[0].Status != "failed" {
		t.Fatalf("unexpected: %+v", ps)
	}
}
