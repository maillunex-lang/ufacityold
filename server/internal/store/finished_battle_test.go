package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSaveFinishedBattleGrantsOnce(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	var grants atomic.Int32
	prev := grantBattleReward
	grantBattleReward = func(tx *sql.Tx, accountID int64, battleID string) error {
		grants.Add(1)
		if accountID != id || battleID == "" {
			t.Errorf("награда: account=%d battle=%s", accountID, battleID)
		}
		return nil
	}
	t.Cleanup(func() { grantBattleReward = prev })
	const body = `{"balanceVersion":"pinned-d8","result":{"winner":"player","reason":"elimination","durationMs":100}}`
	var wg sync.WaitGroup
	var created atomic.Int32
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, _, err := s.SaveFinishedBattle(id, "battle-once", "pinned-d8", "player", []byte(body), 1)
			if err != nil {
				t.Errorf("save: %v", err)
				return
			}
			if ok {
				created.Add(1)
			}
		}()
	}
	wg.Wait()
	if created.Load() != 1 || grants.Load() != 1 {
		t.Fatalf("created=%d grants=%d, ожидалось по 1", created.Load(), grants.Load())
	}
	var n int
	var balance string
	if err := s.DB.QueryRow(`SELECT COUNT(*), balance_version FROM battles WHERE id=?`, "battle-once").Scan(&n, &balance); err != nil {
		t.Fatal(err)
	}
	if n != 1 || balance != "pinned-d8" {
		t.Fatalf("строк %d версия %s", n, balance)
	}
}
