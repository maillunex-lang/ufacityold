package store

import (
	"database/sql"
	"errors"
	"lunexgame/internal/campaign"
	"time"
)

func (s *Store) Home(accountID int64) (campaign.Home, error) {
	var h campaign.Home
	err := s.DB.QueryRow(`SELECT gold,hero_xp,campaign_cleared FROM account_progress WHERE account_id=?`, accountID).Scan(&h.Gold, &h.HeroXP, &h.Cleared)
	if err == sql.ErrNoRows {
		return h, ErrNotFound
	}
	if err != nil {
		return h, err
	}
	rows, err := s.DB.Query(`SELECT hero_id,level,xp FROM account_heroes WHERE account_id=? ORDER BY hero_id`, accountID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var heroes string
		var level, xp int
		if err := rows.Scan(&heroes, &level, &xp); err != nil {
			return h, err
		}
		h.Heroes = append(h.Heroes, heroes)
		h.HeroStates = append(h.HeroStates, campaign.HeroState{ID: heroes, Level: level, XP: xp})
	}
	return h, rows.Err()
}

const afkCapSeconds int64 = 12 * 60 * 60
const afkGoldPerHour int64 = 10

func (s *Store) ClaimAFK(accountID int64, now time.Time) (int, int64, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	var last int64
	if err = tx.QueryRow(`SELECT afk_last_claim FROM account_progress WHERE account_id=?`, accountID).Scan(&last); err == sql.ErrNoRows {
		return 0, 0, ErrNotFound
	}
	if err != nil {
		return 0, 0, err
	}
	current := now.Unix()
	if last == 0 {
		if _, err = tx.Exec(`UPDATE account_progress SET afk_last_claim=? WHERE account_id=?`, current, accountID); err != nil {
			return 0, 0, err
		}
		if err = tx.Commit(); err != nil {
			return 0, 0, err
		}
		return 0, 0, nil
	}
	if current < last {
		current = last
	}
	elapsed := current - last
	if elapsed > afkCapSeconds {
		elapsed = afkCapSeconds
	}
	reward := int(elapsed * afkGoldPerHour / 3600)
	if _, err = tx.Exec(`UPDATE account_progress SET afk_last_claim=?,gold=gold+? WHERE account_id=?`, current, reward, accountID); err != nil {
		return 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, err
	}
	return reward, elapsed, nil
}

func (s *Store) Campaign(accountID int64) ([]campaign.Stage, error) {
	h, err := s.Home(accountID)
	if err != nil {
		return nil, err
	}
	return campaign.Stages(h.Cleared), nil
}

func (s *Store) ApplyCampaignWin(accountID int64, stage int) (int, int, bool, error) {
	if stage < 1 || stage > 10 {
		return 0, 0, false, campaign.ErrLocked
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()
	var cleared int
	if err = tx.QueryRow(`SELECT campaign_cleared FROM account_progress WHERE account_id=?`, accountID).Scan(&cleared); err != nil {
		return 0, 0, false, err
	}
	if stage > cleared+1 {
		return 0, 0, false, campaign.ErrLocked
	}
	if stage <= cleared {
		return 0, 0, false, tx.Commit()
	}
	gold := stage * 40
	xp := stage * 20
	if _, err = tx.Exec(`UPDATE account_progress SET campaign_cleared=?,gold=gold+?,hero_xp=hero_xp+? WHERE account_id=?`, stage, gold, xp, accountID); err != nil {
		return 0, 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	return gold, xp, true, nil
}

func (s *Store) LevelUpHero(accountID int64, heroID string) (campaign.HeroState, int, int, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	defer tx.Rollback()
	var level, xp, gold, heroXP int
	if err = tx.QueryRow(`SELECT level,xp FROM account_heroes WHERE account_id=? AND hero_id=?`, accountID, heroID).Scan(&level, &xp); err == sql.ErrNoRows {
		return campaign.HeroState{}, 0, 0, ErrNotFound
	}
	if err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	if level >= 100 {
		return campaign.HeroState{ID: heroID, Level: level, XP: xp}, 0, 0, nil
	}
	if err = tx.QueryRow(`SELECT gold,hero_xp FROM account_progress WHERE account_id=?`, accountID).Scan(&gold, &heroXP); err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	costGold, costXP := level*10, level*10
	if gold < costGold || heroXP < costXP {
		return campaign.HeroState{ID: heroID, Level: level, XP: xp}, costGold, costXP, errors.New("insufficient resources")
	}
	if _, err = tx.Exec(`UPDATE account_progress SET gold=gold-?,hero_xp=hero_xp-? WHERE account_id=?`, costGold, costXP, accountID); err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	if _, err = tx.Exec(`UPDATE account_heroes SET level=level+1 WHERE account_id=? AND hero_id=?`, accountID, heroID); err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	if err = tx.Commit(); err != nil {
		return campaign.HeroState{}, 0, 0, err
	}
	return campaign.HeroState{ID: heroID, Level: level + 1, XP: xp}, costGold, costXP, nil
}
