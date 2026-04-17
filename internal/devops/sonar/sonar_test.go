package sonar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestQualityGate(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := r.BasicAuth()
		if !ok || user != "tok" {
			t.Fatalf("wrong auth: %q ok=%v", user, ok)
		}
		if r.URL.Path != "/api/qualitygates/project_status" {
			t.Fatalf("path: %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("projectKey"); got != "acme_api" {
			t.Fatalf("projectKey=%q", got)
		}
		_ = json.NewEncoder(w).Encode(QualityGateStatus{ProjectStatus: ProjectStatus{
			Status: "ERROR",
			Conditions: []Condition{{
				Status: "ERROR", MetricKey: "new_coverage", Comparator: "LT", ErrorThreshold: "80", ActualValue: "65.4",
			}},
		}})
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Token: "tok", ProjectKeyPrefix: "acme_"}, srv.Client())
	qg, err := c.QualityGate(context.Background(), "api")
	if err != nil {
		t.Fatal(err)
	}
	if qg.ProjectStatus.Status != "ERROR" {
		t.Fatalf("status: %s", qg.ProjectStatus.Status)
	}
	if len(qg.ProjectStatus.Conditions) != 1 || qg.ProjectStatus.Conditions[0].MetricKey != "new_coverage" {
		t.Fatalf("conditions: %+v", qg.ProjectStatus.Conditions)
	}
}

func TestSearchIssuesExtraParams(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("severities"); got != "BLOCKER,CRITICAL" {
			t.Fatalf("severities=%q", got)
		}
		if !strings.Contains(r.URL.RawQuery, "componentKeys=api") {
			t.Fatalf("missing componentKeys: %s", r.URL.RawQuery)
		}
		_ = json.NewEncoder(w).Encode(IssueSearch{Total: 2, Issues: []Issue{{Key: "a"}, {Key: "b"}}})
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, Token: "tok"}, srv.Client())
	extra := url.Values{"severities": []string{"BLOCKER,CRITICAL"}}
	got, err := c.SearchIssues(context.Background(), "api", extra)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 2 {
		t.Fatalf("total=%d", got.Total)
	}
}
