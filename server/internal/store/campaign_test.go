package store

import (
	"lunexgame/internal/campaign"
	"path/filepath"
	"testing"
)

func TestCampaignProgressAndOneTimeReward(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	stages, err := s.Campaign(id)
	if err != nil || len(stages) != 10 || !stages[0].Open || stages[1].Open {
		t.Fatalf("stages=%+v err=%v", stages, err)
	}
	gold, granted, err := s.ApplyCampaignWin(id, 1)
	if err != nil || !granted || gold != 40 {
		t.Fatalf("first: %d %v %v", gold, granted, err)
	}
	gold, granted, err = s.ApplyCampaignWin(id, 1)
	if err != nil || granted || gold != 0 {
		t.Fatalf("repeat: %d %v %v", gold, granted, err)
	}
	if _, _, err = s.ApplyCampaignWin(id, 3); err != campaign.ErrLocked {
		t.Fatal("closed stage accepted")
	}
	h, err := s.Home(id)
	if err != nil || h.Gold != 40 || h.Cleared != 1 || len(h.Heroes) != 2 {
		t.Fatalf("home=%+v err=%v", h, err)
	}
}
