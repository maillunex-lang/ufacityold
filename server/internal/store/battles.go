package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
)

var ErrNotFound = errors.New("not found")

// SaveBattle сохраняет рассчитанный бой и возвращает его идентификатор.
func (s *Store) SaveBattle(accountID int64, balance, winner string, outcomeJSON []byte) (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	_, err := s.DB.Exec(`INSERT INTO battles(id,account_id,balance_version,winner,outcome_json) VALUES(?,?,?,?,?)`,
		id, accountID, balance, winner, string(outcomeJSON))
	return id, err
}

// GetBattle возвращает бой только его владельцу.
func (s *Store) GetBattle(accountID int64, id string) ([]byte, error) {
	var js string
	err := s.DB.QueryRow(`SELECT outcome_json FROM battles WHERE id=? AND account_id=?`, id, accountID).Scan(&js)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	return []byte(js), err
}
