package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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
)

var allMessages = flag.Bool("all-messages", false, "Fetch and send all historical messages")

func Run(ctx context.Context) error {
	flag.Parse()
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

		if *allMessages {
			go func() {
				err := fetchAndProcessMessages(ctx, log, cfg, api)
				if err != nil {
					log.Error("fetch and process messages", zap.Error(err))
				}
			}()
		}

		return gaps.Run(ctx, client.API(), user.ID, updates.AuthOptions{
			OnStart: func(ctx context.Context) {
				log.Info("Gaps started")
			},
		})
	})
}

func getChannel(ctx context.Context, client *tg.Client, channelID int64) (*tg.Channel, error) {
	inputChannel := &tg.InputChannel{
		ChannelID:  channelID,
		AccessHash: 0, // This will be updated with the correct access hash
	}

	channels, err := client.ChannelsGetChannels(ctx, []tg.InputChannelClass{inputChannel})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel: %w", err)
	}

	if len(channels.GetChats()) == 0 {
		return nil, fmt.Errorf("no channels found")
	}

	channel, ok := channels.GetChats()[0].(*tg.Channel)
	if !ok {
		return nil, errors.New("unexpected chat type")
	}

	return channel, nil
}

func handleEditChannelMessage(ctx context.Context, log *zap.Logger, cfg *config.Config, api *tg.Client, update *tg.UpdateEditChannelMessage) error {
	msg, _ := update.GetMessage().(*tg.Message)
	channel, err := getChannel(ctx, api, int64(msg.GetPeerID().(*tg.PeerChannel).ChannelID))
	if err != nil {
		log.Error("get channel", zap.Error(err))
		return err
	}

	if channel.GetID() == cfg.TgApp.ChatForWatch {
		text := msg.GetMessage()
		err := sendMessage(text, cfg.TgApp.WebhookUrl, "editMessage", msg.GetID())
		if err != nil {
			log.Error("Error sending message", zap.Error(err))
		}
		log.Info("Message", zap.Any("text", text))
	}

	return nil
}

func handleNewChannelMessage(ctx context.Context, log *zap.Logger, cfg *config.Config, api *tg.Client, update *tg.UpdateNewChannelMessage) error {
	msg, _ := update.GetMessage().(*tg.Message)
	channel, err := getChannel(ctx, api, int64(msg.GetPeerID().(*tg.PeerChannel).ChannelID))
	if err != nil {
		log.Error("get channel", zap.Error(err))
		return err
	}

	if channel.GetID() == cfg.TgApp.ChatForWatch {
		text := msg.GetMessage()
		err := sendMessage(text, cfg.TgApp.WebhookUrl, "newMessage", msg.GetID())
		if err != nil {
			log.Error("Error sending message", zap.Error(err))
		}
		log.Info("Message", zap.Any("text", text))
	}

	return nil
}

func fetchAndProcessMessages(ctx context.Context, log *zap.Logger, cfg *config.Config, api *tg.Client) error {
	channel, err := getChannel(ctx, api, int64(cfg.TgApp.ChatForWatch))
	if err != nil {
		return err
	}

	peer := &tg.InputPeerChannel{
		ChannelID:  channel.ID,
		AccessHash: channel.AccessHash,
	}

	offsetID := 0
	for {
		messages, err := api.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     peer,
			OffsetID: offsetID,
			Limit:    100,
		})
		if err != nil {
			return err
		}

		history, ok := messages.(*tg.MessagesChannelMessages)
		if !ok {
			return errors.New("unexpected messages type")
		}

		for _, message := range history.Messages {
			msg, ok := message.(*tg.Message)
			if !ok {
				continue
			}

			text := msg.GetMessage()
			err := sendMessage(text, cfg.TgApp.WebhookUrl, "oldMessage", msg.GetID())
			if err != nil {
				log.Error("Error sending message", zap.Error(err))
			}
			log.Info("Message", zap.Any("text", text))
		}

		if len(history.Messages) < 100 {
			break
		}

		offsetID = history.Messages[len(history.Messages)-1].(*tg.Message).ID
	}

	return nil
}

func sendMessage(text string, webHookUrl string, messageType string, messageID int) error {
	postBody, _ := json.Marshal(map[string]string{
		"text":        text,
		"type":        messageType,
		"external_id": strconv.Itoa(messageID),
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
