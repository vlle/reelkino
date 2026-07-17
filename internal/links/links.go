// Package links — извлечение из текста сообщения ссылок на рилсы
// и на карточки фильмов (Кинопоиск / IMDb).
package links

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// FilmRef — распознанная ссылка на фильм: заполнен ровно один из id.
type FilmRef struct {
	KPID   int64  // kinopoisk.ru/film/<id> или /series/<id>
	IMDbID string // imdb.com/title/tt<digits>
	URL    string
}

// Parsed — результат разбора текста сообщения.
type Parsed struct {
	Film    *FilmRef
	ReelURL string
	Comment string // текст без ссылок
}

var (
	urlRe  = regexp.MustCompile(`https?://[^\s]+`)
	kpRe   = regexp.MustCompile(`^/(?:film|series)/(\d+)`)
	imdbRe = regexp.MustCompile(`^/(?:[a-z]{2}/)?title/(tt\d+)`)
)

// хосты коротких видео; instagram целиком — шарятся и /p/, и /share/
var reelHosts = map[string]bool{
	"instagram.com":        true,
	"tiktok.com":           true,
	"vt.tiktok.com":        true,
	"vm.tiktok.com":        true,
	"youtube.com":          true,
	"youtu.be":             true,
	"m.youtube.com":        true,
	"youtube-nocookie.com": true,
}

func normHost(h string) string {
	h = strings.ToLower(h)
	h = strings.TrimPrefix(h, "www.")
	h = strings.TrimPrefix(h, "m.")
	return h
}

// Parse разбирает текст: первая фильмовая ссылка, первая рилс-ссылка,
// остальной текст — комментарий.
func Parse(text string) Parsed {
	var p Parsed
	comment := text
	for _, raw := range urlRe.FindAllString(text, -1) {
		raw = strings.TrimRight(raw, ").,;!?")
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			continue
		}
		host := normHost(u.Host)
		switch {
		case host == "kinopoisk.ru":
			if m := kpRe.FindStringSubmatch(u.Path); m != nil && p.Film == nil {
				id, _ := strconv.ParseInt(m[1], 10, 64)
				p.Film = &FilmRef{KPID: id, URL: raw}
				comment = strings.Replace(comment, raw, "", 1)
			}
		case host == "imdb.com":
			if m := imdbRe.FindStringSubmatch(u.Path); m != nil && p.Film == nil {
				p.Film = &FilmRef{IMDbID: m[1], URL: raw}
				comment = strings.Replace(comment, raw, "", 1)
			}
		case reelHosts[host] || strings.HasSuffix(host, ".tiktok.com"):
			if p.ReelURL == "" {
				p.ReelURL = raw
				comment = strings.Replace(comment, raw, "", 1)
			}
		}
	}
	p.Comment = strings.TrimSpace(strings.Join(strings.Fields(comment), " "))
	return p
}
