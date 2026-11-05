package app

import (
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"lunexgame/internal/battle"
)

type liveCommand struct {
	Type   string `json:"type"`
	UnitID string `json:"unitId"`
}
type liveSession struct {
	mu      sync.Mutex
	writeMu sync.Mutex
	battle  *battle.Battle
	conn    *websocket.Conn
	started bool
}

func (s *liveSession) write(v any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.conn == nil {
		return nil
	}
	return s.conn.WriteJSON(v)
}

type liveHub struct {
	mu       sync.Mutex
	sessions map[int64]*liveSession
}

var liveUpgrader = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 16384, CheckOrigin: func(*http.Request) bool { return true }}

func (h *liveHub) session(account int64) *liveSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sessions[account]; s != nil {
		return s
	}
	b, _ := battle.New(battle.PrototypeSquads(), battle.Config{Energy: battle.DefaultEnergy})
	s := &liveSession{battle: b}
	h.sessions[account] = s
	return s
}

func (h *liveHub) serve(w http.ResponseWriter, r *http.Request, account int64) {
	c, err := liveUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	s := h.session(account)
	s.mu.Lock()
	s.conn = c
	if !s.started {
		s.started = true
		go s.tick()
	}
	state := s.battle.Snapshot()
	s.mu.Unlock()
	_ = s.write(map[string]any{"type": "state", "state": state, "balanceVersion": state.Balance})
	for {
		var cmd liveCommand
		if err := c.ReadJSON(&cmd); err != nil {
			s.mu.Lock()
			if s.conn == c {
				s.conn = nil
			}
			s.mu.Unlock()
			_ = c.Close()
			return
		}
		if cmd.Type == "main_skill" {
			s.mu.Lock()
			accepted := s.battle.RequestMainSkill(cmd.UnitID)
			s.mu.Unlock()
			_ = s.write(map[string]any{"type": "command", "accepted": accepted, "unitId": cmd.UnitID})
		}
	}
}

func (s *liveSession) tick() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for range t.C {
		s.mu.Lock()
		if s.battle.Done() {
			s.mu.Unlock()
			return
		}
		events := s.battle.Step()
		c := s.conn
		done := s.battle.Done()
		state := s.battle.Snapshot()
		s.mu.Unlock()
		if c != nil {
			_ = s.write(map[string]any{"type": "tick", "events": events, "state": state, "done": done})
		}
	}
}
