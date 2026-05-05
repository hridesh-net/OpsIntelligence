package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/channels/adapter"
	"github.com/opsintelligence/opsintelligence/internal/provider"
)

// Compile-time checks.
var (
	_ adapter.Adapter  = (*Channel)(nil)
	_ channels.Channel = (*Channel)(nil)
)

// Channel implements [channels.Channel] and [adapter.Adapter] for Discord.
type Channel struct {
	session        *discordgo.Session
	dmMode         string
	allowFrom      []string
	requireMention bool
	reliableSend   *adapter.ReliableSender
}

func New(token string, dmMode string, allowFrom []string, requireMention bool) (*Channel, error) {
	if token == "" {
		return nil, fmt.Errorf("discord bot token is required")
	}
	if !strings.HasPrefix(token, "Bot ") {
		token = "Bot " + token
	}
	dg, err := discordgo.New(token)
	if err != nil {
		return nil, err
	}
	return &Channel{
		session:        dg,
		dmMode:         dmMode,
		allowFrom:      allowFrom,
		requireMention: requireMention,
	}, nil
}

// WithReliableOutbound wires shared adapter reliability for Discord reply sends.
func (c *Channel) WithReliableOutbound(rs *adapter.ReliableSender) *Channel {
	c.reliableSend = rs
	return c
}

func (c *Channel) Name() string {
	return "discord"
}

func (c *Channel) AdapterVersion() int {
	return adapter.Version1
}

func (c *Channel) Capabilities() adapter.ChannelCapabilities {
	return adapter.ChannelCapabilities{
		Threading:        true,
		Attachments:      true,
		DirectMessages:   true,
		GroupMessages:    true,
		Mentions:         true,
		Reactions:        true,
		Edits:            true,
		MaxMessageLength: 2000,
	}
}

// Ping verifies the bot token via GET /users/@me.
func (c *Channel) Ping(ctx context.Context) error {
	done := make(chan error, 1)
	go func() {
		_, err := c.session.User("@me")
		done <- err
	}()
	select {
	case <-ctx.Done():
		return adapter.NewChannelError(adapter.ErrorKindRetryable, "discord ping cancelled", ctx.Err())
	case err := <-done:
		if err != nil {
			return adapter.NewChannelError(adapter.ErrorKindPermanent, "discord @me failed", err)
		}
		return nil
	}
}

// Send posts a message to a text channel. Session id format: discord:<guildID>:<channelID> (guild may be empty for DMs, e.g. discord::channelID).
func (c *Channel) Send(ctx context.Context, msg adapter.OutboundMessage) (*adapter.DeliveryReceipt, error) {
	guildID, channelID, err := parseDiscordSession(msg.SessionID)
	if err != nil {
		return nil, err
	}
	_ = guildID // channel send uses channel id only
	body := adapter.OutboundBody(msg)
	if body == "" {
		return nil, adapter.NewChannelError(adapter.ErrorKindPermanent, "discord: empty outbound body", nil)
	}
	send := &discordgo.MessageSend{Content: body}
	if msg.ReplyToID != "" {
		send.Reference = &discordgo.MessageReference{MessageID: msg.ReplyToID}
	}
	sent, err := c.session.ChannelMessageSendComplex(channelID, send)
	if err != nil {
		return nil, adapter.NewChannelError(adapter.ErrorKindRetryable, "discord send", err)
	}
	now := time.Now().UTC()
	return &adapter.DeliveryReceipt{
		ProviderMessageID: sent.ID,
		IdempotencyKey:    msg.IdempotencyKey,
		SentAt:            now,
	}, nil
}

func parseDiscordSession(sessionID string) (guildID, channelID string, err error) {
	if !strings.HasPrefix(sessionID, "discord:") {
		return "", "", adapter.NewChannelError(adapter.ErrorKindPermanent, "discord: invalid session prefix", nil)
	}
	rest := strings.TrimPrefix(sessionID, "discord:")
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return "", "", adapter.NewChannelError(adapter.ErrorKindPermanent, "discord: invalid session id", nil)
	}
	return parts[0], parts[1], nil
}

// splitDiscordMessage keeps legacy test/API compatibility while delegating to
// the shared channel adapter splitter.
func splitDiscordMessage(s string) []string {
	return adapter.SplitMessage(s, 2000)
}

// StartInbound implements [adapter.InboundLifecycle].
func (c *Channel) StartInbound(ctx context.Context, h adapter.InboundHandler) error {
	c.session.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}
		if !c.shouldAcceptMessage(m) {
			return
		}

		sessionID := fmt.Sprintf("discord:%s:%s", m.GuildID, m.ChannelID)
		authorID := m.Author.ID

		if c.dmMode == "disabled" {
			return
		}

		if c.dmMode == "allowlist" {
			allowed := false
			for _, allowedNum := range c.allowFrom {
				if authorID == allowedNum {
					allowed = true
					break
				}
			}
			if !allowed {
				log.Printf("Discord: blocked message from unauthorized sender: %s", authorID)
				return
			}
		}

		s.ChannelTyping(m.ChannelID)

		ev := adapter.InboundEvent{
			ID:         m.ID,
			ChannelID:  c.Name(),
			SessionID:  sessionID,
			OccurredAt: m.Timestamp,
			Sender: adapter.SenderRef{
				ID:          m.Author.ID,
				DisplayName: m.Author.Username,
				Username:    m.Author.Username,
			},
			Recipient: adapter.RecipientRef{
				ID:   m.ChannelID,
				Kind: "channel",
			},
			Text: m.Content,
			Parts: []provider.ContentPart{{
				Type: provider.ContentTypeText,
				Text: m.Content,
			}},
			Metadata: map[string]string{
				channels.MetaDiscordChannelID: m.ChannelID,
				channels.MetaDiscordGuildID:   m.GuildID,
			},
		}

		go func() {
			if err := h(ctx, ev); err != nil {
				log.Printf("Discord: inbound handler error: %v", err)
			}
		}()
	})

	err := c.session.Open()
	if err != nil {
		return fmt.Errorf("error opening discord connection: %w", err)
	}

	log.Printf("channels/discord: listening for incoming messages")
	return nil
}

// Start implements [channels.Channel].
func (c *Channel) Start(ctx context.Context, handler channels.MessageHandler) error {
	return c.StartInbound(ctx, c.legacyInboundHandler(handler))
}

func (c *Channel) legacyInboundHandler(handler channels.MessageHandler) adapter.InboundHandler {
	return func(ctx context.Context, ev adapter.InboundEvent) error {
		chID := ev.Metadata[channels.MetaDiscordChannelID]
		if chID == "" {
			return adapter.NewChannelError(adapter.ErrorKindPermanent, "discord: missing channel id", nil)
		}
		guildID := ev.Metadata[channels.MetaDiscordGuildID]

		msg := channels.MessageFromInbound(ev)

		sendText := func(text string) error {
			for _, part := range adapter.SplitMessage(text, 2000) {
				out := adapter.OutboundMessage{
					SessionID: fmt.Sprintf("discord:%s:%s", guildID, chID),
					Text:      part,
				}
				if c.reliableSend != nil {
					if _, err := c.reliableSend.Send(ctx, out); err != nil {
						return adapter.NewChannelError(adapter.ErrorKindRetryable, "discord reply send", err)
					}
				} else {
					if _, err := c.Send(ctx, out); err != nil {
						return adapter.NewChannelError(adapter.ErrorKindRetryable, "discord reply send", err)
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
				log.Printf("Discord: flush send: %v", err)
			}
		}()
		return nil
	}
}

func (c *Channel) shouldAcceptMessage(m *discordgo.MessageCreate) bool {
	if m == nil || m.Message == nil || m.Author == nil || m.GuildID == "" {
		return true // DMs and malformed events pass through existing policies.
	}
	if !c.requireMention {
		return true
	}
	if c.session == nil || c.session.State == nil || c.session.State.User == nil {
		return true
	}
	botID := c.session.State.User.ID
	if botID == "" {
		return true
	}
	if strings.Contains(m.Content, "<@"+botID+">") || strings.Contains(m.Content, "<@!"+botID+">") {
		return true
	}
	if m.MessageReference != nil && m.ReferencedMessage != nil && m.ReferencedMessage.Author != nil {
		if m.ReferencedMessage.Author.ID == botID {
			return true
		}
	}
	return false
}

// Stop implements [channels.Channel] and [adapter.InboundLifecycle].
func (c *Channel) Stop() error {
	return c.session.Close()
}
