package store

import (
	"database/sql"
	"errors"
)

var ErrTowerLocked = errors.New("tower floor locked")

type TowerFloor struct {
	Floor int  `json:"floor"`
	Open  bool `json:"open"`
	Boss  bool `json:"boss"`
}

func (s *Store) Tower(accountID int64) (int, []TowerFloor, error) {
	var cleared int
	if err := s.DB.QueryRow(`SELECT floor_cleared FROM tower_progress WHERE account_id=?`, accountID).Scan(&cleared); err != nil {
		return 0, nil, err
	}
	floors := make([]TowerFloor, 20)
	for i := range floors {
		floor := i + 1
		floors[i] = TowerFloor{Floor: floor, Open: floor <= cleared+1, Boss: floor%5 == 0}
	}
	return cleared, floors, nil
}

func (s *Store) ApplyTowerWin(accountID int64, floor int) (gold, xp int, granted bool, err error) {
	if floor < 1 {
		return 0, 0, false, ErrTowerLocked
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()
	var cleared int
	if err = tx.QueryRow(`SELECT floor_cleared FROM tower_progress WHERE account_id=?`, accountID).Scan(&cleared); err == sql.ErrNoRows {
		return 0, 0, false, ErrNotFound
	}
	if err != nil {
		return 0, 0, false, err
	}
	if floor > cleared+1 {
		return 0, 0, false, ErrTowerLocked
	}
	if floor <= cleared {
		return 0, 0, false, tx.Commit()
	}
	gold, xp = 100+floor*10, floor*5
	if _, err = tx.Exec(`UPDATE tower_progress SET floor_cleared=? WHERE account_id=?`, floor, accountID); err != nil {
		return 0, 0, false, err
	}
	if _, err = tx.Exec(`UPDATE account_progress SET gold=gold+?,hero_xp=hero_xp+? WHERE account_id=?`, gold, xp, accountID); err != nil {
		return 0, 0, false, err
	}
	if err = tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	return gold, xp, true, nil
}
