package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lunexgame/internal/content"
	"lunexgame/internal/store"
)

const testPassword = "correct horse battery staple"

type env struct {
	t      *testing.T
	svc    *Service
	srv    http.Handler
	now    time.Time
	cookie string
	csrf   string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)}
	e.svc = New(Config{
		DB: st.DB, PasswordHash: hash,
		ClientIP: func(*http.Request) string { return "203.0.113.7" },
		Now:      func() time.Time { return e.now },
		Preview: func(kind string, draft, published json.RawMessage) (map[string]any, error) {
			return map[string]any{"kind": kind, "hasPublished": published != nil}, nil
		},
	})
	mux := http.NewServeMux()
	mux.Handle(Prefix+"/", e.svc.Handler())
	e.srv = mux
	return e
}

type resp struct {
	code int
	body map[string]any
	raw  string
	hdr  http.Header
}

func (e *env) do(method, path string, body any, mod func(*http.Request)) resp {
	var rd *bytes.Reader
	if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, Prefix+path, rd)
	r.Host = "tts-ufa.ru"
	if mod != nil {
		mod(r)
	}
	w := httptest.NewRecorder()
	e.srv.ServeHTTP(w, r)
	out := resp{code: w.Code, raw: w.Body.String(), hdr: w.Header()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

// authed — запрос с cookie и CSRF-токеном.
func (e *env) authed(method, path string, body any) resp {
	return e.do(method, path, body, func(r *http.Request) {
		if e.cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: e.cookie})
		}
		if e.csrf != "" {
			r.Header.Set("X-CSRF-Token", e.csrf)
		}
		if method != "GET" {
			r.Header.Set("Origin", "https://tts-ufa.ru")
			r.Header.Set("Sec-Fetch-Site", "same-origin")
		}
	})
}

func (e *env) login() {
	e.t.Helper()
	r := e.do("POST", "/api/login", map[string]any{"password": testPassword}, nil)
	if r.code != 200 {
		e.t.Fatalf("вход: %d %s", r.code, r.raw)
	}
	e.csrf = r.body["csrf"].(string)
	for _, c := range r.hdr.Values("Set-Cookie") {
		if strings.HasPrefix(c, cookieName+"=") {
			e.cookie = strings.TrimPrefix(strings.SplitN(c, ";", 2)[0], cookieName+"=")
		}
	}
	if e.cookie == "" {
		e.t.Fatal("cookie сессии не выдана")
	}
}

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatal(h)
	}
	if !VerifyPassword(h, testPassword) || VerifyPassword(h, testPassword+"x") || VerifyPassword(h, "") {
		t.Fatal("проверка пароля")
	}
	h2, _ := HashPassword(testPassword)
	if h == h2 {
		t.Fatal("соль должна быть случайной")
	}
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("короткий пароль должен отклоняться")
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=1,t=1,p=1$aaaa$bbbb", "$bcrypt$x$y$z$w", strings.Replace(h, "m=65536", "m=999999999", 1),
		strings.Replace(h, "t=3", "t=0", 1), h[:len(h)-4]} {
		if VerifyPassword(bad, testPassword) {
			t.Errorf("испорченный хэш принят: %q", bad)
		}
	}
}

func TestDisabledWithoutHash(t *testing.T) {
	if New(Config{PasswordHash: ""}) != nil {
		t.Fatal("без хэша пароля админка не должна создаваться")
	}
}

func TestAuthRequired(t *testing.T) {
	e := newEnv(t)
	for _, p := range []string{"/api/kinds", "/api/me", "/api/content/energy", "/api/audit", "/api/content/energy/versions/1"} {
		if r := e.do("GET", p, nil, nil); r.code != 401 {
			t.Errorf("GET %s без входа: %d", p, r.code)
		}
	}
	// чужой/испорченный токен
	for _, tok := range []string{"abc", strings.Repeat("0", 64)} {
		r := e.do("GET", "/api/me", nil, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: tok}) })
		if r.code != 401 {
			t.Errorf("токен %q принят: %d", tok, r.code)
		}
	}
}

func TestLoginCookieAndHeaders(t *testing.T) {
	e := newEnv(t)
	if r := e.do("POST", "/api/login", map[string]any{"password": "wrong password!!"}, nil); r.code != 401 {
		t.Fatal("неверный пароль:", r.code)
	}
	r := e.do("POST", "/api/login", map[string]any{"password": testPassword}, nil)
	if r.code != 200 {
		t.Fatal(r.code, r.raw)
	}
	c := strings.Join(r.hdr.Values("Set-Cookie"), ";")
	for _, want := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Path=" + Prefix} {
		if !strings.Contains(c, want) {
			t.Errorf("в cookie нет %s: %s", want, c)
		}
	}
	if !strings.Contains(r.hdr.Get("Content-Security-Policy"), "default-src 'none'") || r.hdr.Get("Cache-Control") != "no-store" ||
		r.hdr.Get("X-Frame-Options") != "DENY" {
		t.Errorf("заголовки безопасности: %v", r.hdr)
	}
	// токен сессии в БД — только хэш
	e.login()
	var n int
	if err := e.svc.DB.QueryRow(`SELECT count(*) FROM admin_sessions WHERE token_hash=?`, e.cookie).Scan(&n); err != nil || n != 0 {
		t.Fatal("в БД хранится сам токен")
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t)
	codes := map[int]int{}
	for i := 0; i < 8; i++ {
		codes[e.do("POST", "/api/login", map[string]any{"password": "bad password 123"}, nil).code]++
	}
	if codes[401] != 5 || codes[429] != 3 {
		t.Fatalf("ожидалось 5×401 и 3×429: %v", codes)
	}
	// после блокировки даже верный пароль не принимается, пока окно не пройдёт
	if r := e.do("POST", "/api/login", map[string]any{"password": testPassword}, nil); r.code != 429 {
		t.Fatal("верный пароль во время блокировки:", r.code)
	}
}

func TestLoginRejectsBadInput(t *testing.T) {
	e := newEnv(t)
	for name, body := range map[string]any{"не JSON": "{", "огромный пароль": map[string]any{"password": strings.Repeat("a", 2000)}} {
		if r := e.do("POST", "/api/login", body, nil); r.code != 400 {
			t.Errorf("%s: %d", name, r.code)
		}
	}
	r := e.do("POST", "/api/login", map[string]any{"password": testPassword}, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") })
	if r.code != 403 {
		t.Errorf("вход с чужого источника: %d", r.code)
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t)
	e.login()
	put := func(mod func(*http.Request)) int {
		return e.do("PUT", "/api/content/energy/draft", map[string]any{"data": map[string]any{"max": 1}}, func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: e.cookie})
			mod(r)
		}).code
	}
	if c := put(func(r *http.Request) {}); c != 403 {
		t.Errorf("без CSRF-токена: %d", c)
	}
	if c := put(func(r *http.Request) { r.Header.Set("X-CSRF-Token", "nope") }); c != 403 {
		t.Errorf("с неверным токеном: %d", c)
	}
	if c := put(func(r *http.Request) {
		r.Header.Set("X-CSRF-Token", e.csrf)
		r.Header.Set("Origin", "https://evil.example")
	}); c != 403 {
		t.Errorf("чужой Origin: %d", c)
	}
	if c := put(func(r *http.Request) {
		r.Header.Set("X-CSRF-Token", e.csrf)
		r.Header.Set("Sec-Fetch-Site", "cross-site")
	}); c != 403 {
		t.Errorf("cross-site: %d", c)
	}
	if c := put(func(r *http.Request) {
		r.Header.Set("X-CSRF-Token", e.csrf)
		r.Header.Set("Origin", "https://tts-ufa.ru")
	}); c != 200 {
		t.Errorf("корректный запрос: %d", c)
	}
}

func TestSessionExpiryAndLogout(t *testing.T) {
	e := newEnv(t)
	e.login()
	if r := e.authed("GET", "/api/me", nil); r.code != 200 {
		t.Fatal(r.code)
	}
	e.now = e.now.Add(sessionTTL - time.Minute)
	if r := e.authed("GET", "/api/me", nil); r.code != 200 {
		t.Fatal("сессия должна жить до истечения срока")
	}
	e.now = e.now.Add(2 * time.Minute)
	if r := e.authed("GET", "/api/me", nil); r.code != 401 {
		t.Fatal("просроченная сессия принята:", r.code)
	}
	e.login()
	if r := e.authed("POST", "/api/logout", nil); r.code != 200 {
		t.Fatal(r.code)
	}
	if r := e.authed("GET", "/api/me", nil); r.code != 401 {
		t.Fatal("сессия жива после выхода")
	}
}

func TestContentWorkflow(t *testing.T) {
	e := newEnv(t)
	e.login()
	good := map[string]any{"max": 100, "perDealt": 0.08, "perTaken": 0.06, "perHealed": 0.05}
	good2 := map[string]any{"max": 150, "perDealt": 0.1, "perTaken": 0.06, "perHealed": 0.05}

	if r := e.authed("GET", "/api/kinds", nil); r.code != 200 || len(r.body["kinds"].([]any)) != len(content.Kinds) {
		t.Fatal(r.code, r.raw)
	}
	if r := e.authed("GET", "/api/content/nope", nil); r.code != 404 {
		t.Fatal("неизвестный вид:", r.code)
	}
	r := e.authed("GET", "/api/content/energy", nil)
	if r.code != 200 || r.body["published"] != nil || r.body["draft"] != nil {
		t.Fatal(r.raw)
	}
	// черновик с ошибками сохраняется, но проверка и публикация его не пропускают
	r = e.authed("PUT", "/api/content/energy/draft", map[string]any{"data": map[string]any{"max": 0}})
	if r.code != 200 || len(r.body["errors"].([]any)) == 0 {
		t.Fatal(r.raw)
	}
	if r := e.authed("POST", "/api/content/energy/preview", map[string]any{}); r.code != 422 {
		t.Fatal("предпросмотр невалидного черновика:", r.code)
	}
	if r := e.authed("POST", "/api/content/energy/publish", map[string]any{"expectedVersion": 0}); r.code != 422 {
		t.Fatal("публикация невалидного черновика:", r.code)
	}
	// исправляем, смотрим, публикуем
	e.authed("PUT", "/api/content/energy/draft", map[string]any{"data": good})
	if r := e.authed("POST", "/api/content/energy/validate", map[string]any{}); r.body["valid"] != true {
		t.Fatal(r.raw)
	}
	if r := e.authed("POST", "/api/content/energy/preview", map[string]any{}); r.code != 200 || r.body["preview"] == nil {
		t.Fatal(r.raw)
	}
	if r := e.authed("POST", "/api/content/energy/publish", map[string]any{"note": "x"}); r.code != 400 {
		t.Fatal("публикация без expectedVersion:", r.code)
	}
	if r := e.authed("POST", "/api/content/energy/publish", map[string]any{"note": "первая", "expectedVersion": 0}); r.code != 200 || r.body["version"] != float64(1) {
		t.Fatal(r.raw)
	}
	if r := e.authed("GET", "/api/content/energy", nil); r.body["draft"] != nil {
		t.Fatal("черновик должен исчезнуть после публикации")
	}
	// устаревшее ожидание → конфликт
	if r := e.authed("POST", "/api/content/energy/publish", map[string]any{"data": good2, "expectedVersion": 0}); r.code != 409 {
		t.Fatal("ожидался конфликт:", r.code)
	}
	if r := e.authed("POST", "/api/content/energy/publish", map[string]any{"data": good2, "note": "вторая", "expectedVersion": 1}); r.body["version"] != float64(2) {
		t.Fatal(r.raw)
	}
	// возврат к версии 1 = новая версия 3
	r = e.authed("POST", "/api/content/energy/rollback", map[string]any{"toVersion": 1, "note": "вернули", "expectedVersion": 2})
	if r.code != 200 || r.body["version"] != float64(3) {
		t.Fatal(r.raw)
	}
	if r := e.authed("POST", "/api/content/energy/rollback", map[string]any{"toVersion": 99, "expectedVersion": 3}); r.code != 404 {
		t.Fatal("откат на несуществующую версию:", r.code)
	}
	if r := e.authed("POST", "/api/content/energy/rollback", map[string]any{"toVersion": 1, "expectedVersion": 2}); r.code != 409 {
		t.Fatal("устаревший откат:", r.code)
	}
	r = e.authed("GET", "/api/content/energy", nil)
	pub := r.body["published"].(map[string]any)
	hist := r.body["history"].([]any)
	if pub["version"] != float64(3) || pub["source"] != "rollback" || len(hist) != 3 {
		t.Fatal(r.raw)
	}
	r = e.authed("GET", "/api/content/energy/versions/1", nil)
	if r.code != 200 || !strings.Contains(r.raw, `"max":100`) {
		t.Fatal(r.raw)
	}
	// журнал содержит ключевые действия
	r = e.authed("GET", "/api/audit", nil)
	actions := []string{}
	for _, row := range r.body["rows"].([]any) {
		actions = append(actions, row.(map[string]any)["action"].(string))
	}
	joined := strings.Join(actions, ",")
	for _, want := range []string{"login", "draft_save", "publish", "rollback", "preview"} {
		if !strings.Contains(joined, want) {
			t.Errorf("в журнале нет %q: %s", want, joined)
		}
	}
}

func TestBodyLimitAndBadJSON(t *testing.T) {
	e := newEnv(t)
	e.login()
	huge := `{"data":"` + strings.Repeat("a", maxBody+10) + `"}`
	if r := e.authed("PUT", "/api/content/energy/draft", huge); r.code != 400 {
		t.Errorf("слишком большое тело: %d", r.code)
	}
	if r := e.authed("PUT", "/api/content/energy/draft", `{"data": }`); r.code != 400 {
		t.Errorf("битый JSON: %d", r.code)
	}
	if r := e.authed("PUT", "/api/content/energy/draft", map[string]any{}); r.code != 400 {
		t.Errorf("нет данных: %d", r.code)
	}
	if r := e.authed("PUT", "/api/content/energy/draft", `{"data":{"max":1}} {"x":1}`); r.code != 400 {
		t.Errorf("лишние данные: %d", r.code)
	}
}

func TestStaticAndUnknownRoutes(t *testing.T) {
	e := newEnv(t)
	r := e.do("GET", "/", nil, nil)
	if r.code != 200 || !strings.Contains(r.raw, "<html") || !strings.Contains(r.hdr.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal(r.code, r.hdr)
	}
	if r := e.do("GET", "/secret", nil, nil); r.code != 404 {
		t.Errorf("неизвестный путь: %d", r.code)
	}
	if r := e.do("GET", "/static/../../etc/passwd", nil, nil); r.code == 200 {
		t.Error("выход за пределы статики")
	}
}

func TestHashFormatHelpers(t *testing.T) {
	h, _ := HashPassword(testPassword)
	if !ValidHash(h) || ValidHash("") || ValidHash("'"+h+"'") {
		t.Fatal("ValidHash")
	}
	for _, v := range []string{h, "'" + h + "'", `"` + h + `"`, "  " + h + "\n", "' " + h + " '"} {
		if got := NormalizeHash(v); !ValidHash(got) {
			t.Errorf("NormalizeHash(%q) не дал корректный хэш", v)
		}
	}
	if NormalizeHash("'abc") != "'abc" {
		t.Error("непарная кавычка не должна срезаться")
	}
}
