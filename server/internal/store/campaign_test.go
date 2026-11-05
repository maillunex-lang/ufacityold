package store

import (
	"lunexgame/internal/campaign"
	"path/filepath"
	"testing"
	"time"
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
	gold, xp, granted, err := s.ApplyCampaignWin(id, 1)
	if err != nil || !granted || gold != 40 || xp != 20 {
		t.Fatalf("first: %d %v %v", gold, granted, err)
	}
	gold, xp, granted, err = s.ApplyCampaignWin(id, 1)
	if err != nil || granted || gold != 0 {
		t.Fatalf("repeat: %d %v %v", gold, granted, err)
	}
	if _, _, _, err = s.ApplyCampaignWin(id, 3); err != campaign.ErrLocked {
		t.Fatal("closed stage accepted")
	}
	h, err := s.Home(id)
	if err != nil || h.Gold != 40 || h.HeroXP != 20 || h.Cleared != 1 || len(h.Heroes) != 2 {
		t.Fatalf("home=%+v err=%v", h, err)
	}
	state, cg, cx, err := s.LevelUpHero(id, "guardian")
	if err != nil || state.Level != 2 || cg != 10 || cx != 10 {
		t.Fatalf("level up: %+v %d %d %v", state, cg, cx, err)
	}
	if _, _, _, err = s.LevelUpHero(id, "unknown"); err != ErrNotFound {
		t.Fatalf("unknown hero: %v", err)
	}
}

func TestAFKClaimIsCappedAndRepeatSafe(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "afk.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	base := time.Unix(100000, 0)
	if gold, elapsed, err := s.ClaimAFK(id, base); err != nil || gold != 0 || elapsed != 0 {
		t.Fatalf("init: %d %d %v", gold, elapsed, err)
	}
	if _, err := s.DB.Exec(`UPDATE account_progress SET afk_last_claim=? WHERE account_id=?`, base.Unix()-24*60*60, id); err != nil {
		t.Fatal(err)
	}
	gold, elapsed, err := s.ClaimAFK(id, base)
	if err != nil || gold != 120 || elapsed != 12*60*60 {
		t.Fatalf("cap: %d %d %v", gold, elapsed, err)
	}
	gold, elapsed, err = s.ClaimAFK(id, base)
	if err != nil || gold != 0 || elapsed != 0 {
		t.Fatalf("repeat: %d %d %v", gold, elapsed, err)
	}
}
