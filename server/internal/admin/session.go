package admin

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"time"
)

const sessionTTL = 8 * time.Hour

type session struct {
	CSRF string
}

func hashToken(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Service) newSession(ip string) (token, csrf string, err error) {
	if token, err = randomHex(32); err != nil {
		return
	}
	if csrf, err = randomHex(16); err != nil {
		return
	}
	now := s.now().UTC()
	// заодно чистим просроченные
	_, _ = s.DB.Exec(`DELETE FROM admin_sessions WHERE expires_at < ?`, now.Format(time.RFC3339))
	_, err = s.DB.Exec(`INSERT INTO admin_sessions(token_hash,csrf,ip,created_at,expires_at) VALUES(?,?,?,?,?)`,
		hashToken(token), csrf, ip, now.Format(time.RFC3339), now.Add(sessionTTL).Format(time.RFC3339))
	return
}

func (s *Service) lookupSession(token string) (*session, bool) {
	if len(token) != 64 {
		return nil, false
	}
	var csrf, exp string
	err := s.DB.QueryRow(`SELECT csrf,expires_at FROM admin_sessions WHERE token_hash=?`, hashToken(token)).Scan(&csrf, &exp)
	if err == sql.ErrNoRows || err != nil {
		return nil, false
	}
	t, err := time.Parse(time.RFC3339, exp)
	if err != nil || !s.now().Before(t) {
		_, _ = s.DB.Exec(`DELETE FROM admin_sessions WHERE token_hash=?`, hashToken(token))
		return nil, false
	}
	return &session{CSRF: csrf}, true
}

func (s *Service) deleteSession(token string) {
	_, _ = s.DB.Exec(`DELETE FROM admin_sessions WHERE token_hash=?`, hashToken(token))
}

func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
