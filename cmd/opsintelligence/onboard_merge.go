package main

import (
	"bytes"
	"os"

	"gopkg.in/yaml.v3"
)

// mergeOnboardYAML overlays new YAML onto an existing config file when present.
// Top-level keys the wizard owns (gateway, providers, …) replace the previous
// subtree when present in newYAML. agent and webhooks are merged recursively so
// fields the wizard does not emit (memory, custom prompts, …) survive re-runs.
// Keys only present in the old file are preserved.
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
	// Wizard-emitted subtrees replace wholesale so channel deselection etc. stick.
	fullReplaceTopLevel := map[string]struct{}{
		"gateway":    {},
		"providers":  {},
		"embeddings": {},
		"channels":   {},
		"routing":    {},
		"plano":      {},
		"teams":      {},
		"devops":     {},
	}
	for k, nv := range newDoc {
		if _, ok := fullReplaceTopLevel[k]; ok {
			out[k] = nv
			continue
		}
		switch k {
		case "agent", "webhooks":
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
		out[k] = mergeYAMLValues(out[k], nv)
	}
	return out
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
