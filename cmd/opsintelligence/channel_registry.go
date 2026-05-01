package main

// channel_registry.go is the single place to add a new channel integration.
//
// To add a new channel:
//  1. Create the adapter package under internal/channels/<name>/.
//  2. Add one reg.Add(...) block below with Configured, Build, and DoctorPing.
//  3. Add a config field to internal/config/config.go ChannelsConfig.
//
// No other files need to change.

import (
	"context"
	"fmt"
	"path/filepath"

	chadapter "github.com/opsintelligence/opsintelligence/internal/channels/adapter"
	"github.com/opsintelligence/opsintelligence/internal/channels"
	"github.com/opsintelligence/opsintelligence/internal/channels/discord"
	"github.com/opsintelligence/opsintelligence/internal/channels/msteams"
	chanregistry "github.com/opsintelligence/opsintelligence/internal/channels/registry"
	"github.com/opsintelligence/opsintelligence/internal/channels/slack"
	"github.com/opsintelligence/opsintelligence/internal/channels/telegram"
	"github.com/opsintelligence/opsintelligence/internal/channels/whatsapp"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/tools"
	"github.com/opsintelligence/opsintelligence/internal/voice"
)

// channelStartDeps holds runtime dependencies needed when building live channel instances.
// Pass nil when using the registry only for config inspection or doctor checks.
type channelStartDeps struct {
	reliabilityCfg chadapter.ReliabilityConfig
	channelSenders map[string]tools.ChannelSender
	voiceClient    *voice.Client
	stateDir       string
	logLevel       string
}

// buildChannelRegistry constructs the channel registry for the given config.
// When deps is nil, Build functions return (nil, nil); Configured and DoctorPing still work.
func buildChannelRegistry(cfg *config.Config, deps *channelStartDeps) *chanregistry.Registry {
	reg := chanregistry.New()

	// ── Telegram ──────────────────────────────────────────────────────────────
	reg.Add(chanregistry.Registration{
		Entry: chanregistry.Entry{ID: "telegram", DisplayName: "Telegram"},
		Configured: func() bool { return cfg.Channels.Telegram != nil },
		Build: func() (channels.Channel, error) {
			if deps == nil {
				return nil, nil
			}
			requireMention := true
			if cfg.Channels.Telegram.RequireMention != nil {
				requireMention = *cfg.Channels.Telegram.RequireMention
			}
			tg, err := telegram.New(
				cfg.Channels.Telegram.BotToken,
				cfg.Channels.Telegram.DMMode,
				cfg.Channels.Telegram.AllowFrom,
				requireMention,
			)
			if err != nil {
				return nil, err
			}
			tgRS := chadapter.NewReliableSender("telegram", tg, deps.reliabilityCfg)
			tg.WithReliableOutbound(tgRS)
			deps.channelSenders["telegram"] = reliableToolSender{rs: tgRS}
			return tg, nil
		},
		DoctorPing: func(ctx context.Context) error {
			tg, err := telegram.New(cfg.Channels.Telegram.BotToken, cfg.Channels.Telegram.DMMode, cfg.Channels.Telegram.AllowFrom, true)
			if err != nil {
				return fmt.Errorf("Telegram: init: %s", formatChannelPingError("channel.telegram", "init", err))
			}
			if err := tg.Ping(ctx); err != nil {
				return fmt.Errorf("Telegram: %s", formatChannelPingError("channel.telegram", "getMe", err))
			}
			return nil
		},
	})

	// ── Discord ───────────────────────────────────────────────────────────────
	reg.Add(chanregistry.Registration{
		Entry: chanregistry.Entry{ID: "discord", DisplayName: "Discord"},
		Configured: func() bool { return cfg.Channels.Discord != nil },
		Build: func() (channels.Channel, error) {
			if deps == nil {
				return nil, nil
			}
			requireMention := true
			if cfg.Channels.Discord.RequireMention != nil {
				requireMention = *cfg.Channels.Discord.RequireMention
			}
			dc, err := discord.New(
				cfg.Channels.Discord.BotToken,
				cfg.Channels.Discord.DMMode,
				cfg.Channels.Discord.AllowFrom,
				requireMention,
				deps.voiceClient,
			)
			if err != nil {
				return nil, err
			}
			dcRS := chadapter.NewReliableSender("discord", dc, deps.reliabilityCfg)
			dc.WithReliableOutbound(dcRS)
			deps.channelSenders["discord"] = reliableToolSender{rs: dcRS}
			return dc, nil
		},
		DoctorPing: func(ctx context.Context) error {
			dc, err := discord.New(cfg.Channels.Discord.BotToken, cfg.Channels.Discord.DMMode, cfg.Channels.Discord.AllowFrom, true, nil)
			if err != nil {
				return fmt.Errorf("Discord: init: %s", formatChannelPingError("channel.discord", "init", err))
			}
			if err := dc.Ping(ctx); err != nil {
				return fmt.Errorf("Discord: %s", formatChannelPingError("channel.discord", "users/@me", err))
			}
			return nil
		},
	})

	// ── Slack ─────────────────────────────────────────────────────────────────
	chadapter.RegisterChannelHint("slack", "\n- **Slack:** short replies, mrkdwn, small snippets—link out to PRs/pipelines for detail.")
	reg.Add(chanregistry.Registration{
		Entry: chanregistry.Entry{ID: "slack", DisplayName: "Slack"},
		Configured: func() bool { return cfg.Channels.Slack != nil },
		Build: func() (channels.Channel, error) {
			if deps == nil {
				return nil, nil
			}
			sl, err := slack.New(cfg.Channels.Slack.BotToken, cfg.Channels.Slack.AppToken, cfg.Channels.Slack.DMMode, cfg.Channels.Slack.AllowFrom)
			if err != nil {
				return nil, err
			}
			slRS := chadapter.NewReliableSender("slack", sl, deps.reliabilityCfg)
			sl.WithReliableOutbound(slRS)
			deps.channelSenders["slack"] = reliableToolSender{rs: slRS}
			return sl, nil
		},
		DoctorPing: func(ctx context.Context) error {
			if err := validateSlackTokenFormats(cfg.Channels.Slack.BotToken, cfg.Channels.Slack.AppToken); err != nil {
				return fmt.Errorf("Slack: %s", formatChannelTokenError("token format", err))
			}
			sl, err := slack.New(cfg.Channels.Slack.BotToken, cfg.Channels.Slack.AppToken, cfg.Channels.Slack.DMMode, cfg.Channels.Slack.AllowFrom)
			if err != nil {
				return fmt.Errorf("Slack: %s", formatChannelPingError("channel.slack", "init", err))
			}
			if err := sl.Ping(ctx); err != nil {
				return fmt.Errorf("Slack: %s", formatChannelPingError("channel.slack", "auth.test", err))
			}
			return nil
		},
	})

	// ── WhatsApp ──────────────────────────────────────────────────────────────
	chadapter.RegisterChannelHint("whatsapp", "\n- **WhatsApp:** short paragraphs; avoid dumping huge logs or raw diffs in one message.")
	reg.Add(chanregistry.Registration{
		Entry: chanregistry.Entry{ID: "whatsapp", DisplayName: "WhatsApp"},
		Configured: func() bool { return cfg.Channels.WhatsApp != nil },
		Build: func() (channels.Channel, error) {
			if deps == nil {
				return nil, nil
			}
			wa, err := whatsapp.New(
				filepath.Join(deps.stateDir, "whatsapp.db"),
				cfg.Channels.WhatsApp.SessionID,
				cfg.Channels.WhatsApp.DMMode,
				cfg.Channels.WhatsApp.AllowFrom,
				deps.logLevel,
				deps.voiceClient,
			)
			if err != nil {
				return nil, err
			}
			return wa, nil
		},
		// WhatsApp has no lightweight ping (requires a full session handshake).
	})

	// ── Microsoft Teams ───────────────────────────────────────────────────────
	reg.Add(chanregistry.Registration{
		Entry: chanregistry.Entry{ID: "msteams", DisplayName: "Microsoft Teams"},
		// When expose_via: gateway the channel is mounted on the gateway server
		// directly (see main.go), not registered as a standalone channel here.
		Configured: func() bool {
			return cfg.Channels.Teams != nil && cfg.Channels.Teams.ExposeVia != "gateway" // standalone server on listen_addr
		},
		Build: func() (channels.Channel, error) {
			if deps == nil {
				return nil, nil
			}
			teams, err := msteams.New(
				cfg.Channels.Teams.AppID,
				cfg.Channels.Teams.AppPassword,
				cfg.Channels.Teams.ListenAddr,
				cfg.Channels.Teams.DMMode,
				cfg.Channels.Teams.AllowFrom,
			)
			if err != nil {
				return nil, err
			}
			teamsRS := chadapter.NewReliableSender("msteams", teams, deps.reliabilityCfg)
			teams.WithReliableOutbound(teamsRS)
			deps.channelSenders["msteams"] = reliableToolSender{rs: teamsRS}
			return teams, nil
		},
		DoctorPing: func(ctx context.Context) error {
			teams, err := msteams.New(
				cfg.Channels.Teams.AppID,
				cfg.Channels.Teams.AppPassword,
				cfg.Channels.Teams.ListenAddr,
				cfg.Channels.Teams.DMMode,
				cfg.Channels.Teams.AllowFrom,
			)
			if err != nil {
				return fmt.Errorf("Microsoft Teams: init: %s", formatChannelPingError("channel.msteams", "init", err))
			}
			if err := teams.Ping(ctx); err != nil {
				return fmt.Errorf("Microsoft Teams: %s", formatChannelPingError("channel.msteams", "token fetch", err))
			}
			return nil
		},
	})

	return reg
}
