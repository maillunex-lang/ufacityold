// Package app содержит HTTP-каркас LunexGame. Все маршруты живут под префиксом
// /LunexGame/ (регистр важен); корневые /api, /assets, /admin не используются.
package app

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lunexgame/internal/store"
)

const Prefix = "/LunexGame"

// Build — сведения о сборке (задаются через -ldflags).
type Build struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"buildTime"`
}

type Config struct {
	Store     *store.Store
	DBPath    string
	BackupDir string
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
		out := map[string]any{"status": "ok", "uptimeSeconds": int(time.Since(started).Seconds())}
		code := http.StatusOK
		if cfg.Store != nil {
			if v, err := cfg.Store.SchemaVersion(); err != nil {
				out["status"], out["db"], code = "error", "unavailable", http.StatusServiceUnavailable
			} else {
				out["db"], out["schemaVersion"] = "ok", v
			}
		}
		writeJSON(w, code, out)
	})
	mux.HandleFunc("GET "+Prefix+"/api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b)
	})
	if cfg.Store != nil {
		lim := newLimiter(10, time.Minute)
		mux.HandleFunc("POST "+Prefix+"/api/auth/guest", func(w http.ResponseWriter, r *http.Request) {
			if !lim.allow(clientIP(r)) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_requests"})
				return
			}
			id, tok, err := cfg.Store.CreateGuest()
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"accountId": id, "token": tok})
		})
		mux.HandleFunc("GET "+Prefix+"/api/me", func(w http.ResponseWriter, r *http.Request) {
			tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			id, err := cfg.Store.AccountByToken(tok)
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"accountId": id})
		})
	}
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

// clientIP: за nginx на loopback доверяем X-Real-IP; напрямую — адрес соединения.
func clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if v := r.Header.Get("X-Real-IP"); v != "" {
			return v
		}
	}
	return host
}

type limiter struct {
	mu   sync.Mutex
	n    int
	win  time.Duration
	hits map[string][]time.Time
}

func newLimiter(n int, win time.Duration) *limiter {
	return &limiter{n: n, win: win, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.win {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.n {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if len(l.hits) > 10000 { // защита памяти
		l.hits = map[string][]time.Time{key: l.hits[key]}
	}
	return true
}

// FromEnv читает конфигурацию из окружения.
func FromEnv() (addr string, cfg Config) {
	addr = os.Getenv("LUNEXGAME_LISTEN")
	if addr == "" {
		addr = "127.0.0.1:18741"
	}
	cfg.AssetsDir = os.Getenv("LUNEXGAME_ASSETS_DIR")
	cfg.DBPath = os.Getenv("LUNEXGAME_DB")
	if cfg.DBPath == "" {
		cfg.DBPath = "./data/lunexgame.db"
		if st, err := os.Stat("/opt/lunexgame/data"); err == nil && st.IsDir() {
			cfg.DBPath = "/opt/lunexgame/data/lunexgame.db"
		}
	}
	cfg.BackupDir = filepath.Join(filepath.Dir(cfg.DBPath), "backups")
	return
}
