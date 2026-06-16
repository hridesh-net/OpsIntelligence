package main

import (
	"bytes"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// mergeOnboardYAML overlays new YAML onto an existing config file when present.
// Credential-bearing subtrees (gateway, providers, devops) plus agent/webhooks
// are merged recursively, and a blank new scalar never erases an existing value
// — so tokens/api_keys pasted on a previous onboard survive an Enter-through
// re-run. Non-secret wizard subtrees (channels, routing, …) still replace
// wholesale so deselection sticks. Keys only present in the old file are kept.
func mergeOnboardYAML(configPath string, newYAML []byte) ([]byte, error) {
	newYAML = bytes.TrimSpace(newYAML)
	if len(newYAML) == 0 {
		return newYAML, nil
	}
	var newDoc map[string]interface{}
	if err := yaml.Unmarshal(newYAML, &newDoc); err != nil {
		return nil, err
	}
	prev, err := os.ReadFile(configPath)
	if err != nil || len(bytes.TrimSpace(prev)) == 0 {
		return yamlWithTrailingNewline(newYAML), nil
	}
	var oldDoc map[string]interface{}
	if err := yaml.Unmarshal(prev, &oldDoc); err != nil {
		return yamlWithTrailingNewline(newYAML), nil
	}
	merged := mergeOpsIntelYAMLDocuments(oldDoc, newDoc)
	out, err := yaml.Marshal(merged)
	if err != nil {
		return nil, err
	}
	return yamlWithTrailingNewline(out), nil
}

func yamlWithTrailingNewline(b []byte) []byte {
	b = bytes.TrimRight(b, "\n")
	return append(b, '\n')
}

func mergeOpsIntelYAMLDocuments(oldDoc, newDoc map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(oldDoc)+len(newDoc))
	for k, v := range oldDoc {
		out[k] = v
	}
	// Wizard-emitted subtrees that replace wholesale so deselection (e.g. turning
	// a channel off) sticks. These hold no long-lived pasted secrets.
	fullReplaceTopLevel := map[string]struct{}{
		"embeddings": {},
		"channels":   {},
		"routing":    {},
		"plano":      {},
		"teams":      {},
	}
	for k, nv := range newDoc {
		if _, ok := fullReplaceTopLevel[k]; ok {
			out[k] = nv
			continue
		}
		switch k {
		// Credential-bearing subtrees are merged recursively and blank wizard
		// fields never erase an existing value — so a token/api_key pasted on a
		// previous onboard survives a re-run where the user just hits Enter.
		case "agent", "webhooks", "providers", "devops", "gateway":
			out[k] = mergeYAMLValues(out[k], nv)
		default:
			out[k] = nv
		}
	}
	return out
}

func mergeYAMLValues(oldv, newv interface{}) interface{} {
	if newv == nil {
		return oldv
	}
	oldMap, ok1 := toStringMap(oldv)
	newMap, ok2 := toStringMap(newv)
	if ok1 && ok2 {
		return mergeStringMapsRecursive(oldMap, newMap)
	}
	return newv
}

func mergeStringMapsRecursive(old, new map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(old)+len(new))
	for k, v := range old {
		out[k] = v
	}
	for k, nv := range new {
		// A blank new scalar (e.g. an Enter-through onboard field) must not
		// overwrite an existing non-empty value — this is what keeps pasted
		// credentials (tokens, api_keys) from being wiped on re-onboard.
		if isBlankScalar(nv) {
			if ov, ok := out[k]; ok && !isBlankScalar(ov) {
				continue
			}
		}
		out[k] = mergeYAMLValues(out[k], nv)
	}
	return out
}

// isBlankScalar reports whether v is nil or an empty/whitespace-only string.
func isBlankScalar(v interface{}) bool {
	if v == nil {
		return true
	}
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) == ""
}

func toStringMap(v interface{}) (map[string]interface{}, bool) {
	switch m := v.(type) {
	case map[string]interface{}:
		return m, true
	case map[interface{}]interface{}:
		out := make(map[string]interface{}, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	default:
		return nil, false
	}
}
