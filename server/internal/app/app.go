// Package app содержит HTTP-каркас LunexGame. Все маршруты живут под префиксом
// /LunexGame/ (регистр важен); корневые /api, /assets, /admin не используются.
package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const Prefix = "/LunexGame"

// Build — сведения о сборке (задаются через -ldflags).
type Build struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

type Config struct {
	AssetsDir string // каталог загружаемых ресурсов (вне релизов, не перезаписывается деплоем)
}

func New(b Build, cfg Config) http.Handler {
	started := time.Now()
	mux := http.NewServeMux()

	mux.HandleFunc("GET "+Prefix+"/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(statusPage(b)))
	})
	mux.HandleFunc("GET "+Prefix+"/api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":        "ok",
			"uptimeSeconds": int(time.Since(started).Seconds()),
		})
	})
	mux.HandleFunc("GET "+Prefix+"/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b)
	})
	// Раздача ресурсов (основа для загрузчика клиента). Каталог пуст, пока нет контента.
	if cfg.AssetsDir != "" {
		fs := http.StripPrefix(Prefix+"/assets/", http.FileServer(http.Dir(cfg.AssetsDir)))
		mux.Handle("GET "+Prefix+"/assets/", noListing(fs))
	}
	// Админка не реализована: маршрут закрыт явно.
	mux.HandleFunc(Prefix+"/admin/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	// Перенаправление без слэша — с сохранением префикса.
	mux.HandleFunc("GET "+Prefix, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, Prefix+"/", http.StatusMovedPermanently)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// noListing запрещает листинг каталогов и скрытые файлы.
func noListing(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") || strings.HasPrefix(filepath.Base(r.URL.Path), ".") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func statusPage(b Build) string {
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return `<!doctype html><html lang="ru"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>LunexGame — состояние</title>
<style>body{margin:0;min-height:100vh;display:grid;place-items:center;background:#06251f;color:#e8d9a0;font-family:system-ui,sans-serif}
main{padding:24px;max-width:480px}h1{color:#d4af37;margin:0 0 8px}.ok{color:#3fe0c5}code{color:#3fe0c5}</style></head><body><main>
<h1>Самрая: Пробуждение легенд</h1>
<p class="ok">Сервер LunexGame работает (этап 0, технический каркас).</p>
<p>Версия: <code>` + esc.Replace(b.Version) + `</code><br>Коммит: <code>` + esc.Replace(b.Commit) + `</code></p>
<p><a style="color:#3fe0c5" href="` + Prefix + `/api/health">health</a> · <a style="color:#3fe0c5" href="` + Prefix + `/api/version">version</a></p>
</main></body></html>`
}

// FromEnv читает конфигурацию из окружения.
func FromEnv() (addr string, cfg Config) {
	addr = os.Getenv("LUNEXGAME_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:18741"
	}
	cfg.AssetsDir = os.Getenv("LUNEXGAME_ASSETS_DIR")
	return
}
