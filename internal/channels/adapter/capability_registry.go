package adapter

import (
	"strings"
	"sync"
)

var capabilityRegistry = map[string]ChannelCapabilities{
	"slack": {
		Threading:        true,
		Attachments:      true,
		DirectMessages:   true,
		GroupMessages:    true,
		Mentions:         true,
		Voice:            false,
		Reactions:        true,
		Edits:            true,
		MaxMessageLength: 40000,
	},
}

var capabilityRegistryMu sync.RWMutex

// CapabilitiesFor returns known capabilities for a built-in channel type.
func CapabilitiesFor(channelType string) (ChannelCapabilities, bool) {
	key := strings.ToLower(strings.TrimSpace(channelType))
	capabilityRegistryMu.RLock()
	c, ok := capabilityRegistry[key]
	capabilityRegistryMu.RUnlock()
	return c, ok
}

// RegisterCapabilities allows extensions/new channels to publish their capabilities.
func RegisterCapabilities(channelType string, caps ChannelCapabilities) {
	channelType = strings.ToLower(strings.TrimSpace(channelType))
	if channelType == "" {
		return
	}
	capabilityRegistryMu.Lock()
	capabilityRegistry[channelType] = caps
	capabilityRegistryMu.Unlock()
}
