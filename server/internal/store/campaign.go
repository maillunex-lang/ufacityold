package store

import (
	"database/sql"
	"lunexgame/internal/campaign"
)

func (s *Store) Home(accountID int64) (campaign.Home, error) {
	var h campaign.Home
	var heroes string
	err := s.DB.QueryRow(`SELECT gold,hero_xp,campaign_cleared FROM account_progress WHERE account_id=?`, accountID).Scan(&h.Gold, &h.HeroXP, &h.Cleared)
	if err == sql.ErrNoRows {
		return h, ErrNotFound
	}
	if err != nil {
		return h, err
	}
	rows, err := s.DB.Query(`SELECT hero_id FROM account_heroes WHERE account_id=? ORDER BY hero_id`, accountID)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		if err := rows.Scan(&heroes); err != nil {
			return h, err
		}
		h.Heroes = append(h.Heroes, heroes)
	}
	return h, rows.Err()
}

func (s *Store) Campaign(accountID int64) ([]campaign.Stage, error) {
	h, err := s.Home(accountID)
	if err != nil {
		return nil, err
	}
	return campaign.Stages(h.Cleared), nil
}

func (s *Store) ApplyCampaignWin(accountID int64, stage int) (int, bool, error) {
	if stage < 1 || stage > 10 {
		return 0, false, campaign.ErrLocked
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var cleared int
	if err = tx.QueryRow(`SELECT campaign_cleared FROM account_progress WHERE account_id=?`, accountID).Scan(&cleared); err != nil {
		return 0, false, err
	}
	if stage > cleared+1 {
		return 0, false, campaign.ErrLocked
	}
	if stage <= cleared {
		return 0, false, tx.Commit()
	}
	gold := stage * 40
	if _, err = tx.Exec(`UPDATE account_progress SET campaign_cleared=?,gold=gold+? WHERE account_id=?`, stage, gold, accountID); err != nil {
		return 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, false, err
	}
	return gold, true, nil
}
