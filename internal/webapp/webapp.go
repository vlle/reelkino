// Package webapp — HTTP-обвязка Mini App: статика + JSON API
// с авторизацией по Telegram initData.
package webapp

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"reelkino/internal/store"
)

const initDataMaxAge = 24 * time.Hour

type Server struct {
	st       *store.Store
	botToken string
	allowed  map[int64]bool
	mediaDir string
}

func New(st *store.Store, botToken string, allowed map[int64]bool, mediaDir string) *Server {
	return &Server{st: st, botToken: botToken, allowed: allowed, mediaDir: mediaDir}
}

// Register вешает маршруты миниаппа на mux (апп живёт в корне домена).
func (s *Server) Register(mux *http.ServeMux, static fs.FS) {
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/list", s.auth(s.handleList))
	mux.HandleFunc("PATCH /api/movie/{id}", s.auth(s.handleUpdate))
	mux.HandleFunc("DELETE /api/movie/{id}", s.auth(s.handleDelete))
	mux.HandleFunc("GET /api/media/{name}", s.auth(s.handleMedia))
}

type handler func(w http.ResponseWriter, r *http.Request, userID int64)

func (s *Server) auth(h handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// query-параметр — для <img>/<video>, где заголовок не выставить
		initData := r.Header.Get("X-Telegram-Init-Data")
		if initData == "" {
			initData = r.URL.Query().Get("initData")
		}
		userID, err := validateInitData(initData, s.botToken, initDataMaxAge)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if !s.allowed[userID] {
			jsonError(w, http.StatusForbidden, "forbidden")
			return
		}
		h(w, r, userID)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request, userID int64) {
	status := r.URL.Query().Get("status")
	if status != "" && status != store.StatusWant && status != store.StatusWatched {
		jsonError(w, http.StatusBadRequest, "bad status")
		return
	}
	movies, err := s.st.List(userID, status, 0)
	if err != nil {
		log.Printf("webapp: list: %v", err)
		jsonError(w, http.StatusInternalServerError, "internal")
		return
	}
	if movies == nil {
		movies = []*store.Movie{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request, userID int64) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad id")
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "bad body")
		return
	}
	if err := s.st.SetStatus(userID, id, body.Status); err != nil {
		log.Printf("webapp: update %d: %v", id, err)
		jsonError(w, http.StatusBadRequest, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, userID int64) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "bad id")
		return
	}
	m, err := s.st.Get(userID, id)
	if err != nil {
		log.Printf("webapp: delete get %d: %v", id, err)
	}
	if err := s.st.Delete(userID, id); err != nil {
		log.Printf("webapp: delete %d: %v", id, err)
		jsonError(w, http.StatusBadRequest, "delete failed")
		return
	}
	if m != nil {
		removeMediaFiles(s.mediaDir, m.ReelVideo, m.ReelThumb)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// имена файлов задаёт бот: "<movieID>.<ext>"
var mediaNameRe = regexp.MustCompile(`^(\d+)\.[a-z0-9]+$`)

// handleMedia отдаёт скачанный рилс/превью; файл доступен только владельцу фильма.
func (s *Server) handleMedia(w http.ResponseWriter, r *http.Request, userID int64) {
	name := r.PathValue("name")
	match := mediaNameRe.FindStringSubmatch(name)
	if match == nil {
		jsonError(w, http.StatusBadRequest, "bad name")
		return
	}
	id, _ := strconv.ParseInt(match[1], 10, 64)
	m, err := s.st.Get(userID, id)
	if err != nil {
		log.Printf("webapp: media get %d: %v", id, err)
		jsonError(w, http.StatusInternalServerError, "internal")
		return
	}
	if m == nil || (m.ReelVideo != name && m.ReelThumb != name) {
		jsonError(w, http.StatusNotFound, "not found")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeFile(w, r, filepath.Join(s.mediaDir, name))
}

func removeMediaFiles(dir string, names ...string) {
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "/\\") {
			continue
		}
		_ = os.Remove(filepath.Join(dir, n))
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func jsonError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
