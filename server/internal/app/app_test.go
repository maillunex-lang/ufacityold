package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"lunexgame/internal/store"
)

func srv(t *testing.T) http.Handler {
	d := t.TempDir()
	_ = os.MkdirAll(filepath.Join(d, "start"), 0o755)
	_ = os.WriteFile(filepath.Join(d, "start", "a.txt"), []byte("x"), 0o644)
	return New(Build{Version: "t", Commit: "abc"}, Config{AssetsDir: d})
}

func get(h http.Handler, p string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
	return w
}

func TestRoutes(t *testing.T) {
	h := srv(t)
	cases := map[string]int{
		"/LunexGame/":                    200,
		"/LunexGame":                     301,
		"/LunexGame/api/health":          200,
		"/LunexGame/api/version":         200,
		"/LunexGame/assets/start/a.txt":  200,
		"/LunexGame/api/assets/manifest": 200,
		"/LunexGame/assets/a.txt":        404,
		"/LunexGame/assets/":             404,
		"/LunexGame/admin/":              404,
		"/LunexGame/admin/x":             404,
		"/api/health":                    404,
		"/assets/a.txt":                  404,
		"/admin/":                        404,
		"/lunexgame/":                    404,
	}
	for p, want := range cases {
		if got := get(h, p).Code; got != want {
			t.Errorf("%s: %d, ожидалось %d", p, got, want)
		}
	}
}

func TestVersion(t *testing.T) {
	var b Build
	if err := json.Unmarshal(get(srv(t), "/LunexGame/api/version").Body.Bytes(), &b); err != nil || b.Commit != "abc" {
		t.Fatalf("%v %+v", err, b)
	}
}

func TestGuestFlow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(Build{Version: "t", Commit: "abc"}, Config{Store: st})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/LunexGame/api/auth/guest", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var r struct {
		AccountID int64  `json:"accountId"`
		Token     string `json:"token"`
	}
	json.Unmarshal(w.Body.Bytes(), &r)
	req := httptest.NewRequest("GET", "/LunexGame/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+r.Token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal("me", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/LunexGame/api/me", nil))
	if w.Code != 401 {
		t.Fatal("без токена", w.Code)
	}
	if get(h, "/LunexGame/api/health").Code != 200 {
		t.Fatal("health")
	}
	// лимит: 10 в минуту с одного адреса (одна уже потрачена)
	limited := false
	for i := 0; i < 12; i++ {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/LunexGame/api/auth/guest", nil))
		if w.Code == 429 {
			limited = true
		}
	}
	if !limited {
		t.Fatal("лимит не сработал")
	}
}
