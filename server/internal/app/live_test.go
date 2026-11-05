package app

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lunexgame/internal/battle"
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

// Обрыв: при выключенном авто сервер сам выпускает главную способность,
// бой не останавливается, а после возвращения снова действует выбор игрока.
// Без SetForceAuto на detach энергия так и останется полной.
func TestLiveDisconnectForcesAutoThenRestoresChoice(t *testing.T) {
	prevTick := liveTickNanos.Load()
	liveTickNanos.Store(int64(5 * time.Millisecond))
	t.Cleanup(func() { liveTickNanos.Store(prevTick) })
	prevNew := newLiveBattle
	newLiveBattle = func() (*battle.Battle, error) {
		defs := []battle.UnitDef{
			{ID: "p", Name: "Лучник", Side: "player", Role: battle.Archer, Slot: 2, MaxHP: 50000, Attack: 40, IntervalMs: 5000},
			{ID: "e", Name: "Мишень", Side: "enemy", Role: battle.Tank, Slot: 0, MaxHP: 500000, Attack: 1, IntervalMs: 1000, Melee: true},
		}
		return battle.New(defs, battle.Config{
			Energy:         battle.Energy{Max: 30, PerDealt: 1},
			BalanceVersion: "reconnect",
		})
	}
	t.Cleanup(func() { newLiveBattle = prevNew })

	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	if hello := readLive(t, c, 2*time.Second); hello["type"] != "hello" {
		t.Fatalf("hello: %#v", hello)
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	if state := readLive(t, c, 2*time.Second); state["type"] != "state" {
		t.Fatalf("старт: %#v", state)
	}
	off := false
	if err := c.WriteJSON(map[string]any{"type": "set_auto", "auto": off}); err != nil {
		t.Fatal(err)
	}
	if ack := readLive(t, c, 2*time.Second); ack["type"] != "auto" || ack["auto"] != false {
		t.Fatalf("авто: %#v", ack)
	}
	var fullAt int
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m := readLive(t, c, time.Until(deadline))
		if e, ok := unitEnergy(m, "p"); ok && e == 30 {
			fullAt = stateT(m)
			break
		}
	}
	if fullAt == 0 {
		t.Fatal("без авто энергия не копилась или способность вышла сама")
	}
	c.Close()
	time.Sleep(150 * time.Millisecond)

	c2 := dialLive(t, ts, token)
	resumed := readLive(t, c2, 2*time.Second)
	if resumed["type"] != "state" || resumed["resumed"] != true {
		t.Fatalf("переподключение: %#v", resumed)
	}
	if resumed["auto"] != false {
		t.Fatalf("выбор игрока не сохранился: %#v", resumed["auto"])
	}
	if stateT(resumed) <= fullAt {
		t.Fatalf("во время обрыва бой стоял: t=%d, было %d", stateT(resumed), fullAt)
	}
	spent, ok := unitEnergy(resumed, "p")
	if !ok || spent >= 30 {
		t.Fatalf("при обрыве полный автомат не потратил энергию: %v t=%d", spent, stateT(resumed))
	}
	held := 0
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && held < 2 {
		m := readLive(t, c2, time.Until(deadline))
		e, ok := unitEnergy(m, "p")
		if !ok {
			continue
		}
		if e == 30 {
			held++
			continue
		}
		if held > 0 {
			t.Fatalf("после возвращения способность снова вышла сама, энергия %v", e)
		}
	}
	if held < 2 {
		t.Fatal("после переподключения энергия не удержалась на полной без команды")
	}
}

func unitEnergy(m map[string]any, id string) (int, bool) {
	st, _ := m["state"].(map[string]any)
	if st == nil {
		return 0, false
	}
	list, _ := st["units"].([]any)
	for _, raw := range list {
		u, _ := raw.(map[string]any)
		if u["id"] != id {
			continue
		}
		n, ok := u["energy"].(float64)
		return int(n), ok
	}
	return 0, false
}

func TestLiveRejectsOversizedCommand(t *testing.T) {
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
	if state := readLive(t, c, 2*time.Second); state["type"] != "state" {
		t.Fatalf("state: %#v", state)
	}
	// Больше 1 КБ: раньше сервер принимал и отвечал command. Теперь рвёт кадр.
	err := c.WriteJSON(map[string]any{"type": "main_skill", "unitId": strings.Repeat("x", 4096)})
	if err != nil {
		return // соединение сброшено на слишком большом кадре
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var m map[string]any
	err = c.ReadJSON(&m)
	if err == nil && (m["type"] == "command" || m["type"] == "tick" || m["type"] == "state") {
		t.Fatalf("сообщение больше лимита принято: %#v", m["type"])
	}
}

func TestLiveSendsPing(t *testing.T) {
	prev := livePingNanos.Load()
	livePingNanos.Store(int64(30 * time.Millisecond))
	t.Cleanup(func() { livePingNanos.Store(prev) })
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	pinged := make(chan struct{}, 1)
	c.SetPingHandler(func(appData string) error {
		select {
		case pinged <- struct{}{}:
		default:
		}
		return c.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(time.Second))
	})
	go func() {
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}()
	select {
	case <-pinged:
	case <-time.After(2 * time.Second):
		t.Fatal("сервер не прислал ping")
	}
}

func TestLiveReplacedConnectionIsClosed(t *testing.T) {
	prev := liveTickNanos.Load()
	liveTickNanos.Store(int64(time.Hour))
	t.Cleanup(func() { liveTickNanos.Store(prev) })
	ts, token := newLiveServer(t)
	c1 := dialLive(t, ts, token)
	_ = readLive(t, c1, 2*time.Second)
	if err := c1.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	_ = readLive(t, c1, 2*time.Second)
	c2 := dialLive(t, ts, token)
	_ = readLive(t, c2, 2*time.Second)
	_ = c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := c1.ReadMessage(); err == nil {
		t.Fatal("прежнее соединение при замене не закрыто")
	}
}

func TestLiveBattleNewErrorIsReported(t *testing.T) {
	prev := newLiveBattle
	newLiveBattle = func() (*battle.Battle, error) { return nil, errors.New("плохая конфигурация") }
	t.Cleanup(func() { newLiveBattle = prev })
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	if hello := readLive(t, c, 2*time.Second); hello["type"] != "hello" {
		t.Fatalf("hello: %#v", hello)
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	msg := readLive(t, c, 2*time.Second)
	if msg["error"] != "internal" {
		t.Fatalf("ошибка создания боя потеряна: %#v", msg)
	}
	// Повтор не должен паниковать и не должен открыть бой из сломанной конфигурации.
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	msg = readLive(t, c, 2*time.Second)
	if msg["error"] != "internal" {
		t.Fatalf("повтор: %#v", msg)
	}
}

func TestLiveParallelConnections(t *testing.T) {
	prev := liveTickNanos.Load()
	liveTickNanos.Store(int64(2 * time.Millisecond))
	t.Cleanup(func() { liveTickNanos.Store(prev) })
	ts, token := newLiveServer(t)
	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, _, err := websocket.DefaultDialer.Dial(wsURL(ts), map[string][]string{"Authorization": {"Bearer " + token}})
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			var hello map[string]any
			if err := c.ReadJSON(&hello); err != nil {
				return
			}
			_ = c.WriteJSON(map[string]any{"type": "start"})
			_ = c.WriteJSON(map[string]any{"type": "main_skill", "unitId": "p0"})
			for range 5 {
				var m map[string]any
				if err := c.ReadJSON(&m); err != nil {
					return
				}
			}
		}()
	}
	wg.Wait()
}

func wsURL(ts *httptest.Server) string {
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = Prefix + "/api/battle/live"
	return u.String()
}

func TestLiveResultPersistedOnce(t *testing.T) {
	prevTick := liveTickNanos.Load()
	liveTickNanos.Store(int64(time.Millisecond))
	prevNew := newLiveBattle
	newLiveBattle = func() (*battle.Battle, error) {
		return battle.New(battle.PrototypeSquads(), battle.Config{Energy: battle.DefaultEnergy, BalanceVersion: "pinned-d8"})
	}
	t.Cleanup(func() {
		liveTickNanos.Store(prevTick)
		newLiveBattle = prevNew
	})
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
	c := dialLive(t, ts, token)
	if hello := readLive(t, c, 2*time.Second); hello["type"] != "hello" {
		t.Fatalf("hello: %#v", hello)
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	var battleID string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		m := readLive(t, c, time.Until(deadline))
		if id, _ := m["battleId"].(string); id != "" {
			battleID = id
		}
		if stateDone(m) {
			break
		}
	}
	if battleID == "" {
		t.Fatal("идентификатор боя не закреплён")
	}
	var n int
	var balance, winner string
	if err := st.DB.QueryRow(`SELECT COUNT(*), balance_version, winner FROM battles WHERE id=?`, battleID).Scan(&n, &balance, &winner); err != nil {
		t.Fatal(err)
	}
	if n != 1 || balance != "pinned-d8" || winner == "" {
		t.Fatalf("сохранено n=%d balance=%s winner=%s", n, balance, winner)
	}
	c.Close()
	c2 := dialLive(t, ts, token)
	again := readLive(t, c2, 2*time.Second)
	if again["type"] != "state" || stateDone(again) != true {
		t.Fatalf("результат не отдаётся повторно: %#v", again)
	}
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM battles WHERE id=?`, battleID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("повторное подключение создало ещё строки: %d %v", n, err)
	}
}

// D7: клиент рисует живой бой, а не HTTP-запись. В снимке должны быть состав
// (роль, имя, сторона) и разрешённые скорости — иначе сцена не из чего собрать героев.
func TestLiveStateIncludesRoster(t *testing.T) {
	ts, token := newLiveServer(t)
	c := dialLive(t, ts, token)
	hello := readLive(t, c, 2*time.Second)
	if hello["type"] != "hello" {
		t.Fatalf("hello: %#v", hello["type"])
	}
	if err := c.WriteJSON(map[string]any{"type": "start"}); err != nil {
		t.Fatal(err)
	}
	st := readLive(t, c, 2*time.Second)
	if st["type"] != "state" {
		t.Fatalf("state: %#v", st["type"])
	}
	roster, _ := st["roster"].([]any)
	if len(roster) != 10 {
		t.Fatalf("в снимке нет состава для отрисовки: %d", len(roster))
	}
	unit, _ := roster[0].(map[string]any)
	if unit["id"] == nil || unit["name"] == nil || unit["role"] == nil || unit["side"] == nil {
		t.Fatalf("герой без полей для сцены: %#v", unit)
	}
	speeds, _ := st["allowedSpeeds"].([]any)
	if len(speeds) == 0 || int(speeds[0].(float64)) != 1 {
		t.Fatalf("скорости: %#v", st["allowedSpeeds"])
	}
	c.Close()
	c2 := dialLive(t, ts, token)
	again := readLive(t, c2, 2*time.Second)
	if again["type"] != "state" || again["resumed"] != true {
		t.Fatalf("переподключение: %#v", again["type"])
	}
	roster2, _ := again["roster"].([]any)
	if len(roster2) != 10 {
		t.Fatalf("после переподключения состав потерян: %d", len(roster2))
	}
}
