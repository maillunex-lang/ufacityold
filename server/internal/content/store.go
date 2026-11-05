package content

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	ErrNotFound = errors.New("не найдено")
	ErrConflict = errors.New("опубликованная версия изменилась")
)

type Version struct {
	Version        int             `json:"version"`
	Data           json.RawMessage `json:"data,omitempty"`
	Note           string          `json:"note"`
	Source         string          `json:"source"`
	RolledBackFrom *int            `json:"rolledBackFrom,omitempty"`
	CreatedAt      string          `json:"createdAt"`
}

type Draft struct {
	Data        json.RawMessage `json:"data"`
	BaseVersion int             `json:"baseVersion"`
	UpdatedAt   string          `json:"updatedAt"`
}

type Store struct{ DB *sql.DB }

func compact(data []byte) ([]byte, error) {
	var b bytes.Buffer
	if err := json.Compact(&b, data); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Head — текущая опубликованная версия; nil, если ничего не опубликовано.
func (s *Store) Head(kind string) (*Version, error) {
	return s.version(kind, 0)
}

// Version возвращает конкретную версию (0 — последнюю).
func (s *Store) version(kind string, v int) (*Version, error) {
	q := `SELECT version,data,note,source,rolled_back_from,created_at FROM content_versions WHERE kind=?`
	args := []any{kind}
	if v > 0 {
		q += ` AND version=?`
		args = append(args, v)
	} else {
		q += ` ORDER BY version DESC LIMIT 1`
	}
	var x Version
	var data string
	var rb sql.NullInt64
	err := s.DB.QueryRow(q, args...).Scan(&x.Version, &data, &x.Note, &x.Source, &rb, &x.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	x.Data = json.RawMessage(data)
	if rb.Valid {
		n := int(rb.Int64)
		x.RolledBackFrom = &n
	}
	return &x, nil
}

func (s *Store) Version(kind string, v int) (*Version, error) {
	if v < 1 {
		return nil, ErrNotFound
	}
	x, err := s.version(kind, v)
	if err == nil && x == nil {
		return nil, ErrNotFound
	}
	return x, err
}

// History — версии от новых к старым, без тел данных (для списка).
func (s *Store) History(kind string, limit int) ([]Version, error) {
	rows, err := s.DB.Query(`SELECT version,note,source,rolled_back_from,created_at FROM content_versions WHERE kind=? ORDER BY version DESC LIMIT ?`, kind, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var x Version
		var rb sql.NullInt64
		if err := rows.Scan(&x.Version, &x.Note, &x.Source, &rb, &x.CreatedAt); err != nil {
			return nil, err
		}
		if rb.Valid {
			n := int(rb.Int64)
			x.RolledBackFrom = &n
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) GetDraft(kind string) (*Draft, error) {
	var d Draft
	var data string
	err := s.DB.QueryRow(`SELECT data,base_version,updated_at FROM content_drafts WHERE kind=?`, kind).Scan(&data, &d.BaseVersion, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.Data = json.RawMessage(data)
	return &d, nil
}

// SaveDraft сохраняет черновик (допускаются непроверенные данные — публикация их всё равно проверит).
func (s *Store) SaveDraft(kind string, data []byte, base int) error {
	c, err := compact(data)
	if err != nil {
		return fmt.Errorf("некорректный JSON: %w", err)
	}
	_, err = s.DB.Exec(`INSERT INTO content_drafts(kind,data,base_version) VALUES(?,?,?)
ON CONFLICT(kind) DO UPDATE SET data=excluded.data, base_version=excluded.base_version, updated_at=strftime('%Y-%m-%dT%H:%M:%SZ','now')`,
		kind, string(c), base)
	return err
}

func (s *Store) DeleteDraft(kind string) error {
	_, err := s.DB.Exec(`DELETE FROM content_drafts WHERE kind=?`, kind)
	return err
}

// Publish создаёт новую версию. expectedHead — версия, на которую опирался владелец (0 — ничего не было);
// если за это время появилась другая, возвращается ErrConflict. Черновик удаляется.
// Данные проверяет вызывающий (см. Kind.Validate); здесь они только нормализуются.
func (s *Store) Publish(kind string, data []byte, note, source string, rolledBackFrom *int, expectedHead int) (int, error) {
	c, err := compact(data)
	if err != nil {
		return 0, fmt.Errorf("некорректный JSON: %w", err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var head sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(version) FROM content_versions WHERE kind=?`, kind).Scan(&head); err != nil {
		return 0, err
	}
	if int(head.Int64) != expectedHead {
		return 0, ErrConflict
	}
	next := int(head.Int64) + 1
	var rb any
	if rolledBackFrom != nil {
		rb = *rolledBackFrom
	}
	if _, err := tx.Exec(`INSERT INTO content_versions(kind,version,data,note,source,rolled_back_from) VALUES(?,?,?,?,?,?)`,
		kind, next, string(c), note, source, rb); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM content_drafts WHERE kind=?`, kind); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return next, nil
}

func (s *Store) Audit(action, kind string, version int, ip, detail string) {
	_, _ = s.DB.Exec(`INSERT INTO admin_audit(action,kind,version,ip,detail) VALUES(?,?,?,?,?)`, action, kind, version, ip, detail)
}

type AuditRow struct {
	At      string `json:"at"`
	Action  string `json:"action"`
	Kind    string `json:"kind"`
	Version int    `json:"version"`
	IP      string `json:"ip"`
	Detail  string `json:"detail"`
}

func (s *Store) AuditLog(limit int) ([]AuditRow, error) {
	rows, err := s.DB.Query(`SELECT at,action,kind,version,ip,detail FROM admin_audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditRow{}
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.At, &r.Action, &r.Kind, &r.Version, &r.IP, &r.Detail); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
