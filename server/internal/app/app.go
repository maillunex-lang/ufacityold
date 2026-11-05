// Package app содержит HTTP-каркас LunexGame. Все маршруты живут под префиксом
// /LunexGame/ (регистр важен); корневые /api, /assets, /admin не используются.
package app

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"lunexgame/internal/assets"
	"lunexgame/internal/battle"
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
	WebDir    string // каталог Web-сборки клиента (PWA); пусто — не раздаётся
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
	if cfg.Store != nil {
		auth := func(w http.ResponseWriter, r *http.Request) (int64, bool) {
			id, err := cfg.Store.AccountByToken(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if err != nil {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return 0, false
			}
			return id, true
		}
		battleLim := newLimiter(20, time.Minute)
		speedLim := newLimiter(60, time.Minute)
		// Прототип: бой рассчитывается целиком на сервере (полный автомат), клиент проигрывает запись.
		mux.HandleFunc("POST "+Prefix+"/api/battle/start", func(w http.ResponseWriter, r *http.Request) {
			acc, ok := auth(w, r)
			if !ok {
				return
			}
			if !battleLim.allow(strconv.FormatInt(acc, 10)) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_requests"})
				return
			}
			speed, err := readSpeed(w, r, true)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_speed"})
				return
			}
			access, err := cfg.Store.BattleSpeedAccess(acc)
			if err != nil {
				writeSpeedError(w, battle.PlaybackOptions{}, err)
				return
			}
			if !access.Allows(speed) {
				writeSpeedError(w, access.Options(1), store.ErrSpeedForbidden)
				return
			}
			out, runErr := battle.Run(battle.PrototypeSquads(), battle.DefaultEnergy)
			if runErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
				return
			}
			js, err := json.Marshal(out)
			if err == nil {
				id, options, saveErr := cfg.Store.SaveBattle(acc, out.Balance, out.Result.Winner, js, speed)
				if saveErr == nil {
					writeJSON(w, http.StatusOK, battleReply{id, json.RawMessage(js), options})
					return
				}
				writeSpeedError(w, options, saveErr)
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
		})
		mux.HandleFunc("GET "+Prefix+"/api/battle/{id}", func(w http.ResponseWriter, r *http.Request) {
			acc, ok := auth(w, r)
			if !ok {
				return
			}
			record, err := cfg.Store.GetBattle(acc, r.PathValue("id"))
			if err == store.ErrNotFound {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
				return
			} else if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
				return
			}
			writeJSON(w, http.StatusOK, battleReply{r.PathValue("id"), json.RawMessage(record.OutcomeJSON), record.Playback})
		})
		mux.HandleFunc("PUT "+Prefix+"/api/battle/{id}/speed", func(w http.ResponseWriter, r *http.Request) {
			acc, ok := auth(w, r)
			if !ok {
				return
			}
			if !speedLim.allow(strconv.FormatInt(acc, 10)) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_requests"})
				return
			}
			speed, err := readSpeed(w, r, false)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_speed"})
				return
			}
			options, err := cfg.Store.SetBattleSpeed(acc, r.PathValue("id"), speed)
			if err != nil {
				writeSpeedError(w, options, err)
				return
			}
			writeJSON(w, http.StatusOK, battleReply{BattleID: r.PathValue("id"), PlaybackOptions: options})
		})
	}
	// Раздача ресурсов (основа для загрузчика клиента). Каталог пуст, пока нет контента.
	as := assets.New(cfg.AssetsDir)
	mux.HandleFunc("GET "+Prefix+"/api/assets/manifest", as.ManifestHandler)
	mux.Handle("GET "+Prefix+"/assets/", as.FileHandler(Prefix+"/assets/"))
	if cfg.WebDir != "" {
		// Web-клиент (PWA) под /LunexGame/play/. Service worker с этого пути имеет область только /LunexGame/play/.
		mux.Handle("GET "+Prefix+"/play/", http.StripPrefix(Prefix+"/play/", webHandler(cfg.WebDir)))
		mux.HandleFunc("GET "+Prefix+"/play", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, Prefix+"/play/", http.StatusMovedPermanently)
		})
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
	cfg.WebDir = os.Getenv("LUNEXGAME_WEB_DIR")
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

// webHandler раздаёт статические файлы Web-сборки; index.html и service worker не кэшируются.
func webHandler(dir string) http.Handler {
	fs := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// FileServer перенаправляет /index.html на ./ — такой «redirected»-ответ service worker не может
		// отдать для навигации (ERR_FAILED без сети), поэтому index.html отдаём напрямую.
		if r.URL.Path == "index.html" || r.URL.Path == "/index.html" {
			f, err := os.Open(filepath.Join(dir, "index.html"))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			defer f.Close()
			st, _ := f.Stat()
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeContent(w, r, "index.html", st.ModTime(), f)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/") || r.URL.Path == "" || strings.HasSuffix(r.URL.Path, ".html") ||
			strings.HasSuffix(r.URL.Path, ".service.worker.js") || strings.HasSuffix(r.URL.Path, ".manifest.json") {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fs.ServeHTTP(w, r)
	})
}
