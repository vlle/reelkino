// Package media — работа с рилсами через внешний yt-dlp:
// метаданные (описание) и скачивание видео с превью на диск.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	probeTimeout    = 90 * time.Second
	downloadTimeout = 5 * time.Minute
	maxFilesize     = "300M"
)

type Client struct {
	bin     string
	dir     string
	cookies string // netscape cookies.txt; инстаграм без логина режет датацентровые IP
}

func New(bin, dir, cookies string) (*Client, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("media: mkdir %s: %w", dir, err)
	}
	return &Client{bin: bin, dir: dir, cookies: cookies}, nil
}

func (c *Client) args(rest ...string) []string {
	base := []string{"--no-playlist", "--playlist-items", "1", "--no-warnings"}
	if c.cookies != "" {
		base = append(base, "--cookies", c.cookies)
	}
	return append(base, rest...)
}

// Dir — каталог со скачанными файлами.
func (c *Client) Dir() string { return c.dir }

// Info — метаданные ролика.
type Info struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Probe достаёт название и описание ролика без скачивания.
func (c *Client) Probe(url string) (*Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin, c.args("-j", url)...).Output()
	if err != nil {
		return nil, fmt.Errorf("media: probe: %s", execErr(err))
	}
	var info Info
	if err := json.Unmarshal(out, &info); err != nil {
		return nil, fmt.Errorf("media: probe decode: %w", err)
	}
	return &info, nil
}

// Download качает ролик и его превью в dir под именем <base>.*.
// Возвращает basename видео и превью (превью может не быть).
func (c *Client) Download(url, base string) (video, thumb string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.bin, c.args(
		"-f", "mp4/best",
		"--max-filesize", maxFilesize,
		"--write-thumbnail",
		"-o", filepath.Join(c.dir, base+".%(ext)s"),
		url,
	)...).CombinedOutput()
	if err != nil {
		return "", "", fmt.Errorf("media: download: %v: %s", err, tail(string(out)))
	}
	matches, _ := filepath.Glob(filepath.Join(c.dir, base+".*"))
	for _, m := range matches {
		switch strings.ToLower(filepath.Ext(m)) {
		case ".mp4", ".webm", ".mkv", ".mov":
			video = filepath.Base(m)
		case ".jpg", ".jpeg", ".webp", ".png":
			thumb = filepath.Base(m)
		}
	}
	if video == "" {
		return "", "", fmt.Errorf("media: download: видео не появилось: %s", tail(string(out)))
	}
	return video, thumb, nil
}

// Remove удаляет скачанные файлы (пустые имена пропускает).
func (c *Client) Remove(names ...string) {
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "/\\") {
			continue
		}
		_ = os.Remove(filepath.Join(c.dir, n))
	}
}

func execErr(err error) string {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return tail(string(ee.Stderr))
	}
	return err.Error()
}

// tail — последние строки вывода yt-dlp для сообщения об ошибке.
func tail(s string) string {
	s = strings.TrimSpace(s)
	if lines := strings.Split(s, "\n"); len(lines) > 3 {
		s = strings.Join(lines[len(lines)-3:], "\n")
	}
	if len(s) > 500 {
		s = s[len(s)-500:]
	}
	return s
}
