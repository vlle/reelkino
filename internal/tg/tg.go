// Package tg — минимальный клиент Telegram Bot API (long polling),
// без внешних зависимостей.
package tg

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	token string
	hc    *http.Client // обычные вызовы
	poll  *http.Client // getUpdates: таймаут больше long-poll окна
}

func New(token string, pollTimeout time.Duration) *Client {
	return &Client{
		token: token,
		hc:    &http.Client{Timeout: 15 * time.Second},
		poll:  &http.Client{Timeout: pollTimeout + 15*time.Second},
	}
}

type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
}

type User struct {
	ID        int64  `json:"id"`
	FirstName string `json:"first_name"`
	Username  string `json:"username"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string      `json:"text"`
	CallbackData string      `json:"callback_data,omitempty"`
	URL          string      `json:"url,omitempty"`
	WebApp       *WebAppInfo `json:"web_app,omitempty"`
}

type WebAppInfo struct {
	URL string `json:"url"`
}

func (c *Client) call(hc *http.Client, method string, payload, out any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("tg: marshal: %w", err)
	}
	resp, err := hc.Post(
		"https://api.telegram.org/bot"+c.token+"/"+method,
		"application/json",
		bytes.NewReader(b),
	)
	if err != nil {
		return fmt.Errorf("tg: %s: %w", method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("tg: %s: read: %w", method, err)
	}
	var env struct {
		OK          bool            `json:"ok"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return fmt.Errorf("tg: %s: decode: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("tg: %s: api error: %s", method, env.Description)
	}
	if out != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("tg: %s: decode result: %w", method, err)
		}
	}
	return nil
}

// GetUpdates блокируется до timeoutSec в ожидании событий.
func (c *Client) GetUpdates(offset int64, timeoutSec int) ([]Update, error) {
	var updates []Update
	err := c.call(c.poll, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         timeoutSec,
		"allowed_updates": []string{"message", "callback_query"},
	}, &updates)
	return updates, err
}

// SendMessage возвращает id отправленного сообщения.
func (c *Client) SendMessage(chatID int64, text string, kb *InlineKeyboardMarkup) (int64, error) {
	payload := map[string]any{"chat_id": chatID, "text": text}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var m Message
	err := c.call(c.hc, "sendMessage", payload, &m)
	return m.MessageID, err
}

// SendPhoto шлёт фото по внешнему URL; при ошибке (битый постер)
// вызывающий делает fallback на SendMessage.
func (c *Client) SendPhoto(chatID int64, photoURL, caption string, kb *InlineKeyboardMarkup) (int64, error) {
	payload := map[string]any{"chat_id": chatID, "photo": photoURL, "caption": caption}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	var m Message
	err := c.call(c.hc, "sendPhoto", payload, &m)
	return m.MessageID, err
}

func (c *Client) EditMessageText(chatID, messageID int64, text string, kb *InlineKeyboardMarkup) error {
	payload := map[string]any{"chat_id": chatID, "message_id": messageID, "text": text}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	return c.call(c.hc, "editMessageText", payload, nil)
}

// EditMessageCaption — аналог EditMessageText для сообщений-фото.
func (c *Client) EditMessageCaption(chatID, messageID int64, caption string, kb *InlineKeyboardMarkup) error {
	payload := map[string]any{"chat_id": chatID, "message_id": messageID, "caption": caption}
	if kb != nil {
		payload["reply_markup"] = kb
	}
	return c.call(c.hc, "editMessageCaption", payload, nil)
}

func (c *Client) DeleteMessage(chatID, messageID int64) error {
	return c.call(c.hc, "deleteMessage", map[string]any{
		"chat_id": chatID, "message_id": messageID,
	}, nil)
}

func (c *Client) AnswerCallbackQuery(id, text string) error {
	return c.call(c.hc, "answerCallbackQuery", map[string]any{
		"callback_query_id": id,
		"text":              text,
	}, nil)
}

// SetChatMenuButton ставит кнопку меню чата, открывающую Mini App.
func (c *Client) SetChatMenuButton(text, webAppURL string) error {
	return c.call(c.hc, "setChatMenuButton", map[string]any{
		"menu_button": map[string]any{
			"type":    "web_app",
			"text":    text,
			"web_app": WebAppInfo{URL: webAppURL},
		},
	}, nil)
}

// SetMyCommands регистрирует команды бота (подсказки в клиенте).
func (c *Client) SetMyCommands(commands [][2]string) error {
	list := make([]map[string]string, 0, len(commands))
	for _, cmd := range commands {
		list = append(list, map[string]string{"command": cmd[0], "description": cmd[1]})
	}
	return c.call(c.hc, "setMyCommands", map[string]any{"commands": list}, nil)
}
