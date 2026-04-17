package jenkins

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGetJob(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "bot" || pass != "tok" {
			t.Fatalf("bad auth: %q %q %v", user, pass, ok)
		}
		if !strings.HasPrefix(r.URL.Path, "/job/platform/job/api-ci/api/json") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Job{
			Name: "api-ci", Color: "red",
			LastBuild: &Build{Number: 21, Result: "FAILURE"},
		})
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, User: "bot", Token: "tok"}, srv.Client())
	j, err := c.GetJob(context.Background(), "platform/api-ci")
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "api-ci" || j.LastBuild == nil || j.LastBuild.Result != "FAILURE" {
		t.Fatalf("unexpected: %+v", j)
	}
}

func TestEncodeJobPath(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"foo":            "job/foo",
		"foo/bar":        "job/foo/job/bar",
		"foo/bar baz":    "job/foo/job/bar%20baz",
		"/a/b/":          "job/a/job/b",
	}
	for in, want := range tests {
		if got := encodeJobPath(in); got != want {
			t.Errorf("encodeJobPath(%q)=%q want %q", in, got, want)
		}
	}
}
