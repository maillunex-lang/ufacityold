package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func srv(t *testing.T) http.Handler {
	d := t.TempDir()
	_ = os.WriteFile(filepath.Join(d, "a.txt"), []byte("x"), 0o644)
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
		"/LunexGame/":             200,
		"/LunexGame":              301,
		"/LunexGame/api/health":   200,
		"/LunexGame/api/version":  200,
		"/LunexGame/assets/a.txt": 200,
		"/LunexGame/assets/":      404,
		"/LunexGame/admin/":       404,
		"/LunexGame/admin/x":      404,
		"/api/health":             404,
		"/assets/a.txt":           404,
		"/admin/":                 404,
		"/lunexgame/":             404,
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
