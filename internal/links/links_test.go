package links

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		text    string
		kpID    int64
		imdbID  string
		reel    string
		comment string
	}{
		{
			name: "reel + kinopoisk film",
			text: "https://www.instagram.com/reel/DAbCdEf/ https://www.kinopoisk.ru/film/435/",
			kpID: 435,
			reel: "https://www.instagram.com/reel/DAbCdEf/",
		},
		{
			name:    "tiktok short + imdb",
			text:    "глянь https://vt.tiktok.com/ZS8abcdef/ https://www.imdb.com/title/tt0133093/",
			imdbID:  "tt0133093",
			reel:    "https://vt.tiktok.com/ZS8abcdef/",
			comment: "глянь",
		},
		{
			name:   "imdb mobile + localized path",
			text:   "https://m.imdb.com/ru/title/tt31184028/?ref_=ext_shr",
			imdbID: "tt31184028",
		},
		{
			name: "kinopoisk series",
			text: "https://kinopoisk.ru/series/1234567?utm=x",
			kpID: 1234567,
		},
		{
			name:    "youtube shorts + comment",
			text:    "советовали в чате https://youtube.com/shorts/xYz123 https://www.kinopoisk.ru/film/326/ топ",
			kpID:    326,
			reel:    "https://youtube.com/shorts/xYz123",
			comment: "советовали в чате топ",
		},
		{
			name: "only reel",
			text: "https://www.tiktok.com/@user/video/7300000000000000000",
			reel: "https://www.tiktok.com/@user/video/7300000000000000000",
		},
		{
			name:    "no links",
			text:    "просто текст",
			comment: "просто текст",
		},
		{
			name:    "trailing punctuation stripped",
			text:    "(https://www.kinopoisk.ru/film/462682/).",
			kpID:    462682,
			comment: "().",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := Parse(tc.text)
			var kpID int64
			var imdbID string
			if p.Film != nil {
				kpID, imdbID = p.Film.KPID, p.Film.IMDbID
			}
			if kpID != tc.kpID || imdbID != tc.imdbID {
				t.Errorf("film: got kp=%d imdb=%q, want kp=%d imdb=%q", kpID, imdbID, tc.kpID, tc.imdbID)
			}
			if p.ReelURL != tc.reel {
				t.Errorf("reel: got %q, want %q", p.ReelURL, tc.reel)
			}
			if p.Comment != tc.comment {
				t.Errorf("comment: got %q, want %q", p.Comment, tc.comment)
			}
		})
	}
}
