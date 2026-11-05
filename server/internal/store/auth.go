package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
)

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

// CreateGuest создаёт гостевой аккаунт и сессию. Токен возвращается один раз, в БД — только хэш.
func (s *Store) CreateGuest() (accountID int64, token string, err error) {
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		return
	}
	token = hex.EncodeToString(b)
	tx, err := s.DB.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO accounts(is_guest) VALUES(1)`)
	if err != nil {
		return
	}
	if accountID, err = res.LastInsertId(); err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO account_progress(account_id) VALUES(?)`, accountID); err != nil {
		return
	}
	if _, err = tx.Exec(`INSERT INTO sessions(token_hash,account_id) VALUES(?,?)`, hashToken(token), accountID); err != nil {
		return
	}
	err = tx.Commit()
	return
}

var ErrUnauthorized = errors.New("unauthorized")

// AccountByToken проверяет токен сессии.
func (s *Store) AccountByToken(token string) (int64, error) {
	if len(token) != 64 {
		return 0, ErrUnauthorized
	}
	var id int64
	err := s.DB.QueryRow(`SELECT account_id FROM sessions WHERE token_hash=?`, hashToken(token)).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, ErrUnauthorized
	}
	return id, err
}
