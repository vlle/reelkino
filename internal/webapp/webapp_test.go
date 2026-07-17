package webapp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"reelkino/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	static := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>reelkino</title>")},
		"app.js":     {Data: []byte("'use strict';")},
	}
	mux := http.NewServeMux()
	New(st, testToken, map[int64]bool{1919118841: true}, t.TempDir()).Register(mux, static)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, st
}

func initData(t *testing.T, userID string) string {
	fields := validFields(time.Now().Unix())
	fields["user"] = `{"id":` + userID + `,"first_name":"x"}`
	return sign(t, fields)
}

func do(t *testing.T, method, url, init, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if init != "" {
		req.Header.Set("X-Telegram-Init-Data", init)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestHTTP(t *testing.T) {
	srv, st := newTestServer(t)
	mv := &store.Movie{TGUserID: 1919118841, Title: "Матрица", Year: 1999, KPID: 301}
	if _, err := st.Add(mv); err != nil {
		t.Fatal(err)
	}

	t.Run("healthz open", func(t *testing.T) {
		if code := do(t, "GET", srv.URL+"/healthz", "", "").StatusCode; code != 200 {
			t.Fatalf("healthz: %d", code)
		}
	})
	t.Run("static served", func(t *testing.T) {
		if code := do(t, "GET", srv.URL+"/", "", "").StatusCode; code != 200 {
			t.Fatalf("index: %d", code)
		}
		if code := do(t, "GET", srv.URL+"/app.js", "", "").StatusCode; code != 200 {
			t.Fatalf("app.js: %d", code)
		}
	})
	t.Run("api without auth -> 401", func(t *testing.T) {
		if code := do(t, "GET", srv.URL+"/api/list", "", "").StatusCode; code != 401 {
			t.Fatalf("got %d", code)
		}
	})
	t.Run("api foreign user -> 403", func(t *testing.T) {
		if code := do(t, "GET", srv.URL+"/api/list", initData(t, "42"), "").StatusCode; code != 403 {
			t.Fatalf("got %d", code)
		}
	})
	t.Run("list / patch / delete", func(t *testing.T) {
		init := initData(t, "1919118841")
		resp := do(t, "GET", srv.URL+"/api/list", init, "")
		var out struct {
			Movies []*store.Movie `json:"movies"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if len(out.Movies) != 1 || out.Movies[0].Title != "Матрица" {
			t.Fatalf("list: %+v", out.Movies)
		}

		id := out.Movies[0].ID
		url := srv.URL + "/api/movie/" + jsonNum(id)
		if code := do(t, "PATCH", url, init, `{"status":"watched"}`).StatusCode; code != 200 {
			t.Fatalf("patch: %d", code)
		}
		got, _ := st.Get(1919118841, id)
		if got.Status != store.StatusWatched {
			t.Fatalf("status: %s", got.Status)
		}
		if code := do(t, "DELETE", url, init, "").StatusCode; code != 200 {
			t.Fatalf("delete: %d", code)
		}
		if got, _ := st.Get(1919118841, id); got != nil {
			t.Fatal("не удалился")
		}
	})
}

func jsonNum(id int64) string {
	b, _ := json.Marshal(id)
	return string(b)
}
