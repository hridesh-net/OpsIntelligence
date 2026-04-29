package adapter

import (
	"strings"
	"sync"
)

var (
	hintMu       sync.RWMutex
	hintRegistry = map[string]string{}
)

// RegisterChannelHint associates a system-prompt formatting hint with channelID.
// Hints are injected into the agent system prompt when sessions originate from that channel.
func RegisterChannelHint(channelID, hint string) {
	key := strings.ToLower(strings.TrimSpace(channelID))
	if key == "" || hint == "" {
		return
	}
	hintMu.Lock()
	hintRegistry[key] = hint
	hintMu.Unlock()
}

// HintFor returns the registered system-prompt hint for channelID, or "" if none is registered.
func HintFor(channelID string) string {
	key := strings.ToLower(strings.TrimSpace(channelID))
	hintMu.RLock()
	h := hintRegistry[key]
	hintMu.RUnlock()
	return h
}
