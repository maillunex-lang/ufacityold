package app

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"lunexgame/internal/battle"
	"lunexgame/internal/store"
)

type liveCommand struct {
	Type   string `json:"type"`
	UnitID string `json:"unitId"`
	Auto   *bool  `json:"auto,omitempty"`
}

type liveSession struct {
	mu         sync.Mutex
	writeMu    sync.Mutex
	battle     *battle.Battle
	conn       *websocket.Conn
	stop       chan struct{}
	replaced   bool
	finishedAt time.Time
	id         string
	account    int64
	store      *store.Store
	persisted  bool
	saving     bool
}

type liveHub struct {
	mu       sync.Mutex
	sessions map[int64]*liveSession
	janitor  bool
	store    *store.Store
}

var liveUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 16384, CheckOrigin: func(*http.Request) bool { return true }}

// liveTickNanos — шаг настенных часов между тиками (0 = 100 мс). Игровое время тика остаётся battle.TickMs.
var liveTickNanos atomic.Int64

// liveResultNanos — сколько хранить завершённый бой без соединения (0 = 2 минуты).
var liveResultNanos atomic.Int64

func liveTickInterval() time.Duration {
	if n := liveTickNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return 100 * time.Millisecond
}

func liveResultTTL() time.Duration {
	if n := liveResultNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return 2 * time.Minute
}

// maxLiveBattles — общий предел одновременных живых боёв на процесс (VPS: 1 vCPU, около 2 ГБ ОЗУ).
const maxLiveBattles = 32

// liveMaxMessage — потолок одного кадра клиента. Команды короткие; 4 МБ раньше принимались.
const liveMaxMessage = 1024

// livePongWaitNanos — сколько ждать сообщение или pong (0 = 70 с, меньше nginx 75 с).
var livePongWaitNanos atomic.Int64

// livePingNanos — период ping (0 = 25 с).
var livePingNanos atomic.Int64

func livePongWait() time.Duration {
	if n := livePongWaitNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return 70 * time.Second
}

func livePingEvery() time.Duration {
	if n := livePingNanos.Load(); n > 0 {
		return time.Duration(n)
	}
	return 25 * time.Second
}

// liveEpoch останавливает тики предыдущих тестов, не трогая игровое время.
var liveEpoch atomic.Int64

// newLiveBattle создаёт бой. Тест подменяет его, чтобы ошибка конфигурации не терялась.
var newLiveBattle = func() (*battle.Battle, error) {
	return battle.New(battle.PrototypeSquads(), battle.Config{Energy: battle.DefaultEnergy, BalanceVersion: battle.BalanceVersion})
}

func (s *liveSession) runningLocked() bool { return s.finishedAt.IsZero() && !s.replaced }

func (s *liveSession) expiredLocked(now time.Time) bool {
	return !s.finishedAt.IsZero() && now.Sub(s.finishedAt) > liveResultTTL()
}

func (s *liveSession) emit(c *websocket.Conn, v any) error {
	if c == nil {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	ok := !s.replaced && s.conn == c
	s.mu.Unlock()
	if !ok {
		return nil
	}
	return c.WriteJSON(v)
}

func writeLive(c *websocket.Conn, v any) error {
	if c == nil {
		return nil
	}
	return c.WriteJSON(v)
}

func (s *liveSession) bind(c *websocket.Conn) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	old := s.conn
	s.conn = c
	if s.battle != nil {
		s.battle.SetForceAuto(false)
	}
	s.mu.Unlock()
	if old != nil && old != c {
		_ = old.Close()
	}
}

func (s *liveSession) stopTick() *websocket.Conn {
	s.writeMu.Lock()
	s.mu.Lock()
	old := s.conn
	s.replaced = true
	s.conn = nil
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.mu.Unlock()
	s.writeMu.Unlock()
	return old
}

func (s *liveSession) snapshotLocked(resumed bool) map[string]any {
	state := s.battle.Snapshot()
	speed, allowed := s.playbackLocked()
	return map[string]any{
		"type": "state", "state": state, "balanceVersion": state.Balance,
		"resumed": resumed, "auto": s.battle.PlayerAuto(), "battleId": s.id,
		"roster": s.battle.Roster(), "speed": speed, "allowedSpeeds": allowed,
	}
}

func (s *liveSession) playbackLocked() (int, []int) {
	if s.store == nil || s.account == 0 {
		return 1, []int{1}
	}
	access, err := s.store.BattleSpeedAccess(s.account)
	if err != nil {
		return 1, []int{1}
	}
	opt := access.Options(1)
	return opt.Speed, opt.AllowedSpeeds
}

func (h *liveHub) ensureJanitor() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.janitor {
		return
	}
	h.janitor = true
	go h.janitorLoop()
}

func (h *liveHub) janitorLoop() {
	epoch := liveEpoch.Load()
	t := time.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		if liveEpoch.Load() != epoch {
			h.mu.Lock()
			for _, s := range h.sessions {
				s.stopTick()
			}
			h.sessions = map[int64]*liveSession{}
			h.mu.Unlock()
			return
		}
		h.mu.Lock()
		h.gcLocked(time.Now())
		h.mu.Unlock()
	}
}

func (h *liveHub) gcLocked(now time.Time) {
	for id, s := range h.sessions {
		s.mu.Lock()
		drop := s.replaced || (s.expiredLocked(now) && s.conn == nil)
		s.mu.Unlock()
		if drop {
			if !s.replaced {
				s.stopTick()
			}
			delete(h.sessions, id)
		}
	}
}

func (h *liveHub) activeLocked() int {
	n := 0
	for _, s := range h.sessions {
		s.mu.Lock()
		if s.runningLocked() {
			n++
		}
		s.mu.Unlock()
	}
	return n
}

// usable возвращает незавершённый или ещё не протухший бой аккаунта.
func (h *liveHub) usable(account int64) *liveSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gcLocked(time.Now())
	s := h.sessions[account]
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.replaced {
		return nil
	}
	if s.expiredLocked(time.Now()) && s.conn == nil {
		return nil
	}
	return s
}

func (h *liveHub) begin(account int64, c *websocket.Conn, forceNew bool) (*liveSession, string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gcLocked(time.Now())
	if cur := h.sessions[account]; cur != nil {
		cur.mu.Lock()
		running := cur.runningLocked()
		cur.mu.Unlock()
		if running && !forceNew {
			cur.bind(c)
			return cur, "already_running"
		}
		oldConn := cur.stopTick()
		if forceNew && oldConn != nil && oldConn != c {
			_ = oldConn.Close()
		}
		delete(h.sessions, account)
	}
	if h.activeLocked() >= maxLiveBattles {
		return nil, "busy"
	}
	id, err := newBattleID()
	if err != nil {
		return nil, "internal"
	}
	b, err := newLiveBattle()
	if err != nil {
		return nil, "internal"
	}
	s := &liveSession{battle: b, conn: c, stop: make(chan struct{}), id: id, account: account, store: h.store}
	h.sessions[account] = s
	return s, ""
}

func (h *liveHub) detach(account int64, c *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[account]
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.conn == c {
		s.conn = nil
		if s.battle != nil && !s.battle.Done() {
			s.battle.SetForceAuto(true)
		}
	}
	s.mu.Unlock()
}

func (h *liveHub) pingLoop(account int64, c *websocket.Conn, stop <-chan struct{}) {
	t := time.NewTicker(livePingEvery())
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.mu.Lock()
			s := h.sessions[account]
			h.mu.Unlock()
			var writeMu *sync.Mutex
			if s != nil {
				s.mu.Lock()
				same := s.conn == c && !s.replaced
				s.mu.Unlock()
				if !same {
					return
				}
				writeMu = &s.writeMu
			}
			if writeMu != nil {
				writeMu.Lock()
			}
			err := c.WriteControl(websocket.PingMessage, []byte("ok"), time.Now().Add(5*time.Second))
			if writeMu != nil {
				writeMu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}
}

func (h *liveHub) serve(w http.ResponseWriter, r *http.Request, account int64) {
	c, err := liveUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer c.Close()
	c.SetReadLimit(liveMaxMessage)
	refreshRead := func() { _ = c.SetReadDeadline(time.Now().Add(livePongWait())) }
	refreshRead()
	c.SetPongHandler(func(string) error { return c.SetReadDeadline(time.Now().Add(livePongWait())) })
	stopPing := make(chan struct{})
	defer close(stopPing)
	go h.pingLoop(account, c, stopPing)
	h.ensureJanitor()
	forceNew := r.URL.Query().Get("new") == "1"
	if !forceNew {
		if s := h.usable(account); s != nil {
			s.bind(c)
			s.mu.Lock()
			msg := s.snapshotLocked(true)
			s.mu.Unlock()
			_ = s.emit(c, msg)
		} else {
			_ = writeLive(c, map[string]any{"type": "hello", "needStart": true})
		}
	} else {
		_ = writeLive(c, map[string]any{"type": "hello", "needStart": true})
	}
	for {
		var cmd liveCommand
		if err := c.ReadJSON(&cmd); err != nil {
			h.detach(account, c)
			return
		}
		refreshRead()
		switch cmd.Type {
		case "start":
			s, code := h.begin(account, c, forceNew)
			forceNew = false
			if code == "already_running" && s != nil {
				_ = s.emit(c, map[string]any{"type": "error", "error": code})
				s.mu.Lock()
				msg := s.snapshotLocked(true)
				s.mu.Unlock()
				_ = s.emit(c, msg)
				continue
			}
			if code != "" {
				if s != nil {
					_ = s.emit(c, map[string]any{"type": "error", "error": code})
				} else {
					_ = writeLive(c, map[string]any{"type": "error", "error": code})
				}
				continue
			}
			s.mu.Lock()
			msg := s.snapshotLocked(false)
			s.mu.Unlock()
			_ = s.emit(c, msg)
			go s.tick()
		case "set_auto":
			s := h.usable(account)
			if s == nil || cmd.Auto == nil {
				_ = writeLive(c, map[string]any{"type": "error", "error": "no_battle"})
				continue
			}
			s.mu.Lock()
			s.battle.SetPlayerAuto(*cmd.Auto)
			on := s.battle.PlayerAuto()
			s.mu.Unlock()
			_ = s.emit(c, map[string]any{"type": "auto", "auto": on})
		case "main_skill":
			s := h.usable(account)
			if s == nil {
				_ = writeLive(c, map[string]any{"type": "command", "accepted": false, "unitId": cmd.UnitID, "reason": "no_battle"})
				continue
			}
			s.mu.Lock()
			accepted := s.battle.RequestMainSkill(cmd.UnitID)
			s.mu.Unlock()
			_ = s.emit(c, map[string]any{"type": "command", "accepted": accepted, "unitId": cmd.UnitID})
		}
	}
}

func (s *liveSession) tick() {
	epoch := liveEpoch.Load()
	ticker := time.NewTicker(liveTickInterval())
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if liveEpoch.Load() != epoch {
				return
			}
			s.mu.Lock()
			if s.replaced || s.battle.Done() {
				done := s.battle.Done()
				if done && s.finishedAt.IsZero() {
					s.finishedAt = time.Now()
				}
				s.mu.Unlock()
				if done {
					s.persist()
				}
				return
			}
			events := s.battle.Step()
			if events == nil {
				events = []battle.Event{}
			}
			c := s.conn
			done := s.battle.Done()
			if done {
				s.finishedAt = time.Now()
			}
			state := s.battle.Snapshot()
			s.mu.Unlock()
			if done {
				s.persist()
			}
			_ = s.emit(c, map[string]any{"type": "tick", "events": events, "state": state, "done": done, "battleId": s.id})
			if done {
				return
			}
		}
	}
}

func newBattleID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// persist закрепляет результат и версию баланса один раз. Награды пока не начисляются:
// место для них — grantBattleReward внутри транзакции SaveFinishedBattle.
func (s *liveSession) persist() {
	s.mu.Lock()
	if s.persisted || s.saving || s.store == nil || s.battle == nil || !s.battle.Done() {
		s.mu.Unlock()
		return
	}
	out, ok := s.battle.Outcome()
	if !ok {
		s.mu.Unlock()
		return
	}
	s.saving = true
	id, account, st := s.id, s.account, s.store
	s.mu.Unlock()
	js, err := json.Marshal(out)
	if err != nil {
		s.mu.Lock()
		s.saving = false
		s.mu.Unlock()
		log.Printf("живой бой %s: не удалось записать результат: %v", id, err)
		return
	}
	_, _, err = st.SaveFinishedBattle(account, id, out.Balance, out.Result.Winner, js, 1)
	s.mu.Lock()
	s.saving = false
	if err != nil {
		log.Printf("живой бой %s: сохранение не удалось: %v", id, err)
	} else {
		s.persisted = true
	}
	s.mu.Unlock()
}
