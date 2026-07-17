// Package kp — клиент api.kinopoisk.dev (v1.4): метаданные фильма
// по id Кинопоиска или IMDb.
package kp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const baseURL = "https://api.kinopoisk.dev/v1.4"

type Client struct {
	token string
	hc    *http.Client
}

func New(token string) *Client {
	return &Client{token: token, hc: &http.Client{Timeout: 15 * time.Second}}
}

// Movie — нужное нам подмножество ответа kinopoisk.dev.
type Movie struct {
	ID              int64  `json:"id"`
	Name            string `json:"name"`
	AlternativeName string `json:"alternativeName"`
	Year            int    `json:"year"`
	Type            string `json:"type"` // movie | tv-series | cartoon | anime | ...
	IsSeries        bool   `json:"isSeries"`
	Poster          struct {
		URL string `json:"url"`
	} `json:"poster"`
	Rating struct {
		KP   float64 `json:"kp"`
		IMDb float64 `json:"imdb"`
	} `json:"rating"`
	ExternalID struct {
		IMDb string `json:"imdb"`
	} `json:"externalId"`
}

// Title — русское имя, иначе альтернативное.
func (m *Movie) Title() string {
	if m.Name != "" {
		return m.Name
	}
	return m.AlternativeName
}

func (c *Client) get(path string, query url.Values, out any) error {
	u := baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("kp: %w", err)
	}
	req.Header.Set("X-API-KEY", c.token)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("kp: %s: %w", path, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("kp: %s: read: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Message any `json:"message"`
		}
		_ = json.Unmarshal(data, &apiErr)
		return fmt.Errorf("kp: %s: http %d: %v", path, resp.StatusCode, apiErr.Message)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("kp: %s: decode: %w", path, err)
	}
	return nil
}

// ByKPID — фильм по id Кинопоиска.
func (c *Client) ByKPID(id int64) (*Movie, error) {
	var m Movie
	if err := c.get("/movie/"+strconv.FormatInt(id, 10), nil, &m); err != nil {
		return nil, err
	}
	if m.ID == 0 {
		return nil, fmt.Errorf("kp: фильм %d не найден", id)
	}
	return &m, nil
}

// Search — fuzzy-поиск по названию.
func (c *Client) Search(query string, limit int) ([]Movie, error) {
	q := url.Values{}
	q.Set("query", query)
	q.Set("limit", strconv.Itoa(limit))
	q.Set("page", "1")
	var res struct {
		Docs []Movie `json:"docs"`
	}
	if err := c.get("/movie/search", q, &res); err != nil {
		return nil, err
	}
	// search иногда отдаёт пустые записи
	out := res.Docs[:0]
	for _, d := range res.Docs {
		if d.ID != 0 && d.Title() != "" {
			out = append(out, d)
		}
	}
	return out, nil
}

// ByIMDbID — фильм по imdb id (tt...).
func (c *Client) ByIMDbID(imdbID string) (*Movie, error) {
	q := url.Values{}
	q.Set("externalId.imdb", imdbID)
	q.Set("limit", "1")
	var res struct {
		Docs []Movie `json:"docs"`
	}
	if err := c.get("/movie", q, &res); err != nil {
		return nil, err
	}
	if len(res.Docs) == 0 {
		return nil, fmt.Errorf("kp: %s не найден", imdbID)
	}
	return &res.Docs[0], nil
}
