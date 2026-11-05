package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"

	"lunexgame/internal/battle"
)

var ErrNotFound = errors.New("not found")
var ErrInvalidSpeed = errors.New("invalid speed")
var ErrSpeedForbidden = errors.New("speed forbidden")

// grantBattleReward — единственная точка будущего однократного начисления награды.
// Сейчас наград нет: функция обязана быть идемпотентной и вызывается в той же
// транзакции, что и первая запись результата. Повтор результата её не вызывает.
var grantBattleReward = func(tx *sql.Tx, accountID int64, battleID string) error { return nil }

type StoredBattle struct {
	OutcomeJSON []byte
	Playback    battle.PlaybackOptions
}

type rowReader interface{ QueryRow(string, ...any) *sql.Row }

func speedAccess(q rowReader, accountID int64) (battle.SpeedAccess, error) {
	var a battle.SpeedAccess
	err := q.QueryRow(`SELECT COALESCE(p.tutorial_completed,0),COALESCE(p.vip_level,0)
FROM accounts a LEFT JOIN account_progress p ON p.account_id=a.id WHERE a.id=?`, accountID).Scan(&a.TutorialCompleted, &a.VIPLevel)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return a, err
}

func (s *Store) BattleSpeedAccess(accountID int64) (battle.SpeedAccess, error) {
	return speedAccess(s.DB, accountID)
}

// SaveBattle сохраняет рассчитанный бой и возвращает его идентификатор.
func (s *Store) SaveBattle(accountID int64, balance, winner string, outcomeJSON []byte, speed int) (string, battle.PlaybackOptions, error) {
	var options battle.PlaybackOptions
	if !battle.ValidSpeed(speed) {
		return "", options, ErrInvalidSpeed
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", options, err
	}
	id := hex.EncodeToString(b)
	tx, err := s.DB.Begin()
	if err != nil {
		return "", options, err
	}
	defer tx.Rollback()
	access, err := speedAccess(tx, accountID)
	if err != nil {
		return "", options, err
	}
	options = access.Options(1)
	if !access.Allows(speed) {
		return "", options, ErrSpeedForbidden
	}
	if _, err = tx.Exec(`INSERT INTO battles(id,account_id,balance_version,winner,outcome_json,speed) VALUES(?,?,?,?,?,?)`,
		id, accountID, balance, winner, string(outcomeJSON), speed); err != nil {
		return "", options, err
	}
	if err = tx.Commit(); err != nil {
		return "", options, err
	}
	return id, access.Options(speed), nil
}

// GetBattle возвращает бой только его владельцу.
func (s *Store) GetBattle(accountID int64, id string) (StoredBattle, error) {
	var record StoredBattle
	tx, err := s.DB.Begin()
	if err != nil {
		return record, err
	}
	defer tx.Rollback()
	var js string
	var speed int
	err = tx.QueryRow(`SELECT outcome_json,speed FROM battles WHERE id=? AND account_id=?`, id, accountID).Scan(&js, &speed)
	if err == sql.ErrNoRows {
		return record, ErrNotFound
	}
	if err != nil {
		return record, err
	}
	access, err := speedAccess(tx, accountID)
	if err != nil {
		return record, err
	}
	if err = tx.Commit(); err != nil {
		return record, err
	}
	return StoredBattle{OutcomeJSON: []byte(js), Playback: access.Options(speed)}, nil
}

// SetBattleSpeed проверяет владельца и текущие права в той же транзакции, что
// изменяет скорость. Повтор не создаёт бой и не пересчитывает запись/награды.
func (s *Store) SetBattleSpeed(accountID int64, id string, speed int) (battle.PlaybackOptions, error) {
	var options battle.PlaybackOptions
	if !battle.ValidSpeed(speed) {
		return options, ErrInvalidSpeed
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return options, err
	}
	defer tx.Rollback()
	var old int
	err = tx.QueryRow(`SELECT speed FROM battles WHERE id=? AND account_id=?`, id, accountID).Scan(&old)
	if err == sql.ErrNoRows {
		return options, ErrNotFound
	}
	if err != nil {
		return options, err
	}
	access, err := speedAccess(tx, accountID)
	if err != nil {
		return options, err
	}
	options = access.Options(old)
	if !access.Allows(speed) {
		return options, ErrSpeedForbidden
	}
	if _, err = tx.Exec(`UPDATE battles SET speed=? WHERE id=? AND account_id=?`, speed, id, accountID); err != nil {
		return options, err
	}
	if err = tx.Commit(); err != nil {
		return options, err
	}
	return access.Options(speed), nil
}

// SaveFinishedBattle записывает результат живого боя один раз.
// Повтор с тем же id (в том числе параллельный) не создаёт вторую строку и не начисляет награду снова.
// balance — версия, закреплённая при старте боя, а не «текущая» на момент сохранения.
func (s *Store) SaveFinishedBattle(accountID int64, id, balance, winner string, outcomeJSON []byte, speed int) (bool, battle.PlaybackOptions, error) {
	var options battle.PlaybackOptions
	if !battle.ValidSpeed(speed) || id == "" || balance == "" || winner == "" || len(outcomeJSON) == 0 {
		return false, options, ErrInvalidSpeed
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return false, options, err
	}
	defer tx.Rollback()
	access, err := speedAccess(tx, accountID)
	if err != nil {
		return false, options, err
	}
	options = access.Options(speed)
	if !access.Allows(speed) {
		return false, options, ErrSpeedForbidden
	}
	_, err = tx.Exec(`INSERT INTO battles(id,account_id,balance_version,winner,outcome_json,speed) VALUES(?,?,?,?,?,?)`,
		id, accountID, balance, winner, string(outcomeJSON), speed)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			if err = tx.Commit(); err != nil {
				return false, options, err
			}
			return false, options, nil
		}
		return false, options, err
	}
	if err = grantBattleReward(tx, accountID, id); err != nil {
		return false, options, err
	}
	if err = tx.Commit(); err != nil {
		return false, options, err
	}
	return true, options, nil
}
