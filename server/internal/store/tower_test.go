package store

import (
	"path/filepath"
	"testing"
)

func TestTowerProgressBossAndOneTimeReward(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "tower.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	cleared, floors, err := s.Tower(id)
	if err != nil || cleared != 0 || len(floors) != 20 || !floors[0].Open || floors[1].Open || !floors[4].Boss {
		t.Fatalf("tower=%+v cleared=%d err=%v", floors, cleared, err)
	}
	gold, xp, granted, err := s.ApplyTowerWin(id, 1)
	if err != nil || !granted || gold != 110 || xp != 5 {
		t.Fatalf("first=%d,%d,%v,%v", gold, xp, granted, err)
	}
	gold, xp, granted, err = s.ApplyTowerWin(id, 1)
	if err != nil || granted || gold != 0 || xp != 0 {
		t.Fatalf("repeat=%d,%d,%v,%v", gold, xp, granted, err)
	}
	if _, _, _, err = s.ApplyTowerWin(id, 3); err != ErrTowerLocked {
		t.Fatalf("locked=%v", err)
	}
	cleared, floors, err = s.Tower(id)
	if err != nil || cleared != 1 || !floors[1].Open || floors[4].Open {
		t.Fatalf("after=%+v cleared=%d err=%v", floors, cleared, err)
	}
}
