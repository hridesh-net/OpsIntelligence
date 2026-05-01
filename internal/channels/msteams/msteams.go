package msteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/channels/adapter"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

const (
	tokenEndpoint = "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token"
	tokenScope    = "https://api.botframework.com/.default"
	maxMessageLen = 28000
)

// Compile-time interface checks.
var (
	_ adapter.Adapter  = (*Channel)(nil)
	_ channels.Channel = (*Channel)(nil)
)

// Channel implements [channels.Channel] and [adapter.Adapter] for Microsoft Teams via Bot Framework.
type Channel struct {
	appID       string
	appPassword string
	listenAddr  string
	dmMode      string
	allowFrom   []string

	server      *http.Server
	stopCh      chan struct{}
	stopOnce    sync.Once
	reliableSend *adapter.ReliableSender

	// serviceURLs maps conversationID → Bot Framework serviceUrl for reply routing.
	serviceURLsMu sync.RWMutex
	serviceURLs   map[string]string

	// convOwner maps conversationID → Teams user id (activity from.id) for the first
	// qualified inbound message, used to enforce allowlist on outbound Send.
	convOwnerMu sync.RWMutex
	convOwner   map[string]string

	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time

	// jwtCache is the JWKS key store used to verify inbound Bot Framework JWTs.
	// A nil value disables verification (Bot Framework Emulator / local dev mode).
	jwtCache *jwksCache
}

// New creates a Teams channel. Use [WithEmulatorMode] to skip JWT verification
// when developing locally with the Bot Framework Emulator.
func New(appID, appPassword, listenAddr, dmMode string, allowFrom []string) (*Channel, error) {
	if appID == "" || appPassword == "" {
		return nil, fmt.Errorf("msteams: app_id and app_password are required")
	}
	if listenAddr == "" {
		listenAddr = ":3978"
	}
	dmMode = strings.ToLower(strings.TrimSpace(dmMode))
	var trimmedAllow []string
	for _, a := range allowFrom {
		if s := strings.TrimSpace(a); s != "" {
			trimmedAllow = append(trimmedAllow, s)
		}
	}
	if len(trimmedAllow) > 0 && dmMode == "" {
		dmMode = "allowlist"
	}
	if dmMode == "" {
		dmMode = "open"
	}
	return &Channel{
		appID:       appID,
		appPassword: appPassword,
		listenAddr:  listenAddr,
		dmMode:      dmMode,
		allowFrom:   trimmedAllow,
		stopCh:      make(chan struct{}),
		serviceURLs: make(map[string]string),
		convOwner:   make(map[string]string),
		jwtCache:    defaultJWKSCache,
	}, nil
}

// WithReliableOutbound wires the reliable sender for retried outbound delivery.
func (c *Channel) WithReliableOutbound(rs *adapter.ReliableSender) *Channel {
	c.reliableSend = rs
	return c
}

// WithEmulatorMode disables JWT verification so the channel works with the
// Bot Framework Emulator (which does not send real Microsoft-signed tokens).
// Do NOT use this in production deployments.
func (c *Channel) WithEmulatorMode() *Channel {
	c.jwtCache = nil
	return c
}

func (c *Channel) Name() string { return "msteams" }

func (c *Channel) AdapterVersion() int { return adapter.Version1 }

func (c *Channel) Capabilities() adapter.ChannelCapabilities {
	return adapter.ChannelCapabilities{
		Threading:        true,
		Attachments:      true,
		DirectMessages:   true,
		GroupMessages:    true,
		Mentions:         true,
		Voice:            false,
		Reactions:        false,
		Edits:            false,
		MaxMessageLength: maxMessageLen,
	}
}

// Ping verifies credentials by fetching a Bot Framework bearer token.
func (c *Channel) Ping(ctx context.Context) error {
	_, err := c.bearerToken(ctx)
	if err != nil {
		return adapter.NewChannelError(adapter.ErrorKindPermanent, "msteams ping: token fetch failed", err)
	}
	return nil
}

// Send posts a message to a Teams conversation using the Bot Framework Connector REST API.
// Session ID format: msteams:<conversationId>.
func (c *Channel) Send(ctx context.Context, msg adapter.OutboundMessage) (*adapter.DeliveryReceipt, error) {
	convID, err := parseTeamsSession(msg.SessionID)
	if err != nil {
		return nil, err
	}

	body := adapter.OutboundBody(msg)
	if body == "" {
		return nil, adapter.NewChannelError(adapter.ErrorKindPermanent, "msteams: empty outbound body", nil)
	}

	c.serviceURLsMu.RLock()
	serviceURL := c.serviceURLs[convID]
	c.serviceURLsMu.RUnlock()
	if serviceURL == "" {
		return nil, adapter.NewChannelError(adapter.ErrorKindPermanent,
			"msteams: no service URL for conversation — bot must receive a message first", nil)
	}

	if err := c.assertOutboundAllowlisted(convID); err != nil {
		return nil, err
	}

	token, err := c.bearerToken(ctx)
	if err != nil {
		return nil, adapter.NewChannelError(adapter.ErrorKindRetryable, "msteams: bearer token", err)
	}

	endpoint := fmt.Sprintf("%s/v3/conversations/%s/activities",
		strings.TrimRight(serviceURL, "/"), url.PathEscape(convID))

	var lastMsgID string
	for _, part := range adapter.SplitMessage(body, maxMessageLen) {
		act := map[string]any{"type": "message", "text": part}
		if msg.ReplyToID != "" {
			act["replyToId"] = msg.ReplyToID
		}

		payload, _ := json.Marshal(act)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, adapter.NewChannelError(adapter.ErrorKindPermanent, "msteams: build request", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, adapter.NewChannelError(adapter.ErrorKindRetryable, "msteams: send activity", err)
		}
		defer resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			return nil, adapter.NewChannelError(adapter.ErrorKindRateLimited, "msteams: rate limited", nil)
		case resp.StatusCode >= 400:
			b, _ := io.ReadAll(resp.Body)
			return nil, adapter.NewChannelError(adapter.ErrorKindRetryable,
				fmt.Sprintf("msteams: send status %d: %s", resp.StatusCode, string(b)), nil)
		}

		var result struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		lastMsgID = result.ID
	}

	return &adapter.DeliveryReceipt{
		ProviderMessageID: lastMsgID,
		IdempotencyKey:    msg.IdempotencyKey,
		SentAt:            time.Now().UTC(),
	}, nil
}

// StartInbound implements [adapter.InboundLifecycle]. It starts an HTTP server that receives
// Bot Framework Activity POSTs from Teams on /api/messages and exposes /health for probes.
func (c *Channel) StartInbound(ctx context.Context, h adapter.InboundHandler) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","channel":"msteams"}`))
	})

	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Verify Bot Framework JWT signature before processing any activity.
		if err := c.verifyInboundJWT(r); err != nil {
			log.Printf("msteams: JWT verification failed: %v", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		r.Body.Close()
		if err != nil {
			http.Error(w, "read error", http.StatusBadRequest)
			return
		}

		var act teamsActivity
		if err := json.Unmarshal(body, &act); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}

		// Always cache serviceUrl so outbound Send can route replies, regardless of activity type.
		c.cacheServiceURL(act.Conversation.ID, act.ServiceURL)

		switch act.Type {
		case "conversationUpdate":
			c.handleConversationUpdate(ctx, act, h)
			w.WriteHeader(http.StatusOK)
			return
		case "message":
			// handled below
		default:
			w.WriteHeader(http.StatusOK)
			return
		}

		text := cleanTeamsText(act.Text, act.TextFormat)
		if text == "" {
			w.WriteHeader(http.StatusOK)
			return
		}

		senderID := act.From.ID

		if c.dmMode == "disabled" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if c.dmMode == "allowlist" {
			if act.Conversation.IsGroup {
				log.Printf("msteams: blocked group conversation from %s in allowlist mode", senderID)
				w.WriteHeader(http.StatusOK)
				return
			}
			if !c.isAllowed(senderID) {
				log.Printf("msteams: blocked message from unauthorized sender: %s", senderID)
				w.WriteHeader(http.StatusOK)
				return
			}
		}

		if act.Conversation.ID != "" && senderID != "" {
			c.convOwnerMu.Lock()
			c.convOwner[act.Conversation.ID] = senderID
			c.convOwnerMu.Unlock()
		}

		ts := act.Timestamp
		if ts.IsZero() {
			ts = time.Now().UTC()
		}

		kind := "dm"
		if act.Conversation.IsGroup {
			kind = "group"
		}

		ev := adapter.InboundEvent{
			ID:         act.ID,
			ChannelID:  c.Name(),
			SessionID:  fmt.Sprintf("msteams:%s", act.Conversation.ID),
			OccurredAt: ts,
			Sender: adapter.SenderRef{
				ID:          act.From.ID,
				DisplayName: act.From.Name,
			},
			Recipient: adapter.RecipientRef{
				ID:   act.Recipient.ID,
				Kind: kind,
			},
			Text: text,
			Parts: []provider.ContentPart{{
				Type: provider.ContentTypeText,
				Text: text,
			}},
			Metadata: map[string]string{
				channels.MetaTeamsConversationID: act.Conversation.ID,
				channels.MetaTeamsServiceURL:     act.ServiceURL,
				channels.MetaTeamsActivityID:     act.ID,
				channels.MetaTeamsTenantID:       act.Conversation.TenantID,
			},
		}

		w.WriteHeader(http.StatusOK)

		go func(ev adapter.InboundEvent) {
			if err := h(ctx, ev); err != nil {
				log.Printf("msteams: inbound handler error: %v", err)
			}
		}(ev)
	})

	c.server = &http.Server{
		Addr:         c.listenAddr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		if err := c.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("msteams: server error: %v", err)
		}
	}()

	log.Printf("channels/msteams: listening for Bot Framework activities on %s/api/messages", c.listenAddr)
	return nil
}

// Start implements [channels.Channel].
func (c *Channel) Start(ctx context.Context, handler channels.MessageHandler) error {
	return c.StartInbound(ctx, c.legacyInboundHandler(handler))
}

func (c *Channel) legacyInboundHandler(handler channels.MessageHandler) adapter.InboundHandler {
	return func(ctx context.Context, ev adapter.InboundEvent) error {
		msg := channels.MessageFromInbound(ev)

		sendText := func(text string) error {
			for i, part := range adapter.SplitMessage(text, maxMessageLen) {
				out := adapter.OutboundMessage{
					SessionID: ev.SessionID,
					Text:      part,
				}
				if i == 0 && ev.ID != "" {
					out.ReplyToID = ev.ID
				}
				if c.reliableSend != nil {
					if _, err := c.reliableSend.Send(ctx, out); err != nil {
						return adapter.NewChannelError(adapter.ErrorKindRetryable, "msteams reply send", err)
					}
				} else {
					if _, err := c.Send(ctx, out); err != nil {
						return err
					}
				}
			}
			return nil
		}

		buf := channels.NewStreamingBuffer(sendText, 700*time.Millisecond)
		replyFn := func(chunk string) error {
			if chunk == "" {
				return nil
			}
			return buf.Push(chunk)
		}

		go func() {
			handler(ctx, msg, replyFn, nil, nil)
			if err := buf.Done(); err != nil {
				log.Printf("msteams: flush send: %v", err)
			}
		}()
		return nil
	}
}

// Stop implements [channels.Channel] and [adapter.InboundLifecycle].
func (c *Channel) Stop() error {
	var retErr error
	c.stopOnce.Do(func() {
		close(c.stopCh)
		if c.server != nil {
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			retErr = c.server.Shutdown(shutCtx)
		}
	})
	return retErr
}

// ── Private helpers ──────────────────────────────────────────────────────────

// cacheServiceURL stores the Bot Framework service URL for a conversation so
// outbound Send can route replies without requiring it in the outbound message.
func (c *Channel) cacheServiceURL(convID, serviceURL string) {
	if convID == "" || serviceURL == "" {
		return
	}
	c.serviceURLsMu.Lock()
	c.serviceURLs[convID] = serviceURL
	c.serviceURLsMu.Unlock()
}

// handleConversationUpdate processes Bot Framework conversationUpdate activities.
// It caches the serviceURL for new conversations and fires an inbound event so
// the agent can send a welcome message if desired.
func (c *Channel) handleConversationUpdate(ctx context.Context, act teamsActivity, h adapter.InboundHandler) {
	// Determine whether the bot itself was added to this conversation.
	botAdded := false
	for _, m := range act.MembersAdded {
		if m.ID == act.Recipient.ID {
			botAdded = true
			break
		}
	}
	if !botAdded || act.Conversation.ID == "" {
		return
	}

	ev := adapter.InboundEvent{
		ID:         act.ID,
		ChannelID:  c.Name(),
		SessionID:  fmt.Sprintf("msteams:%s", act.Conversation.ID),
		OccurredAt: time.Now().UTC(),
		Sender: adapter.SenderRef{
			ID:          act.From.ID,
			DisplayName: act.From.Name,
		},
		Recipient: adapter.RecipientRef{
			ID:   act.Recipient.ID,
			Kind: "system",
		},
		Text: "conversationUpdate:botAdded",
		Parts: []provider.ContentPart{{
			Type: provider.ContentTypeText,
			Text: "conversationUpdate:botAdded",
		}},
		Metadata: map[string]string{
			channels.MetaTeamsConversationID: act.Conversation.ID,
			channels.MetaTeamsServiceURL:     act.ServiceURL,
			channels.MetaTeamsTenantID:       act.Conversation.TenantID,
			"teams_event":                    "bot_added",
		},
	}
	go func() {
		if err := h(ctx, ev); err != nil {
			log.Printf("msteams: conversationUpdate handler error: %v", err)
		}
	}()
}

// bearerToken fetches (or returns a cached) Bot Framework OAuth2 bearer token.
func (c *Channel) bearerToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.cachedToken != "" && time.Now().Before(c.tokenExpiry.Add(-30*time.Second)) {
		return c.cachedToken, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.appID},
		"client_secret": {c.appPassword},
		"scope":         {tokenScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, string(b))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}

	c.cachedToken = tr.AccessToken
	c.tokenExpiry = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return c.cachedToken, nil
}

func (c *Channel) isAllowed(senderID string) bool {
	for _, a := range c.allowFrom {
		if senderID == a {
			return true
		}
	}
	return false
}

func (c *Channel) assertOutboundAllowlisted(convID string) error {
	if c.dmMode == "disabled" {
		return adapter.NewChannelError(adapter.ErrorKindPermanent,
			"msteams: outbound blocked — dm_mode is disabled", nil)
	}
	if c.dmMode != "allowlist" {
		return nil
	}
	c.convOwnerMu.RLock()
	owner, ok := c.convOwner[convID]
	c.convOwnerMu.RUnlock()
	if !ok {
		return adapter.NewChannelError(adapter.ErrorKindPermanent,
			"msteams: outbound blocked — no allowlisted inbound for this conversation yet", nil)
	}
	for _, a := range c.allowFrom {
		if owner == a {
			return nil
		}
	}
	return adapter.NewChannelError(adapter.ErrorKindPermanent,
		"msteams: outbound blocked — conversation owner not in allow_from", nil)
}

func parseTeamsSession(sessionID string) (string, error) {
	const prefix = "msteams:"
	if !strings.HasPrefix(sessionID, prefix) {
		return "", adapter.NewChannelError(adapter.ErrorKindPermanent,
			"msteams: session id must be msteams:<conversationId>", nil)
	}
	convID := strings.TrimPrefix(sessionID, prefix)
	if convID == "" {
		return "", adapter.NewChannelError(adapter.ErrorKindPermanent,
			"msteams: empty conversation id in session", nil)
	}
	return convID, nil
}

// ── Text cleaning ────────────────────────────────────────────────────────────

var (
	// reMention strips <at>BotName</at> mention tags sent by Teams in group channels.
	reMention = regexp.MustCompile(`(?i)<at[^>]*>[^<]*</at>\s*`)
	// reHTMLTag strips any remaining HTML tags (e.g. <b>, <i>, <br/>) from xml-format messages.
	reHTMLTag = regexp.MustCompile(`<[^>]+>`)
)

// cleanTeamsText removes Teams-specific markup from message text:
//   - Strips <at>BotName</at> @mention tags (always, regardless of textFormat)
//   - Strips HTML tags and unescapes entities when textFormat is "xml"
//   - Trims surrounding whitespace
func cleanTeamsText(text, textFormat string) string {
	if text == "" {
		return ""
	}
	// Always strip mention tags — Teams includes them in all textFormat modes.
	text = reMention.ReplaceAllString(text, "")

	// Strip HTML and unescape entities for xml-format messages.
	if textFormat == "xml" || strings.ContainsAny(text, "<>") {
		text = reHTMLTag.ReplaceAllString(text, "")
		text = html.UnescapeString(text)
	}
	return strings.TrimSpace(text)
}

// ── Bot Framework Activity types ─────────────────────────────────────────────

// teamsActivity is a minimal Bot Framework Activity for inbound message parsing.
type teamsActivity struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Timestamp    time.Time           `json:"timestamp"`
	ServiceURL   string              `json:"serviceUrl"`
	From         teamsAccount        `json:"from"`
	Recipient    teamsAccount        `json:"recipient"`
	Conversation teamsConversation   `json:"conversation"`
	Text         string              `json:"text"`
	TextFormat   string              `json:"textFormat"` // "plain", "xml", "markdown"
	ReplyToID    string              `json:"replyToId"`
	MembersAdded []teamsAccount      `json:"membersAdded"` // for conversationUpdate
}

type teamsAccount struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type teamsConversation struct {
	ID       string `json:"id"`
	IsGroup  bool   `json:"isGroup"`
	Name     string `json:"name"`
	TenantID string `json:"tenantId"`
}
