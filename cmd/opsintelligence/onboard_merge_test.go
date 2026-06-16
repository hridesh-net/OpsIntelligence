package main

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMergeOpsIntelYAMLDocuments_preservesExtras(t *testing.T) {
	old := mustYAMLMap(t, `
version: 1
state_dir: /tmp/x
gateway:
  host: old
  port: 1
agent:
  max_iterations: 10
  memory:
    enabled: true
`)
	newDoc := mustYAMLMap(t, `
version: 1
gateway:
  host: new
  port: 18790
agent:
  max_iterations: 64
`)
	got := mergeOpsIntelYAMLDocuments(old, newDoc)
	if got["state_dir"] != "/tmp/x" {
		t.Fatalf("state_dir: got %#v", got["state_dir"])
	}
	gw := toMap(t, got["gateway"])
	if gw["host"] != "new" || gw["port"] != 18790 {
		t.Fatalf("gateway: %#v", gw)
	}
	ag := toMap(t, got["agent"])
	if ag["max_iterations"] != 64 {
		t.Fatalf("agent.max_iterations: %#v", ag["max_iterations"])
	}
	mem := toMap(t, ag["memory"])
	if mem["enabled"] != true {
		t.Fatalf("agent.memory should survive merge, got %#v", ag["memory"])
	}
}

func TestMergeOpsIntelYAMLDocuments_channelsReplaced(t *testing.T) {
	old := mustYAMLMap(t, `
channels:
  telegram:
    bot_token: "a"
  slack:
    bot_token: "b"
`)
	newDoc := mustYAMLMap(t, `
channels:
  slack:
    bot_token: "c"
`)
	got := mergeOpsIntelYAMLDocuments(old, newDoc)
	ch := toMap(t, got["channels"])
	if _, ok := ch["telegram"]; ok {
		t.Fatalf("telegram should be dropped when wizard omits it: %#v", ch)
	}
	sl := toMap(t, ch["slack"])
	if sl["bot_token"] != "c" {
		t.Fatalf("slack: %#v", sl)
	}
}

func TestMergeOpsIntelYAMLDocuments_webhooksDeepMerge(t *testing.T) {
	old := mustYAMLMap(t, `
webhooks:
  enabled: true
  adapters:
    github:
      enabled: true
      secret: oldsecret
      prompts:
        pull_request: "custom"
`)
	newDoc := mustYAMLMap(t, `
webhooks:
  adapters:
    github:
      secret: newsecret
`)
	got := mergeOpsIntelYAMLDocuments(old, newDoc)
	wh := toMap(t, got["webhooks"])
	gh := toMap(t, toMap(t, wh["adapters"])["github"])
	if gh["secret"] != "newsecret" {
		t.Fatalf("github.secret: %#v", gh["secret"])
	}
	pm := toMap(t, gh["prompts"])
	if pm["pull_request"] != "custom" {
		t.Fatalf("prompts should survive: %#v", gh["prompts"])
	}
}

// Re-onboarding and Enter-ing through provider/devops/gateway steps (blank
// secret fields) must NOT wipe previously pasted credentials. Regression for
// "creds not persistent across upgrade — had to re-onboard".
func TestMergeOpsIntelYAMLDocuments_preservesCredsOnBlankReonboard(t *testing.T) {
	old := mustYAMLMap(t, `
providers:
  gemini:
    api_key: SECRET_GEMINI
    default_model: gemini-2.5-flash
devops:
  github:
    enabled: true
    token: SECRET_GH
gateway:
  token: SECRET_GW
  port: 18790
`)
	// Wizard re-emits the same shape with blank secrets + one real change.
	newDoc := mustYAMLMap(t, `
providers:
  gemini:
    api_key: ""
    default_model: gemini-2.5-flash
devops:
  github:
    enabled: true
    token: ""
gateway:
  token: ""
  port: 19000
`)
	got := mergeOpsIntelYAMLDocuments(old, newDoc)

	gem := toMap(t, toMap(t, got["providers"])["gemini"])
	if gem["api_key"] != "SECRET_GEMINI" {
		t.Fatalf("gemini api_key wiped: %#v", gem["api_key"])
	}
	ghc := toMap(t, toMap(t, got["devops"])["github"])
	if ghc["token"] != "SECRET_GH" {
		t.Fatalf("github token wiped: %#v", ghc["token"])
	}
	gw := toMap(t, got["gateway"])
	if gw["token"] != "SECRET_GW" {
		t.Fatalf("gateway token wiped: %#v", gw["token"])
	}
	if gw["port"] != 19000 {
		t.Fatalf("non-empty change (port) did not apply: %#v", gw["port"])
	}
}

// A non-empty new credential must still override the old one.
func TestMergeOpsIntelYAMLDocuments_nonEmptyCredOverrides(t *testing.T) {
	old := mustYAMLMap(t, "devops:\n  github:\n    token: OLD\n")
	newDoc := mustYAMLMap(t, "devops:\n  github:\n    token: NEW\n")
	got := mergeOpsIntelYAMLDocuments(old, newDoc)
	ghc := toMap(t, toMap(t, got["devops"])["github"])
	if ghc["token"] != "NEW" {
		t.Fatalf("expected NEW to override OLD, got %#v", ghc["token"])
	}
}

func mustYAMLMap(t *testing.T, s string) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := yaml.Unmarshal([]byte(s), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func toMap(t *testing.T, v interface{}) map[string]interface{} {
	t.Helper()
	m, ok := toStringMap(v)
	if !ok {
		t.Fatalf("not a map: %#v", v)
	}
	return m
}
