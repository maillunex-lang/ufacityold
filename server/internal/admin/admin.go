// Package admin — веб-админка владельца (этап 5, основа).
//
// Безопасность: без хэша пароля (LUNEXGAME_ADMIN_PASSWORD_HASH) админка не создаётся вовсе (маршрут остаётся 404).
// Один владелец; пароль — argon2id; сессия — случайный токен в cookie HttpOnly+Secure+SameSite=Strict (в БД только хэш);
// все изменяющие запросы требуют заголовок X-CSRF-Token и проверки источника; вход ограничен по адресу и в целом;
// все действия пишутся в журнал. Страница отдаётся со строгой Content-Security-Policy без внешних ресурсов.
package admin

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"lunexgame/internal/content"
)

const (
	Prefix     = "/LunexGame/admin"
	cookieName = "lg_admin"
	maxBody    = 512 << 10
)

// Preview строит предпросмотр черновика (например, прогоняет демо-бой). published может быть nil.
type Preview func(kind string, draft, published json.RawMessage) (map[string]any, error)

type Config struct {
	DB           *sql.DB
	PasswordHash string
	Preview      Preview
	ClientIP     func(*http.Request) string
	Logf         func(format string, args ...any)
	Now          func() time.Time
}

type Service struct {
	Config
	content  *content.Store
	loginIP  *limiter
	loginAll *limiter
	mux      *http.ServeMux
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func (s *Service) ip(r *http.Request) string {
	if s.ClientIP != nil {
		return s.ClientIP(r)
	}
	return r.RemoteAddr
}

// New возвращает nil, если админка не настроена (нет хэша пароля) — тогда её нельзя включить случайно.
func New(cfg Config) *Service {
	if cfg.PasswordHash == "" || cfg.DB == nil {
		return nil
	}
	s := &Service{Config: cfg, content: &content.Store{DB: cfg.DB},
		loginIP: newLimiter(5, time.Minute), loginAll: newLimiter(20, time.Minute)}
	s.routes()
	return s
}

func (s *Service) Handler() http.Handler {
	return http.StripPrefix(Prefix, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setSecurityHeaders(w)
		s.mux.ServeHTTP(w, r)
	}))
}

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; "+
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string, details ...string) {
	body := map[string]any{"error": msg}
	if len(details) > 0 {
		body["details"] = details
	}
	writeJSON(w, code, body)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("лишние данные после JSON")
	}
	return nil
}

// sameOrigin: запрос должен исходить с этой же страницы (CSRF помимо токена).
func sameOrigin(r *http.Request) bool {
	if sfs := r.Header.Get("Sec-Fetch-Site"); sfs != "" && sfs != "same-origin" && sfs != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		if err != nil || (u.Host != r.Host && u.Host != r.Header.Get("X-Forwarded-Host")) {
			return false
		}
	}
	return true
}

type authedHandler func(w http.ResponseWriter, r *http.Request, sess *session)

func (s *Service) auth(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		sess, ok := s.lookupSession(c.Value)
		if !ok {
			fail(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) || !equal(r.Header.Get("X-CSRF-Token"), sess.CSRF) {
				fail(w, http.StatusForbidden, "csrf")
				return
			}
		}
		h(w, r, sess)
	}
}

func (s *Service) routes() {
	m := http.NewServeMux()
	s.mux = m
	m.HandleFunc("GET /{$}", serveIndex)
	m.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	m.HandleFunc("POST /api/login", s.login)
	m.HandleFunc("POST /api/logout", s.auth(s.logout))
	m.HandleFunc("GET /api/me", s.auth(func(w http.ResponseWriter, r *http.Request, sess *session) {
		writeJSON(w, http.StatusOK, map[string]any{"csrf": sess.CSRF})
	}))
	m.HandleFunc("GET /api/kinds", s.auth(s.kinds))
	m.HandleFunc("GET /api/content/{kind}", s.auth(s.getContent))
	m.HandleFunc("GET /api/content/{kind}/versions/{n}", s.auth(s.getVersion))
	m.HandleFunc("PUT /api/content/{kind}/draft", s.auth(s.saveDraft))
	m.HandleFunc("DELETE /api/content/{kind}/draft", s.auth(s.deleteDraft))
	m.HandleFunc("POST /api/content/{kind}/validate", s.auth(s.validate))
	m.HandleFunc("POST /api/content/{kind}/preview", s.auth(s.preview))
	m.HandleFunc("POST /api/content/{kind}/publish", s.auth(s.publish))
	m.HandleFunc("POST /api/content/{kind}/rollback", s.auth(s.rollback))
	m.HandleFunc("GET /api/audit", s.auth(s.audit))
	// всё остальное под /admin/ — 404 в формате JSON/текста без подсказок
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
}

// ---- вход / выход ----

func (s *Service) login(w http.ResponseWriter, r *http.Request) {
	ip := s.ip(r)
	if !sameOrigin(r) {
		fail(w, http.StatusForbidden, "csrf")
		return
	}
	if !s.loginIP.allow(ip) || !s.loginAll.allow("all") {
		s.logf("admin: слишком много попыток входа ip=%s", ip)
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusTooManyRequests, "too_many_attempts")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &body); err != nil || len(body.Password) > 1024 {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !VerifyPassword(s.PasswordHash, body.Password) {
		s.logf("admin: вход отклонён ip=%s", ip) // формат удобен для fail2ban
		s.content.Audit("login_failed", "", 0, ip, "")
		fail(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	token, csrf, err := s.newSession(ip)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: Prefix, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
	s.content.Audit("login", "", 0, ip, "")
	writeJSON(w, http.StatusOK, map[string]any{"csrf": csrf})
}

func (s *Service) logout(w http.ResponseWriter, r *http.Request, _ *session) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.deleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: Prefix, HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	s.content.Audit("logout", "", 0, s.ip(r), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- контент ----

func (s *Service) kinds(w http.ResponseWriter, r *http.Request, _ *session) {
	writeJSON(w, http.StatusOK, map[string]any{"kinds": content.Kinds})
}

func (s *Service) kindOr404(w http.ResponseWriter, r *http.Request) *content.Kind {
	k := content.Find(r.PathValue("kind"))
	if k == nil {
		fail(w, http.StatusNotFound, "unknown_kind")
	}
	return k
}

func headVersion(v *content.Version) int {
	if v == nil {
		return 0
	}
	return v.Version
}

func (s *Service) getContent(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	head, err1 := s.content.Head(k.Name)
	draft, err2 := s.content.GetDraft(k.Name)
	hist, err3 := s.content.History(k.Name, 100)
	if err1 != nil || err2 != nil || err3 != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	var draftErrs []string
	if draft != nil {
		draftErrs = k.Validate(draft.Data)
	}
	writeJSON(w, http.StatusOK, map[string]any{"kind": k, "published": head, "draft": draft, "draftErrors": draftErrs, "history": hist})
}

func (s *Service) getVersion(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	v, err := s.content.Version(k.Name, n)
	if err == content.ErrNotFound {
		fail(w, http.StatusNotFound, "not_found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

type dataReq struct {
	Data            json.RawMessage `json:"data"`
	Note            string          `json:"note"`
	ExpectedVersion *int            `json:"expectedVersion"`
	ToVersion       int             `json:"toVersion"`
}

func (s *Service) saveDraft(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	var req dataReq
	if err := readJSON(w, r, &req); err != nil || len(req.Data) == 0 {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	head, err := s.content.Head(k.Name)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	base := headVersion(head)
	if old, _ := s.content.GetDraft(k.Name); old != nil {
		base = old.BaseVersion // основа черновика не «уезжает» при сохранении
	}
	if err := s.content.SaveDraft(k.Name, req.Data, base); err != nil {
		fail(w, http.StatusBadRequest, "bad_json", err.Error())
		return
	}
	s.content.Audit("draft_save", k.Name, base, s.ip(r), "")
	writeJSON(w, http.StatusOK, map[string]any{"saved": true, "baseVersion": base, "errors": k.Validate(req.Data)})
}

func (s *Service) deleteDraft(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	if err := s.content.DeleteDraft(k.Name); err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	s.content.Audit("draft_delete", k.Name, 0, s.ip(r), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// dataFrom берёт данные из запроса, а если их нет — из сохранённого черновика.
func (s *Service) dataFrom(k *content.Kind, req dataReq) (json.RawMessage, bool) {
	if len(req.Data) > 0 {
		return req.Data, true
	}
	if d, _ := s.content.GetDraft(k.Name); d != nil {
		return d.Data, true
	}
	return nil, false
}

func (s *Service) validate(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	var req dataReq
	if err := readJSON(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	data, ok := s.dataFrom(k, req)
	if !ok {
		fail(w, http.StatusBadRequest, "no_data")
		return
	}
	errs := k.Validate(data)
	writeJSON(w, http.StatusOK, map[string]any{"valid": len(errs) == 0, "errors": errs})
}

func (s *Service) preview(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	var req dataReq
	if err := readJSON(w, r, &req); err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	data, ok := s.dataFrom(k, req)
	if !ok {
		fail(w, http.StatusBadRequest, "no_data")
		return
	}
	if errs := k.Validate(data); len(errs) > 0 {
		fail(w, http.StatusUnprocessableEntity, "invalid", errs...)
		return
	}
	if s.Preview == nil {
		writeJSON(w, http.StatusOK, map[string]any{"preview": map[string]any{"сообщение": "предпросмотр для этого вида не предусмотрен"}})
		return
	}
	head, _ := s.content.Head(k.Name)
	var pub json.RawMessage
	if head != nil {
		pub = head.Data
	}
	out, err := s.Preview(k.Name, data, pub)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, "preview_failed", err.Error())
		return
	}
	s.content.Audit("preview", k.Name, 0, s.ip(r), "")
	writeJSON(w, http.StatusOK, map[string]any{"preview": out})
}

func (s *Service) publish(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	var req dataReq
	if err := readJSON(w, r, &req); err != nil || req.ExpectedVersion == nil || len(req.Note) > 300 {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	data, ok := s.dataFrom(k, req)
	if !ok {
		fail(w, http.StatusBadRequest, "no_data")
		return
	}
	if errs := k.Validate(data); len(errs) > 0 {
		fail(w, http.StatusUnprocessableEntity, "invalid", errs...)
		return
	}
	v, err := s.content.Publish(k.Name, data, strings.TrimSpace(req.Note), "publish", nil, *req.ExpectedVersion)
	if err == content.ErrConflict {
		fail(w, http.StatusConflict, "conflict")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	s.content.Audit("publish", k.Name, v, s.ip(r), strings.TrimSpace(req.Note))
	writeJSON(w, http.StatusOK, map[string]any{"version": v})
}

func (s *Service) rollback(w http.ResponseWriter, r *http.Request, _ *session) {
	k := s.kindOr404(w, r)
	if k == nil {
		return
	}
	var req dataReq
	if err := readJSON(w, r, &req); err != nil || req.ExpectedVersion == nil || req.ToVersion < 1 || len(req.Note) > 300 {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	old, err := s.content.Version(k.Name, req.ToVersion)
	if err == content.ErrNotFound {
		fail(w, http.StatusNotFound, "not_found")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	// Старые данные должны проходить ТЕКУЩУЮ проверку (правила могли стать строже).
	if errs := k.Validate(old.Data); len(errs) > 0 {
		fail(w, http.StatusUnprocessableEntity, "invalid", errs...)
		return
	}
	from := req.ToVersion
	note := strings.TrimSpace(req.Note)
	v, err := s.content.Publish(k.Name, old.Data, note, "rollback", &from, *req.ExpectedVersion)
	if err == content.ErrConflict {
		fail(w, http.StatusConflict, "conflict")
		return
	} else if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	s.content.Audit("rollback", k.Name, v, s.ip(r), "из версии "+strconv.Itoa(from))
	writeJSON(w, http.StatusOK, map[string]any{"version": v, "rolledBackFrom": from})
}

func (s *Service) audit(w http.ResponseWriter, r *http.Request, _ *session) {
	rows, err := s.content.AuditLog(200)
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}
