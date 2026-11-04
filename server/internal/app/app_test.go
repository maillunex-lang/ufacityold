package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestBattleAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := New(Build{}, Config{Store: st})
	_, tok1, _ := st.CreateGuest()
	_, tok2, _ := st.CreateGuest()
	call := func(method, path, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil)
		if tok != "" {
			r.Header.Set("Authorization", "Bearer "+tok)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if call("POST", "/LunexGame/api/battle/start", "").Code != 401 {
		t.Fatal("бой без входа должен быть закрыт")
	}
	w := call("POST", "/LunexGame/api/battle/start", tok1)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var resp struct {
		BattleID string `json:"battleId"`
		Battle   struct {
			Result struct{ Winner string } `json:"result"`
			Events []json.RawMessage       `json:"events"`
			Units  []json.RawMessage       `json:"units"`
		} `json:"battle"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.BattleID == "" || len(resp.Battle.Events) == 0 || len(resp.Battle.Units) != 10 || resp.Battle.Result.Winner == "" {
		t.Fatalf("неполный ответ: %s", w.Body.String()[:200])
	}
	if g := call("GET", "/LunexGame/api/battle/"+resp.BattleID, tok1); g.Code != 200 {
		t.Fatal("владелец не получил бой", g.Code)
	}
	if call("GET", "/LunexGame/api/battle/"+resp.BattleID, tok2).Code != 404 {
		t.Fatal("чужой бой должен быть недоступен")
	}
	if call("GET", "/LunexGame/api/battle/nope", tok1).Code != 404 {
		t.Fatal("несуществующий бой")
	}
	// Повторное чтение не пересчитывает бой и не создаёт записей/побочных эффектов.
	for range 5 {
		g := call("GET", "/LunexGame/api/battle/"+resp.BattleID, tok1)
		if g.Code != 200 || !bytes.Equal(g.Body.Bytes(), w.Body.Bytes()) {
			t.Fatal("повторное чтение изменило результат")
		}
	}
	var count int
	if err := st.DB.QueryRow("SELECT count(*) FROM battles").Scan(&count); err != nil || count != 1 {
		t.Fatal("повторное чтение создало бой", count, err)
	}
	// В прототипе повтор POST означает новую бесплатную попытку, не повторную выдачу награды.
	second := call("POST", "/LunexGame/api/battle/start", tok1)
	var next struct {
		BattleID string          `json:"battleId"`
		Battle   json.RawMessage `json:"battle"`
	}
	var first struct {
		Battle json.RawMessage `json:"battle"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil || second.Code != 200 || next.BattleID == resp.BattleID {
		t.Fatal("повторный старт", err, second.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || !bytes.Equal(next.Battle, first.Battle) {
		t.Fatal("одинаковые отряды дали разные записи", err)
	}
	if err := st.DB.QueryRow("SELECT count(*) FROM battles").Scan(&count); err != nil || count != 2 {
		t.Fatal("число попыток", count, err)
	}
	// Результат/награду нельзя прислать с клиента. Начислений в этой части ещё нет;
	// защиту от двойного начисления проверяем только после появления экономики/транзакции награды.
	for range 2 {
		for _, action := range []string{"finish", "claim"} {
			r := httptest.NewRequest("POST", "/LunexGame/api/battle/"+resp.BattleID+"/"+action,
				strings.NewReader(`{"winner":"player","gold":1000000}`))
			r.Header.Set("Authorization", "Bearer "+tok1)
			g := httptest.NewRecorder()
			h.ServeHTTP(g, r)
			if g.Code != 404 {
				t.Fatal("принят клиентский результат/запрос награды", g.Code)
			}
		}
	}
	if g := call("GET", "/LunexGame/api/battle/"+resp.BattleID, tok1); !bytes.Equal(g.Body.Bytes(), w.Body.Bytes()) {
		t.Fatal("попытка подделки изменила сохранённый бой")
	}
}

func TestWebIndexNoRedirect(t *testing.T) {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "index.html"), []byte("<html>ok</html>"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "index.wasm"), []byte("\x00asm"), 0o644)
	h := New(Build{}, Config{WebDir: d})
	for p, want := range map[string]int{
		"/LunexGame/play/":           200,
		"/LunexGame/play/index.html": 200, // без перенаправления (иначе service worker ломается)
		"/LunexGame/play/index.wasm": 200,
		"/LunexGame/play/nope":       404,
		"/LunexGame/play/../x":       307, // ServeMux нормализует путь; выхода за каталог нет
	} {
		if got := get(h, p).Code; got != want {
			t.Errorf("%s: %d, ожидалось %d", p, got, want)
		}
	}
	if ct := get(h, "/LunexGame/play/index.wasm").Header().Get("Content-Type"); ct != "application/wasm" {
		t.Errorf("тип wasm: %q", ct)
	}
}
