package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"lunexgame/internal/admin"
	"lunexgame/internal/store"
)

// Админка полностью выключена, пока не задан хэш пароля владельца.
func TestAdminDisabledByDefault(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(Build{}, Config{Store: st})
	for _, p := range []string{"/LunexGame/admin/", "/LunexGame/admin", "/LunexGame/admin/api/me", "/LunexGame/admin/static/app.js", "/LunexGame/admin/api/login"} {
		for _, m := range []string{"GET", "POST"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(m, p, nil))
			if loc := w.Header().Get("Location"); (w.Code == 301 || w.Code == 307 || w.Code == 308) && loc != "" {
				// стандартное перенаправление /admin → /admin/: его цель обязана быть закрыта
				w = httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(m, loc, nil))
			}
			if w.Code != 404 && w.Code != 405 {
				t.Errorf("%s %s при выключенной админке: %d", m, p, w.Code)
			}
			if strings.Contains(w.Body.String(), "<html") {
				t.Errorf("%s %s раскрывает страницу админки", m, p)
			}
		}
	}
}

// Сквозной сценарий через настоящее приложение: вход → черновик → предпросмотр (реальный бой) → публикация.
func TestAdminEnabledEndToEnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash, err := admin.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	h := New(Build{}, Config{Store: st, AdminPasswordHash: hash})
	var cookie, csrf string
	call := func(method, path string, body any) (int, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		r := httptest.NewRequest(method, "/LunexGame/admin"+path, rd)
		r.Host = "tts-ufa.ru"
		r.Header.Set("Origin", "https://tts-ufa.ru")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: "lg_admin", Value: cookie})
			r.Header.Set("X-CSRF-Token", csrf)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		for _, c := range w.Result().Cookies() {
			if c.Name == "lg_admin" {
				cookie = c.Value
			}
		}
		return w.Code, out
	}
	if w := httptest.NewRecorder(); true {
		h.ServeHTTP(w, httptest.NewRequest("GET", "/LunexGame/admin/", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<html") {
			t.Fatal("страница админки недоступна:", w.Code)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/LunexGame/admin", nil))
		if w.Code != 301 {
			t.Fatal("перенаправление без слэша:", w.Code)
		}
	}
	code, out := call("POST", "/api/login", map[string]any{"password": "correct horse battery staple"})
	if code != 200 {
		t.Fatal(code, out)
	}
	csrf = out["csrf"].(string)
	energy := map[string]any{"max": 100, "perDealt": 0.08, "perTaken": 0.06, "perHealed": 0.05}
	if code, _ := call("PUT", "/api/content/energy/draft", map[string]any{"data": energy}); code != 200 {
		t.Fatal(code)
	}
	code, out = call("POST", "/api/content/energy/preview", map[string]any{})
	if code != 200 {
		t.Fatal(code, out)
	}
	raw, _ := json.Marshal(out)
	for _, want := range []string{"Демо-бой с черновиком", "Победитель", "Главных способностей"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("в предпросмотре нет %q: %s", want, raw)
		}
	}
	if code, out = call("POST", "/api/content/energy/publish", map[string]any{"note": "старт", "expectedVersion": 0}); code != 200 || out["version"] != float64(1) {
		t.Fatal(code, out)
	}
	// второй предпросмотр уже сравнивает с опубликованной версией
	energy["max"] = 50
	call("PUT", "/api/content/energy/draft", map[string]any{"data": energy})
	_, out = call("POST", "/api/content/energy/preview", map[string]any{})
	raw, _ = json.Marshal(out)
	if !strings.Contains(string(raw), "опубликованными значениями") {
		t.Errorf("нет сравнения с опубликованной версией: %s", raw)
	}
	// игровое API не затронуто
	if w := httptest.NewRecorder(); true {
		h.ServeHTTP(w, httptest.NewRequest("GET", "/LunexGame/api/health", nil))
		if w.Code != 200 {
			t.Fatal("health", w.Code)
		}
	}
}

// Испорченный хэш пароля не включает админку (закрыто по умолчанию), а хэш в кавычках работает после нормализации.
func TestAdminBadHashStaysClosed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(Build{}, Config{Store: st, AdminPasswordHash: "это не хэш"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/LunexGame/admin/", nil))
	if w.Code != 404 {
		t.Fatal("админка включилась с испорченным хэшем:", w.Code)
	}
	t.Setenv("LUNEXGAME_ADMIN_PASSWORD_HASH", "' $argon2id$v=19$m=65536,t=3,p=2$c2FsdHNhbHRzYWx0$aGFzaGhhc2hoYXNoaGFzaA '")
	_, cfg := FromEnv()
	if !admin.ValidHash(cfg.AdminPasswordHash) {
		t.Fatalf("значение в кавычках не нормализовано: %q", cfg.AdminPasswordHash)
	}
}
