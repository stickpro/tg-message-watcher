package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-faster/errors"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/updates"
	updhook "github.com/gotd/td/telegram/updates/hook"
	"github.com/gotd/td/tg"
	"go-tg.com/internal/config"
	tgService "go-tg.com/internal/services/telegram"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"net/http"
	"strconv"
	"strings"
)

func Run(ctx context.Context) error {
	cfg, err := config.Init()

	if err != nil {
		panic(err)
	}
	sessionStorage := &session.FileStorage{
		Path: "./session.json",
	}

	log, _ := zap.NewDevelopment(zap.IncreaseLevel(zapcore.InfoLevel), zap.AddStacktrace(zapcore.FatalLevel))
	defer func() { _ = log.Sync() }()

	d := tg.NewUpdateDispatcher()
	gaps := updates.New(updates.Config{
		Handler: d,
		Logger:  log.Named("gaps"),
	})

	flow := auth.NewFlow(tgService.Terminal{}, auth.SendCodeOptions{})

	client := telegram.NewClient(cfg.TgApp.AppId, cfg.TgApp.AppHash, telegram.Options{
		SessionStorage: sessionStorage,
		Logger:         log,
		UpdateHandler:  gaps,
		Middlewares: []telegram.Middleware{
			updhook.UpdateHook(gaps.Handle),
		},
	})

	api := tg.NewClient(client)

	handleFuncEditMessage := func(ctx context.Context, e tg.Entities, update *tg.UpdateEditChannelMessage) error {
		return handleEditChannelMessage(ctx, log, cfg, api, update)
	}

	handleFuncNewMessage := func(ctx context.Context, e tg.Entities, update *tg.UpdateNewChannelMessage) error {
		return handleNewChannelMessage(ctx, log, cfg, api, update)
	}

	d.OnEditChannelMessage(handleFuncEditMessage)
	d.OnNewChannelMessage(handleFuncNewMessage)

	return client.Run(ctx, func(ctx context.Context) error {
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return errors.Wrap(err, "auth")
		}

		user, err := client.Self(ctx)
		if err != nil {
			return errors.Wrap(err, "call self")
		}

		return gaps.Run(ctx, client.API(), user.ID, updates.AuthOptions{
			OnStart: func(ctx context.Context) {
				log.Info("Gaps started")
			},
		})
	})
}

func getChannel(ctx context.Context, client *tg.Client, msg tg.NotEmptyMessage) (*tg.Channel, error) {
	ch, ok := msg.GetPeerID().(*tg.PeerChannel)
	if !ok {
		return nil, errors.New("bad peerID")
	}
	channelID := ch.GetChannelID()

	inputChannel := &tg.InputChannel{
		ChannelID:  channelID,
		AccessHash: 0,
	}
	channels, err := client.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})

	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel: %w", err)
	}

	if len(channels.GetChats()) == 0 {
		return nil, fmt.Errorf("no channels found")
	}

	return channels.GetChats()[0].(*tg.Channel), nil
}

func handleEditChannelMessage(ctx context.Context, log *zap.Logger, cfg *config.Config, api *tg.Client, update *tg.UpdateEditChannelMessage) error {
	msg, _ := update.GetMessage().(*tg.Message)
	channel, err := getChannel(ctx, api, msg)
	if err != nil {
		log.Error("get channel", zap.Error(err))
		return err
	}

	if channel.GetID() == cfg.TgApp.ChatForWatch {
		text := strings.ToLower(msg.GetMessage())
		err := sendMessage(text, cfg.TgApp.WebhookUrl, "editMessage", msg.GetID())
		if err != nil {
			fmt.Println("Error sending message:", err)
		}
		log.Info("Message", zap.Any("text", text))
	}

	return nil
}

func handleNewChannelMessage(ctx context.Context, log *zap.Logger, cfg *config.Config, api *tg.Client, update *tg.UpdateNewChannelMessage) error {
	msg, _ := update.GetMessage().(*tg.Message)
	channel, err := getChannel(ctx, api, msg)

	if err != nil {
		log.Error("get channel", zap.Error(err))
		return err
	}

	if channel.GetID() == cfg.TgApp.ChatForWatch {
		text := strings.ToLower(msg.GetMessage())
		err := sendMessage(text, cfg.TgApp.WebhookUrl, "newMessage", msg.GetID())
		if err != nil {
			fmt.Println("Error sending message:", err)
		}
		log.Info("Message", zap.Any("text", text))
	}

	return nil
}

func sendMessage(text string, webHookUrl string, messageType string, messageID int) error {
	postBody, _ := json.Marshal(map[string]string{
		"text":       text,
		"type":       messageType,
		"message_id": strconv.Itoa(messageID),
	})
	responseBody := bytes.NewBuffer(postBody)
	resp, err := http.Post(webHookUrl, "application/json", responseBody)

	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	return nil

}
