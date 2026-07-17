// reelkino — TG-бот: сохраняет фильмы из рилсов (ссылка на рилс +
// Кинопоиск/IMDb, либо авто-определение по описанию через LLM),
// список — в Mini App.
package main

import (
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"reelkino"
	"reelkino/internal/kp"
	"reelkino/internal/links"
	"reelkino/internal/llm"
	"reelkino/internal/media"
	"reelkino/internal/store"
	"reelkino/internal/tg"
	"reelkino/internal/webapp"
)

const (
	pollTimeoutSec = 50
	maxCandidates  = 5
)

type pendingReel struct {
	url     string
	comment string
}

// proposal — предложенные пользователю кандидаты с Кинопоиска.
type proposal struct {
	reelURL string
	comment string
	cands   []kp.Movie
	idx     int
	chatID  int64
	msgID   int64
}

type app struct {
	tg      *tg.Client
	kp      *kp.Client
	llm     *llm.Client
	media   *media.Client
	st      *store.Store
	allowed map[int64]bool
	appURL  string

	mu        sync.Mutex
	pending   map[int64]pendingReel // непривязанные рилсы по пользователям
	proposals map[int64]*proposal   // активные предложения по пользователям
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("reelkino: ")

	botToken := mustEnv("TG_BOT_TOKEN")
	kpToken := mustEnv("KINOPOISK_API_TOKEN")
	zenKey := mustEnv("ZEN_API_KEY")
	zenBase := envDefault("ZEN_BASE_URL", "https://opencode.ai/zen/v1")
	zenModel := os.Getenv("ZEN_MODEL")
	allowed := parseIDs(mustEnv("ALLOWED_TG_IDS"))
	addr := envDefault("LISTEN_ADDR", "127.0.0.1:8091")
	dbPath := envDefault("DB_PATH", "movies.db")
	appURL := envDefault("WEBAPP_URL", "https://kino.vlle.ru/")
	mediaDir := envDefault("MEDIA_DIR", "media")
	ytdlp := envDefault("YTDLP_PATH", "yt-dlp")
	cookies := os.Getenv("YTDLP_COOKIES")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	md, err := media.New(ytdlp, mediaDir, cookies)
	if err != nil {
		log.Fatal(err)
	}

	a := &app{
		tg:        tg.New(botToken, pollTimeoutSec*time.Second),
		kp:        kp.New(kpToken),
		llm:       llm.New(zenBase, zenKey, zenModel),
		media:     md,
		st:        st,
		allowed:   allowed,
		appURL:    appURL,
		pending:   map[int64]pendingReel{},
		proposals: map[int64]*proposal{},
	}

	static, err := fs.Sub(reelkino.WebFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	webapp.New(st, botToken, allowed, md.Dir()).Register(mux, static)
	go func() {
		log.Printf("http на %s", addr)
		log.Fatal(http.ListenAndServe(addr, mux))
	}()

	if err := a.tg.SetChatMenuButton("Список", appURL); err != nil {
		log.Printf("setChatMenuButton: %v", err)
	}
	if err := a.tg.SetMyCommands([][2]string{
		{"list", "Последние сохранённые"},
		{"random", "Случайный из очереди"},
		{"app", "Открыть список"},
	}); err != nil {
		log.Printf("setMyCommands: %v", err)
	}

	go a.backfillReelMedia()

	log.Printf("poll loop, whitelist: %d id", len(allowed))
	a.pollLoop()
}

// backfillReelMedia докачивает рилсы фильмов, сохранённых до появления
// скачивания (или у которых загрузка не удалась). Последовательно,
// чтобы не плодить параллельные yt-dlp.
func (a *app) backfillReelMedia() {
	movies, err := a.st.NeedingReelMedia()
	if err != nil {
		log.Printf("backfill: %v", err)
		return
	}
	if len(movies) == 0 {
		return
	}
	log.Printf("backfill: %d рилсов без видео", len(movies))
	for _, m := range movies {
		video, thumb, err := a.media.Download(m.ReelURL, strconv.FormatInt(m.ID, 10))
		if err != nil {
			log.Printf("backfill %d (%s): %v", m.ID, m.Title, err)
			continue
		}
		if err := a.st.SetReelMedia(m.ID, video, thumb); err != nil {
			log.Printf("backfill %d: save: %v", m.ID, err)
			continue
		}
		log.Printf("backfill %d (%s): ok", m.ID, m.Title)
	}
}

func (a *app) pollLoop() {
	var offset int64
	for {
		updates, err := a.tg.GetUpdates(offset, pollTimeoutSec)
		if err != nil {
			log.Printf("getUpdates: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, u := range updates {
			offset = u.UpdateID + 1
			switch {
			case u.Message != nil:
				a.handleMessage(u.Message)
			case u.CallbackQuery != nil:
				a.handleCallback(u.CallbackQuery)
			}
		}
	}
}

func (a *app) handleMessage(m *tg.Message) {
	if m.From == nil || !a.allowed[m.From.ID] {
		log.Printf("denied: %d", userID(m.From))
		_, _ = a.tg.SendMessage(m.Chat.ID, "⛔ Доступ запрещён", nil)
		return
	}
	text := strings.TrimSpace(m.Text)
	switch {
	case text == "/start":
		a.sendHelp(m.Chat.ID)
	case text == "/list":
		a.sendList(m.Chat.ID, m.From.ID)
	case text == "/random":
		a.sendRandom(m.Chat.ID, m.From.ID)
	case text == "/app":
		_, _ = a.tg.SendMessage(m.Chat.ID, "Твой список:", a.appKeyboard())
	default:
		a.handleText(m)
	}
}

func (a *app) handleText(m *tg.Message) {
	chatID, userID := m.Chat.ID, m.From.ID
	p := links.Parse(m.Text)

	switch {
	case p.Film != nil:
		a.saveByFilmRef(chatID, userID, p)
	case p.ReelURL != "":
		// рилс без карточки фильма — пробуем определить фильм сами
		a.mu.Lock()
		a.pending[userID] = pendingReel{url: p.ReelURL, comment: p.Comment}
		a.mu.Unlock()
		go a.identifyReel(chatID, userID, p.ReelURL, p.Comment)
	case p.Comment != "":
		// просто текст — название или описание фильма
		go a.identifyText(chatID, userID, p.Comment)
	default:
		a.sendHelp(chatID)
	}
}

// saveByFilmRef — прежний флоу: явная ссылка на Кинопоиск/IMDb.
func (a *app) saveByFilmRef(chatID, userID int64, p links.Parsed) {
	// подвешенный рилс из предыдущего сообщения
	if p.ReelURL == "" {
		a.mu.Lock()
		if pr, ok := a.pending[userID]; ok {
			p.ReelURL = pr.url
			if p.Comment == "" {
				p.Comment = pr.comment
			}
			delete(a.pending, userID)
		}
		a.mu.Unlock()
	}

	var mv *kp.Movie
	var err error
	if p.Film.KPID != 0 {
		mv, err = a.kp.ByKPID(p.Film.KPID)
	} else {
		mv, err = a.kp.ByIMDbID(p.Film.IMDbID)
	}
	if err != nil {
		log.Printf("kp lookup: %v", err)
		_, _ = a.tg.SendMessage(chatID, "😔 Не смог получить данные фильма: "+err.Error(), nil)
		return
	}
	a.saveMovie(chatID, userID, mv, p.ReelURL, p.Comment)
}

// identifyReel вытаскивает описание рилса и запускает подбор фильма.
func (a *app) identifyReel(chatID, userID int64, reelURL, comment string) {
	msgID, _ := a.tg.SendMessage(chatID, "🔍 Смотрю рилс, пробую понять, что за фильм…", nil)
	var text string
	info, err := a.media.Probe(reelURL)
	if err != nil {
		log.Printf("probe %s: %v", reelURL, err)
	} else {
		text = strings.TrimSpace(info.Title + "\n" + info.Description)
	}
	if comment != "" {
		text = strings.TrimSpace(text + "\n" + comment)
	}
	if text == "" {
		_ = a.tg.EditMessageText(chatID, msgID,
			"🎞 Рилс поймал, но описание вытащить не смог.\n"+
				"Напиши название или опиши фильм словами — попробую найти. Или кинь ссылку на Кинопоиск/IMDb.", nil)
		return
	}
	a.propose(chatID, userID, msgID, text, reelURL, comment)
}

// identifyText — свободный текст: название или описание фильма.
func (a *app) identifyText(chatID, userID int64, text string) {
	a.mu.Lock()
	pr, hasPending := a.pending[userID]
	a.mu.Unlock()
	reelURL, comment := "", ""
	if hasPending {
		reelURL, comment = pr.url, pr.comment
	}
	msgID, _ := a.tg.SendMessage(chatID, "🔍 Ищу фильм…", nil)
	a.propose(chatID, userID, msgID, text, reelURL, comment)
}

// propose гоняет текст через LLM, ищет кандидатов на КП и показывает первого.
// statusMsgID — сообщение-статус, которое редактируем/удаляем.
func (a *app) propose(chatID, userID, statusMsgID int64, text, reelURL, comment string) {
	guess, err := a.llm.GuessMovie(text)
	if err != nil {
		log.Printf("llm guess: %v", err)
		_ = a.tg.EditMessageText(chatID, statusMsgID,
			"😔 Не смог понять, что за фильм. Уточни название или кинь ссылку на Кинопоиск/IMDb.", nil)
		return
	}

	cands, err := a.kp.Search(guess.Title, maxCandidates)
	if err != nil {
		log.Printf("kp search %q: %v", guess.Title, err)
	}
	if len(cands) == 0 && guess.OriginalTitle != "" && guess.OriginalTitle != guess.Title {
		cands, err = a.kp.Search(guess.OriginalTitle, maxCandidates)
		if err != nil {
			log.Printf("kp search %q: %v", guess.OriginalTitle, err)
		}
	}
	if len(cands) == 0 {
		_ = a.tg.EditMessageText(chatID, statusMsgID,
			fmt.Sprintf("🤷 На Кинопоиске не нашёл «%s». Уточни название или кинь ссылку на КП/IMDb.", guess.Title), nil)
		return
	}
	if guess.Year > 0 {
		cands = yearMatchesFirst(cands, guess.Year)
	}

	a.mu.Lock()
	a.proposals[userID] = &proposal{
		reelURL: reelURL,
		comment: comment,
		cands:   cands,
		chatID:  chatID,
	}
	a.mu.Unlock()

	_ = a.tg.DeleteMessage(chatID, statusMsgID)
	a.sendCandidate(userID)
}

// yearMatchesFirst поднимает кандидатов с совпавшим годом (±1) наверх,
// сохраняя порядок внутри групп.
func yearMatchesFirst(cands []kp.Movie, year int) []kp.Movie {
	out := make([]kp.Movie, 0, len(cands))
	var rest []kp.Movie
	for _, c := range cands {
		if c.Year >= year-1 && c.Year <= year+1 {
			out = append(out, c)
		} else {
			rest = append(rest, c)
		}
	}
	return append(out, rest...)
}

// sendCandidate показывает текущего кандидата из proposal.
func (a *app) sendCandidate(userID int64) {
	a.mu.Lock()
	pr := a.proposals[userID]
	if pr == nil {
		a.mu.Unlock()
		return
	}
	c := pr.cands[pr.idx]
	total := len(pr.cands)
	num := pr.idx + 1
	a.mu.Unlock()

	var b strings.Builder
	b.WriteString("🤔 Похоже, это:\n")
	b.WriteString(c.Title())
	if c.Year > 0 {
		fmt.Fprintf(&b, " (%d)", c.Year)
	}
	var ratings []string
	if c.Rating.KP > 0 {
		ratings = append(ratings, fmt.Sprintf("КП %.1f", c.Rating.KP))
	}
	if c.Rating.IMDb > 0 {
		ratings = append(ratings, fmt.Sprintf("IMDb %.1f", c.Rating.IMDb))
	}
	if len(ratings) > 0 {
		b.WriteString("\n⭐ " + strings.Join(ratings, " · "))
	}
	fmt.Fprintf(&b, "\nВариант %d из %d", num, total)

	kb := &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{
			{Text: "✅ Он", CallbackData: "p:ok"},
			{Text: "➡️ Не тот", CallbackData: "p:next"},
		},
		{{Text: "✖️ Отмена", CallbackData: "p:no"}},
	}}

	var msgID int64
	var err error
	if c.Poster.URL != "" {
		msgID, err = a.tg.SendPhoto(pr.chatID, c.Poster.URL, b.String(), kb)
	}
	if c.Poster.URL == "" || err != nil {
		msgID, err = a.tg.SendMessage(pr.chatID, b.String(), kb)
	}
	if err != nil {
		log.Printf("send candidate: %v", err)
		return
	}
	a.mu.Lock()
	pr.msgID = msgID
	a.mu.Unlock()
}

// saveMovie сохраняет фильм и шлёт карточку; медиа рилса качается фоном.
func (a *app) saveMovie(chatID, userID int64, mv *kp.Movie, reelURL, comment string) {
	rec := &store.Movie{
		TGUserID:   userID,
		Title:      mv.Title(),
		Year:       mv.Year,
		Kind:       mv.Type,
		PosterURL:  mv.Poster.URL,
		RatingKP:   mv.Rating.KP,
		RatingIMDb: mv.Rating.IMDb,
		KPID:       mv.ID,
		IMDbID:     mv.ExternalID.IMDb,
		KPURL:      fmt.Sprintf("https://www.kinopoisk.ru/film/%d/", mv.ID),
		ReelURL:    reelURL,
		Comment:    comment,
	}
	if rec.IMDbID != "" {
		rec.IMDbURL = "https://www.imdb.com/title/" + rec.IMDbID + "/"
	}
	created, err := a.st.Add(rec)
	if err != nil {
		log.Printf("store add: %v", err)
		_, _ = a.tg.SendMessage(chatID, "😔 Не смог сохранить, смотри логи", nil)
		return
	}

	header := "🎬 Добавил: "
	if !created {
		header = "♻️ Уже в списке, обновил: "
	}
	caption := movieCaption(rec, header)
	kb := a.movieKeyboard(rec.ID)
	if rec.PosterURL != "" {
		if _, err := a.tg.SendPhoto(chatID, rec.PosterURL, caption, kb); err == nil {
			a.fetchReelMedia(rec)
			return
		}
	}
	_, _ = a.tg.SendMessage(chatID, caption, kb)
	a.fetchReelMedia(rec)
}

// fetchReelMedia фоном качает видео рилса и превью для миниаппа.
func (a *app) fetchReelMedia(rec *store.Movie) {
	if rec.ReelURL == "" {
		return
	}
	id, url := rec.ID, rec.ReelURL
	go func() {
		video, thumb, err := a.media.Download(url, strconv.FormatInt(id, 10))
		if err != nil {
			log.Printf("reel download %d: %v", id, err)
			return
		}
		if err := a.st.SetReelMedia(id, video, thumb); err != nil {
			log.Printf("reel media save %d: %v", id, err)
		}
	}()
}

func movieCaption(m *store.Movie, header string) string {
	var b strings.Builder
	b.WriteString(header)
	b.WriteString(m.Title)
	if m.Year > 0 {
		fmt.Fprintf(&b, " (%d)", m.Year)
	}
	var ratings []string
	if m.RatingKP > 0 {
		ratings = append(ratings, fmt.Sprintf("КП %.1f", m.RatingKP))
	}
	if m.RatingIMDb > 0 {
		ratings = append(ratings, fmt.Sprintf("IMDb %.1f", m.RatingIMDb))
	}
	if len(ratings) > 0 {
		b.WriteString("\n⭐ " + strings.Join(ratings, " · "))
	}
	if m.Comment != "" {
		b.WriteString("\n💬 " + m.Comment)
	}
	return b.String()
}

func (a *app) movieKeyboard(id int64) *tg.InlineKeyboardMarkup {
	sid := strconv.FormatInt(id, 10)
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{
			{Text: "✅ Посмотрел", CallbackData: "w:" + sid},
			{Text: "🗑 Удалить", CallbackData: "d:" + sid},
		},
		{
			{Text: "📱 Открыть список", WebApp: &tg.WebAppInfo{URL: a.appURL}},
		},
	}}
}

func (a *app) appKeyboard() *tg.InlineKeyboardMarkup {
	return &tg.InlineKeyboardMarkup{InlineKeyboard: [][]tg.InlineKeyboardButton{
		{{Text: "📱 Открыть список", WebApp: &tg.WebAppInfo{URL: a.appURL}}},
	}}
}

func (a *app) handleCallback(q *tg.CallbackQuery) {
	if q.From == nil || !a.allowed[q.From.ID] {
		_ = a.tg.AnswerCallbackQuery(q.ID, "⛔")
		return
	}
	action, arg, ok := strings.Cut(q.Data, ":")
	if !ok {
		_ = a.tg.AnswerCallbackQuery(q.ID, "не понял")
		return
	}
	if action == "p" {
		a.handleProposalCallback(q, arg)
		return
	}
	id, err := strconv.ParseInt(arg, 10, 64)
	if err != nil {
		_ = a.tg.AnswerCallbackQuery(q.ID, "не понял")
		return
	}
	switch action {
	case "w":
		if err := a.st.SetStatus(q.From.ID, id, store.StatusWatched); err != nil {
			_ = a.tg.AnswerCallbackQuery(q.ID, "ошибка: "+err.Error())
			return
		}
		_ = a.tg.AnswerCallbackQuery(q.ID, "✅ Отметил просмотренным")
	case "d":
		m, err := a.st.Get(q.From.ID, id)
		if err != nil {
			log.Printf("delete get %d: %v", id, err)
		}
		if err := a.st.Delete(q.From.ID, id); err != nil {
			_ = a.tg.AnswerCallbackQuery(q.ID, "ошибка: "+err.Error())
			return
		}
		if m != nil {
			a.media.Remove(m.ReelVideo, m.ReelThumb)
		}
		if q.Message != nil {
			_ = a.tg.DeleteMessage(q.Message.Chat.ID, q.Message.MessageID)
		}
		_ = a.tg.AnswerCallbackQuery(q.ID, "🗑 Удалено")
	default:
		_ = a.tg.AnswerCallbackQuery(q.ID, "не понял")
	}
}

func (a *app) handleProposalCallback(q *tg.CallbackQuery, arg string) {
	userID := q.From.ID
	a.mu.Lock()
	pr := a.proposals[userID]
	a.mu.Unlock()
	if pr == nil {
		_ = a.tg.AnswerCallbackQuery(q.ID, "предложение устарело")
		return
	}

	switch arg {
	case "ok":
		_ = a.tg.AnswerCallbackQuery(q.ID, "🎬")
		a.mu.Lock()
		delete(a.proposals, userID)
		if pr.reelURL != "" {
			if p, ok := a.pending[userID]; ok && p.url == pr.reelURL {
				delete(a.pending, userID)
			}
		}
		a.mu.Unlock()
		_ = a.tg.DeleteMessage(pr.chatID, pr.msgID)
		go func() {
			// полная карточка: search отдаёт урезанные данные
			mv, err := a.kp.ByKPID(pr.cands[pr.idx].ID)
			if err != nil {
				log.Printf("kp by id: %v", err)
				c := pr.cands[pr.idx]
				mv = &c
			}
			a.saveMovie(pr.chatID, userID, mv, pr.reelURL, pr.comment)
		}()
	case "next":
		a.mu.Lock()
		pr.idx++
		done := pr.idx >= len(pr.cands)
		if done {
			delete(a.proposals, userID)
		}
		msgID := pr.msgID
		a.mu.Unlock()
		_ = a.tg.AnswerCallbackQuery(q.ID, "")
		_ = a.tg.DeleteMessage(pr.chatID, msgID)
		if done {
			_, _ = a.tg.SendMessage(pr.chatID,
				"Варианты кончились 🤷 Напиши название точнее или кинь ссылку на Кинопоиск/IMDb.", nil)
			return
		}
		a.sendCandidate(userID)
	case "no":
		_ = a.tg.AnswerCallbackQuery(q.ID, "")
		a.mu.Lock()
		delete(a.proposals, userID)
		a.mu.Unlock()
		_ = a.tg.DeleteMessage(pr.chatID, pr.msgID)
		_, _ = a.tg.SendMessage(pr.chatID,
			"Ок. Напиши название/описание точнее или кинь ссылку на Кинопоиск/IMDb.", nil)
	default:
		_ = a.tg.AnswerCallbackQuery(q.ID, "не понял")
	}
}

func (a *app) sendHelp(chatID int64) {
	_, _ = a.tg.SendMessage(chatID,
		"Кидай мне ссылку на рилс/тикток — попробую сам понять, что за фильм, и предложу варианты.\n\n"+
			"Можно и по-старому: рилс + ссылка на Кинопоиск/IMDb (одним сообщением или двумя подряд).\n"+
			"А можно просто написать название или описать фильм словами.\n\n"+
			"/list — последние сохранённые\n/random — случайный из очереди\n/app — открыть список",
		a.appKeyboard())
}

func (a *app) sendList(chatID, userID int64) {
	movies, err := a.st.List(userID, "", 10)
	if err != nil {
		log.Printf("list: %v", err)
		_, _ = a.tg.SendMessage(chatID, "😔 Ошибка чтения списка", nil)
		return
	}
	if len(movies) == 0 {
		_, _ = a.tg.SendMessage(chatID, "Список пуст. Кидай ссылки!", nil)
		return
	}
	var b strings.Builder
	b.WriteString("Последние:\n")
	for _, m := range movies {
		mark := "▫️"
		if m.Status == store.StatusWatched {
			mark = "✅"
		}
		fmt.Fprintf(&b, "%s %s", mark, m.Title)
		if m.Year > 0 {
			fmt.Fprintf(&b, " (%d)", m.Year)
		}
		b.WriteString("\n")
	}
	_, _ = a.tg.SendMessage(chatID, b.String(), a.appKeyboard())
}

func (a *app) sendRandom(chatID, userID int64) {
	m, err := a.st.Random(userID)
	if err != nil {
		log.Printf("random: %v", err)
		_, _ = a.tg.SendMessage(chatID, "😔 Ошибка", nil)
		return
	}
	if m == nil {
		_, _ = a.tg.SendMessage(chatID, "Очередь пуста — нечего советовать 🤷", nil)
		return
	}
	caption := movieCaption(m, "🎲 ")
	kb := a.movieKeyboard(m.ID)
	if m.PosterURL != "" {
		if _, err := a.tg.SendPhoto(chatID, m.PosterURL, caption, kb); err == nil {
			return
		}
	}
	_, _ = a.tg.SendMessage(chatID, caption, kb)
}

func userID(u *tg.User) int64 {
	if u == nil {
		return 0
	}
	return u.ID
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("не задан env %s", key)
	}
	return v
}

func envDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseIDs(s string) map[int64]bool {
	out := map[int64]bool{}
	for part := range strings.SplitSeq(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil {
			log.Fatalf("ALLOWED_TG_IDS: плохой id %q", part)
		}
		out[id] = true
	}
	if len(out) == 0 {
		log.Fatal("ALLOWED_TG_IDS пуст")
	}
	return out
}
