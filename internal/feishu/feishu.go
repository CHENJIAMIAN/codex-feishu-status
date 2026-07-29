package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher/callback"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

type Messenger interface {
	CreateCard(ctx context.Context, chatID, content string) (string, error)
	PatchCard(ctx context.Context, messageID, content string) error
	SendText(ctx context.Context, chatID, content string) error
}

type messenger struct {
	client *lark.Client
}

type IncomingMessage struct {
	ChatID string
	Text   string
}

type CardAction struct {
	ChatID    string
	MessageID string
	Value     map[string]any
}

type ControlCallbacks struct {
	OnText       func(context.Context, IncomingMessage) error
	OnCardAction func(context.Context, CardAction) (string, error)
}

func NewMessenger(appID, appSecret string) (Messenger, error) {
	if strings.TrimSpace(appID) == "" || strings.TrimSpace(appSecret) == "" {
		return nil, errors.New("Feishu app ID or app secret is empty")
	}
	return &messenger{client: lark.NewClient(appID, appSecret)}, nil
}

func (messenger *messenger) CreateCard(ctx context.Context, chatID, content string) (string, error) {
	response, err := messenger.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("interactive").
			Content(content).
			Build()).
		Build())
	if err != nil {
		return "", err
	}
	if !response.Success() {
		return "", fmt.Errorf("create Feishu card failed: code=%d msg=%s", response.Code, response.Msg)
	}
	if response.Data == nil || response.Data.MessageId == nil || *response.Data.MessageId == "" {
		return "", errors.New("create Feishu card returned no message ID")
	}
	return *response.Data.MessageId, nil
}

func (messenger *messenger) PatchCard(ctx context.Context, messageID, content string) error {
	response, err := messenger.client.Im.V1.Message.Patch(ctx, larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().Content(content).Build()).
		Build())
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("update Feishu card failed: code=%d msg=%s", response.Code, response.Msg)
	}
	return nil
}

func (messenger *messenger) SendText(ctx context.Context, chatID, content string) error {
	payload, err := json.Marshal(map[string]string{"text": content})
	if err != nil {
		return err
	}
	response, err := messenger.client.Im.V1.Message.Create(ctx, larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType("text").
			Content(string(payload)).
			Build()).
		Build())
	if err != nil {
		return err
	}
	if !response.Success() {
		return fmt.Errorf("send Feishu text failed: code=%d msg=%s", response.Code, response.Msg)
	}
	return nil
}

// RunControl keeps a single long connection open for bot commands and card actions.
// It leaves authorization decisions to the caller, which knows the configured chat ID.
func RunControl(ctx context.Context, appID, appSecret string, callbacks ControlCallbacks) error {
	eventHandler := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(eventCtx context.Context, event *larkim.P2MessageReceiveV1) error {
			if callbacks.OnText == nil {
				return nil
			}
			message, ok := incomingMessage(event)
			if !ok {
				return nil
			}
			return callbacks.OnText(eventCtx, message)
		}).
		OnP2CardActionTrigger(func(eventCtx context.Context, event *callback.CardActionTriggerEvent) (*callback.CardActionTriggerResponse, error) {
			if callbacks.OnCardAction == nil {
				return nil, nil
			}
			action, ok := incomingCardAction(event)
			if !ok {
				return nil, nil
			}
			toast, err := callbacks.OnCardAction(eventCtx, action)
			if err != nil {
				return &callback.CardActionTriggerResponse{Toast: &callback.Toast{
					Type:    "error",
					Content: "设置未保存，请稍后重试",
				}}, nil
			}
			if strings.TrimSpace(toast) == "" {
				return nil, nil
			}
			return &callback.CardActionTriggerResponse{Toast: &callback.Toast{
				Type:    "success",
				Content: toast,
			}}, nil
		})
	client := larkws.NewClient(appID, appSecret, larkws.WithEventHandler(eventHandler))
	return client.Start(ctx)
}

func WaitForBinding(ctx context.Context, appID, appSecret, code string) (string, error) {
	if strings.TrimSpace(code) == "" {
		return "", errors.New("pairing code is empty")
	}

	boundChatIDs := make(chan string, 1)
	eventHandler := dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(func(_ context.Context, event *larkim.P2MessageReceiveV1) error {
		chatID, matched := matchingBinding(event, code)
		if !matched {
			return nil
		}
		select {
		case boundChatIDs <- chatID:
		default:
		}
		return nil
	})

	client := larkws.NewClient(appID, appSecret, larkws.WithEventHandler(eventHandler))
	connectionErrors := make(chan error, 1)
	go func() {
		connectionErrors <- client.Start(ctx)
	}()

	select {
	case chatID := <-boundChatIDs:
		return chatID, nil
	case err := <-connectionErrors:
		if err == nil {
			return "", errors.New("Feishu long connection stopped unexpectedly")
		}
		return "", fmt.Errorf("start Feishu long connection: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func matchingBinding(event *larkim.P2MessageReceiveV1, code string) (string, bool) {
	message, ok := incomingMessage(event)
	if !ok || (message.Text != "绑定 "+code && message.Text != "bind "+code) {
		return "", false
	}
	return message.ChatID, true
}

func incomingMessage(event *larkim.P2MessageReceiveV1) (IncomingMessage, bool) {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Message.ChatId == nil || event.Event.Message.Content == nil {
		return IncomingMessage{}, false
	}
	if event.Event.Message.MessageType == nil || *event.Event.Message.MessageType != "text" {
		return IncomingMessage{}, false
	}
	var content struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(*event.Event.Message.Content), &content); err != nil {
		return IncomingMessage{}, false
	}
	text := strings.TrimSpace(content.Text)
	if text == "" {
		return IncomingMessage{}, false
	}
	return IncomingMessage{ChatID: *event.Event.Message.ChatId, Text: text}, true
}

func incomingCardAction(event *callback.CardActionTriggerEvent) (CardAction, bool) {
	if event == nil || event.Event == nil || event.Event.Context == nil || event.Event.Action == nil {
		return CardAction{}, false
	}
	chatID := strings.TrimSpace(event.Event.Context.OpenChatID)
	if chatID == "" {
		return CardAction{}, false
	}
	return CardAction{
		ChatID:    chatID,
		MessageID: strings.TrimSpace(event.Event.Context.OpenMessageID),
		Value:     event.Event.Action.Value,
	}, true
}
