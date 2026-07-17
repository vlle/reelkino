// Package llm — определение фильма по свободному тексту (описание рилса,
// название от пользователя) через opencode Zen (OpenAI-совместимый API).
package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultModel = "deepseek-v4-flash-free"
	// reasoning-модели думают долго
	requestTimeout = 90 * time.Second
)

type Client struct {
	baseURL string
	apiKey  string
	model   string
	hc      *http.Client
}

func New(baseURL, apiKey, model string) *Client {
	if model == "" {
		model = defaultModel
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		model:   model,
		hc:      &http.Client{Timeout: requestTimeout},
	}
}

// Guess — фильм, извлечённый моделью из текста.
type Guess struct {
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Year          int    `json:"year"`
}

const systemPrompt = `Ты определяешь фильм или сериал по тексту: это может быть название, вольное описание сюжета или текст поста из соцсетей (описание рилса с хэштегами и т.п.).
Ответь ТОЛЬКО валидным JSON без markdown-обёртки:
{"title":"название на русском, если есть, иначе оригинальное","original_title":"оригинальное название или пустая строка","year":год числом или 0}
Если фильм определить невозможно — {"title":"","original_title":"","year":0}.`

// GuessMovie просит модель извлечь фильм из текста.
func (c *Client) GuessMovie(text string) (*Guess, error) {
	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
		"temperature": 0,
	})
	if err != nil {
		return nil, fmt.Errorf("llm: marshal: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("llm: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("llm: read: %w", err)
	}

	var out struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("llm: decode (http %d): %w", resp.StatusCode, err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("llm: api: %s", out.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(out.Choices) == 0 {
		return nil, fmt.Errorf("llm: http %d, пустой ответ", resp.StatusCode)
	}
	return parseGuess(out.Choices[0].Message.Content)
}

// parseGuess вырезает JSON из ответа модели (бывает обёрнут в ```json или текст).
func parseGuess(content string) (*Guess, error) {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return nil, fmt.Errorf("llm: нет JSON в ответе: %.200s", content)
	}
	var g Guess
	if err := json.Unmarshal([]byte(content[start:end+1]), &g); err != nil {
		return nil, fmt.Errorf("llm: плохой JSON: %w", err)
	}
	g.Title = strings.TrimSpace(g.Title)
	g.OriginalTitle = strings.TrimSpace(g.OriginalTitle)
	if g.Title == "" && g.OriginalTitle == "" {
		return nil, fmt.Errorf("llm: фильм не распознан")
	}
	if g.Title == "" {
		g.Title = g.OriginalTitle
	}
	return &g, nil
}
