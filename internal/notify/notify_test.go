package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSlackHappyPath(t *testing.T) {
	var received slackMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ev := Event{
		Type:       "complete",
		Contractor: "aria",
		Engine:     "claude-code",
		Model:      "claude-sonnet-4-6",
		Cost:       0.34,
		Turns:      8,
		DurationMS: 45000,
		AuditCount: 14,
	}
	if err := Slack(srv.URL, ev); err != nil {
		t.Fatalf("Slack: %v", err)
	}
	if !strings.Contains(received.Text, "aria") {
		t.Errorf("message should mention contractor, got: %q", received.Text)
	}
	if !strings.Contains(received.Text, "completed") {
		t.Errorf("message should mention completed, got: %q", received.Text)
	}
	if !strings.Contains(received.Text, "0.34") {
		t.Errorf("message should mention cost, got: %q", received.Text)
	}
	if len(received.Blocks) != 2 {
		t.Errorf("expected 2 blocks, got %d", len(received.Blocks))
	}
}

func TestSlackWithJiraKey(t *testing.T) {
	var received slackMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &received)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ev := Event{
		Type:       "start",
		Contractor: "aria",
		Engine:     "claude-code",
		Model:      "claude-sonnet-4-6",
		Budget:     5.0,
		JiraKey:    "PROJ-456",
	}
	if err := Slack(srv.URL, ev); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(received.Text, "PROJ-456") {
		t.Errorf("message should mention Jira key, got: %q", received.Text)
	}
}

func TestSlackEmptyURL(t *testing.T) {
	if err := Slack("", Event{Type: "start"}); err != nil {
		t.Errorf("empty URL should return nil, got: %v", err)
	}
}

func TestSlackServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := Slack(srv.URL, Event{Type: "complete", Contractor: "aria"})
	if err == nil {
		t.Fatal("expected error on 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status code, got: %v", err)
	}
}

func TestSlackAllEventTypes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for _, evType := range []string{"start", "complete", "fail", "cost_blocked"} {
		ev := Event{
			Type:       evType,
			Contractor: "aria",
			Engine:     "claude-code",
			Budget:     5.0,
			Error:      "something went wrong",
		}
		if err := Slack(srv.URL, ev); err != nil {
			t.Errorf("Slack(%q): %v", evType, err)
		}
	}
}

func TestJiraCommentEmptyConfig(t *testing.T) {
	if err := JiraComment(nil, "PROJ-1", Event{}); err != nil {
		t.Errorf("nil config should return nil, got: %v", err)
	}
	if err := JiraComment(&JiraConfig{}, "PROJ-1", Event{}); err != nil {
		t.Errorf("empty baseURL should return nil, got: %v", err)
	}
	if err := JiraComment(&JiraConfig{BaseURL: "http://x"}, "", Event{}); err != nil {
		t.Errorf("empty issueKey should return nil, got: %v", err)
	}
}

func TestJiraCommentHappyPath(t *testing.T) {
	var receivedBody map[string]any
	var receivedAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &receivedBody)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	cfg := &JiraConfig{
		BaseURL: srv.URL,
		Email:   "bot@org.com",
		Token:   "test-token",
	}
	ev := Event{
		Type:       "complete",
		Contractor: "aria",
		Cost:       0.34,
		Turns:      8,
		DurationMS: 45000,
		AuditCount: 14,
	}
	if err := JiraComment(cfg, "PROJ-456", ev); err != nil {
		t.Fatalf("JiraComment: %v", err)
	}
	if !strings.HasPrefix(receivedAuth, "Basic ") {
		t.Errorf("expected Basic auth, got: %q", receivedAuth)
	}
	if receivedBody["body"] == nil {
		t.Error("body should contain ADF document")
	}
}

func TestJiraResolveTokenEnvVar(t *testing.T) {
	t.Setenv("TEST_JIRA_TOKEN", "secret123")
	cfg := &JiraConfig{Token: "${TEST_JIRA_TOKEN}"}
	if got := cfg.ResolveToken(); got != "secret123" {
		t.Errorf("ResolveToken = %q, want secret123", got)
	}

	cfg2 := &JiraConfig{Token: "literal-token"}
	if got := cfg2.ResolveToken(); got != "literal-token" {
		t.Errorf("ResolveToken = %q, want literal-token", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		ms   int64
		want string
	}{
		{500, "500ms"},
		{1500, "1.5s"},
		{45000, "45.0s"},
		{90000, "1.5m"},
	}
	for _, tc := range cases {
		if got := formatDuration(tc.ms); got != tc.want {
			t.Errorf("formatDuration(%d) = %q, want %q", tc.ms, got, tc.want)
		}
	}
}
