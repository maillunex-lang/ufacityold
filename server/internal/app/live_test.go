package app

import (
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lunexgame/internal/store"
)

func newLiveServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "live.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	_, token, err := st.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(Build{}, Config{Store: st}))
	t.Cleanup(func() {
		liveEpoch.Add(1)
		ts.Close()
	})
	return ts, token
}

func dialLive(t *testing.T, ts *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = Prefix + "/api/battle/live"
	c, _, err := websocket.DefaultDialer.Dial(u.String(), map[string][]string{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readLive(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	var m map[string]any
	if err := c.ReadJSON(&m); err != nil {
		t.Fatalf("чтение: %v", err)
	}
	return m
}

func stateT(m map[string]any) int {
	st, _ := m["state"].(map[string]any)
	if st == nil {
		return -1
	}
	t, _ := st["t"].(float64)
	return int(t)
}

func stateDone(m map[string]any) bool {
	if done, ok := m["done"].(bool); ok && done {
		return true
	}
	st, _ := m["state"].(map[string]any)
	if st == nil {
		return false
	}
	_, ok := st["result"].(map[string]any)
	return ok
}

// D2: после конца боя повторное подключение навсегда отдаёт старое состояние.
// Явный start должен начать новый бой (t == 0), а не вернуть завершённый.
func TestFinishedLiveBattleCanBeReplaced(t *testing.T) {
	prevTick, prevTTL := liveTickNanos.Load(), liveResultNanos.Load()
	liveTickNanos.Store(int64(time.Millisecond))
	liveResultNanos.Store(int64(2 * time.Minute))
	t.Cleanup(func() {
		liveTickNanos.Store(prevTick)
		liveResultNanos.Store(prevTTL)
	})
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	hello := readLive(t, c, 2*time.Second)
	if hello["type"] != "state" && hello["type"] != "hello" {
		t.Fatalf("первое сообщение: %#v", hello)
	}
	if hello["type"] == "hello" {
		if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
			t.Fatal(err)
		}
	}
	var finished map[string]any
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m := readLive(t, c, time.Until(deadline))
		if stateDone(m) {
			finished = m
			break
		}
	}
	if finished == nil {
		t.Fatal("бой не завершился")
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	fresh := readLive(t, c, 2*time.Second)
	if fresh["type"] == "error" {
		t.Fatalf("новый бой отклонён: %#v", fresh)
	}
	if stateT(fresh) != 0 || stateDone(fresh) {
		t.Fatalf("завершённая сессия не заменена, t=%d done=%v msg=%#v", stateT(fresh), stateDone(fresh), fresh)
	}
}

func TestLiveReconnectKeepsRunningBattle(t *testing.T) {
	prev := liveTickNanos.Load()
	liveTickNanos.Store(int64(5 * time.Millisecond))
	t.Cleanup(func() { liveTickNanos.Store(prev) })
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	if hello := readLive(t, c, 2*time.Second); hello["type"] != "hello" {
		t.Fatalf("ожидался явный старт, получено %#v", hello)
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	first := readLive(t, c, 2*time.Second)
	if stateT(first) != 0 {
		t.Fatalf("старт не с нуля: %#v", first)
	}
	// Второй start не создаёт новый бой.
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	dup := readLive(t, c, 2*time.Second)
	if dup["error"] != "already_running" {
		t.Fatalf("повторный старт: %#v", dup)
	}
	c.Close()
	time.Sleep(40 * time.Millisecond)
	c2 := dialLive(t, ts, token)
	resumed := readLive(t, c2, 2*time.Second)
	if resumed["type"] != "state" || resumed["resumed"] != true {
		t.Fatalf("переподключение не вернуло текущий бой: %#v", resumed)
	}
	if stateDone(resumed) {
		t.Fatal("бой не должен был завершиться")
	}
}

func TestLiveFinishedSessionExpires(t *testing.T) {
	prevTick, prevTTL := liveTickNanos.Load(), liveResultNanos.Load()
	liveTickNanos.Store(int64(time.Millisecond))
	liveResultNanos.Store(int64(40 * time.Millisecond))
	t.Cleanup(func() {
		liveTickNanos.Store(prevTick)
		liveResultNanos.Store(prevTTL)
	})
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	_ = readLive(t, c, 2*time.Second) // hello
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if stateDone(readLive(t, c, time.Until(deadline))) {
			break
		}
	}
	c.Close()
	time.Sleep(120 * time.Millisecond)
	c2 := dialLive(t, ts, token)
	hello := readLive(t, c2, 2*time.Second)
	if hello["type"] != "hello" || hello["needStart"] != true {
		t.Fatalf("протухший бой не удалён: %#v", hello)
	}
}

func TestLiveGlobalBattleLimit(t *testing.T) {
	prev := liveTickNanos.Load()
	liveTickNanos.Store(int64(time.Hour)) // бои не успевают завершиться
	t.Cleanup(func() { liveTickNanos.Store(prev) })
	st, err := store.Open(filepath.Join(t.TempDir(), "limit.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(New(Build{}, Config{Store: st}))
	t.Cleanup(func() {
		liveEpoch.Add(1)
		ts.Close()
	})
	start := func(token string) *websocket.Conn {
		t.Helper()
		c := dialLive(t, ts, token)
		hello := readLive(t, c, 2*time.Second)
		if hello["type"] != "hello" {
			t.Fatalf("hello: %#v", hello)
		}
		if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	for i := 0; i < maxLiveBattles; i++ {
		_, token, err := st.CreateGuest()
		if err != nil {
			t.Fatal(err)
		}
		c := start(token)
		msg := readLive(t, c, 2*time.Second)
		if msg["type"] != "state" || stateT(msg) != 0 {
			t.Fatalf("бой %d: %#v", i, msg)
		}
	}
	_, token, err := st.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	c := start(token)
	msg := readLive(t, c, 2*time.Second)
	if msg["error"] != "busy" {
		t.Fatalf("предел %d не сработал: %#v", maxLiveBattles, msg)
	}
}

func TestLiveAutoToggle(t *testing.T) {
	prev := liveTickNanos.Load()
	liveTickNanos.Store(int64(time.Hour))
	t.Cleanup(func() { liveTickNanos.Store(prev) })
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	if hello := readLive(t, c, 2*time.Second); hello["type"] != "hello" {
		t.Fatalf("hello: %#v", hello)
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	state := readLive(t, c, 2*time.Second)
	if state["auto"] != true {
		t.Fatalf("по умолчанию автомат включён: %#v", state)
	}
	off := false
	if err := c.WriteJSON(map[string]any{"type": "set_auto", "auto": off}); err != nil {
		t.Fatal(err)
	}
	ack := readLive(t, c, 2*time.Second)
	if ack["type"] != "auto" || ack["auto"] != false {
		t.Fatalf("переключатель: %#v", ack)
	}
}
