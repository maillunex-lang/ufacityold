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

func TestLiveBattleReconnectContinuesSession(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "live.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	_, token, err := st.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(Build{}, Config{Store: st}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	u.Scheme = "ws"
	u.Path = Prefix + "/api/battle/live"
	d := websocket.DefaultDialer
	h := map[string][]string{"Authorization": {"Bearer " + token}}
	c, _, err := d.Dial(u.String(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	var hello map[string]any
	if err := c.ReadJSON(&hello); err != nil {
		t.Fatal(err)
	}
	if hello["type"] != "state" {
		t.Fatalf("hello: %#v", hello)
	}
	var tick map[string]any
	if err := c.ReadJSON(&tick); err != nil {
		t.Fatal(err)
	}
	state1 := tick["state"].(map[string]any)
	t1 := int(state1["t"].(float64))
	c.Close()
	time.Sleep(250 * time.Millisecond)
	c2, _, err := d.Dial(u.String(), h)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	var resumed map[string]any
	if err := c2.ReadJSON(&resumed); err != nil {
		t.Fatal(err)
	}
	state2 := resumed["state"].(map[string]any)
	if int(state2["t"].(float64)) <= t1 {
		t.Fatalf("бой не продолжился: %d <= %d", int(state2["t"].(float64)), t1)
	}
}
