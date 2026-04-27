package msteams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

// Compile-time checks.
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

	// serviceURLs maps conversationID -> Bot Framework serviceUrl for reply routing.
	serviceURLsMu sync.RWMutex
	serviceURLs   map[string]string

	tokenMu     sync.Mutex
	cachedToken string
	tokenExpiry time.Time
}

func New(appID, appPassword, listenAddr, dmMode string, allowFrom []string) (*Channel, error) {
	if appID == "" || appPassword == "" {
		return nil, fmt.Errorf("msteams: app_id and app_password are required")
	}
	if listenAddr == "" {
		listenAddr = ":3978"
	}
	return &Channel{
		appID:       appID,
		appPassword: appPassword,
		listenAddr:  listenAddr,
		dmMode:      dmMode,
		allowFrom:   allowFrom,
		stopCh:      make(chan struct{}),
		serviceURLs: make(map[string]string),
	}, nil
}

func (c *Channel) WithReliableOutbound(rs *adapter.ReliableSender) *Channel {
	c.reliableSend = rs
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
		return nil, adapter.NewChannelError(adapter.ErrorKindPermanent, "msteams: no service URL for conversation — bot must receive a message first", nil)
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
// Bot Framework Activity POSTs from Teams on /api/messages.
func (c *Channel) StartInbound(ctx context.Context, h adapter.InboundHandler) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/messages", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

		// Only handle message activities with text.
		if act.Type != "message" || strings.TrimSpace(act.Text) == "" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Cache serviceUrl so outbound Send can route replies.
		if act.ServiceURL != "" && act.Conversation.ID != "" {
			c.serviceURLsMu.Lock()
			c.serviceURLs[act.Conversation.ID] = act.ServiceURL
			c.serviceURLsMu.Unlock()
		}

		senderID := act.From.ID

		if c.dmMode == "disabled" {
			w.WriteHeader(http.StatusOK)
			return
		}

		if c.dmMode == "allowlist" {
			allowed := false
			for _, a := range c.allowFrom {
				if senderID == a {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("msteams: blocked message from unauthorized sender: %s", senderID)
				w.WriteHeader(http.StatusOK)
				return
			}
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
			Text: act.Text,
			Parts: []provider.ContentPart{{
				Type: provider.ContentTypeText,
				Text: act.Text,
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
		Addr:    c.listenAddr,
		Handler: mux,
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
	ReplyToID    string              `json:"replyToId"`
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
