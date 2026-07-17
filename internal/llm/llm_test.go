package llm

import "testing"

func TestParseGuess(t *testing.T) {
	cases := []struct {
		in        string
		title     string
		year      int
		wantError bool
	}{
		{`{"title":"День сурка","original_title":"Groundhog Day","year":1993}`, "День сурка", 1993, false},
		{"```json\n{\"title\":\"Драйв\",\"original_title\":\"Drive\",\"year\":2011}\n```", "Драйв", 2011, false},
		{`Вот ответ: {"title":"Матрица","original_title":"","year":0} — надеюсь, помог.`, "Матрица", 0, false},
		{`{"title":"","original_title":"Heat","year":1995}`, "Heat", 1995, false},
		{`{"title":"","original_title":"","year":0}`, "", 0, true},
		{`не json вообще`, "", 0, true},
	}
	for _, c := range cases {
		g, err := parseGuess(c.in)
		if c.wantError {
			if err == nil {
				t.Errorf("parseGuess(%q): ожидал ошибку, получил %+v", c.in, g)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGuess(%q): %v", c.in, err)
			continue
		}
		if g.Title != c.title || g.Year != c.year {
			t.Errorf("parseGuess(%q) = %+v, ожидал title=%q year=%d", c.in, g, c.title, c.year)
		}
	}
}
