// Package store — SQLite-хранилище и миграции. Миграции только вперёд, каждая в транзакции;
// перед применением непустого набора миграций делается согласованная копия БД (VACUUM INTO).
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct{ DB *sql.DB }

// Open открывает БД (создаёт файл при необходимости) и применяет миграции.
// backupDir может быть пустым — тогда копия не делается (только для тестов/первого создания).
func Open(path, backupDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	_, statErr := os.Stat(path)
	existed := statErr == nil
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // один писатель; достаточно для текущей нагрузки
	s := &Store{DB: db}
	if err := s.migrate(existed, backupDir); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	ents, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range ents {
		n := e.Name()
		v, err := strconv.Atoi(strings.SplitN(n, "_", 2)[0])
		if err != nil {
			return nil, fmt.Errorf("имя миграции %q: %w", n, err)
		}
		b, _ := migrationsFS.ReadFile("migrations/" + n)
		ms = append(ms, migration{v, n, string(b)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

func (s *Store) migrate(dbExisted bool, backupDir string) error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return err
	}
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	var pending []migration
	for _, m := range ms {
		var x int
		err := s.DB.QueryRow(`SELECT version FROM schema_migrations WHERE version=?`, m.version).Scan(&x)
		if err == sql.ErrNoRows {
			pending = append(pending, m)
		} else if err != nil {
			return err
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if dbExisted && backupDir != "" {
		if err := os.MkdirAll(backupDir, 0o750); err != nil {
			return err
		}
		dst := filepath.Join(backupDir, fmt.Sprintf("pre-migration-%s-v%d.db", time.Now().UTC().Format("20060102T150405Z"), pending[0].version))
		if _, err := s.DB.Exec(`VACUUM INTO ?`, dst); err != nil {
			return fmt.Errorf("резервная копия перед миграцией: %w", err)
		}
	}
	for _, m := range pending {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("миграция %s: %w", m.name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,name,applied_at) VALUES(?,?,?)`, m.version, m.name, time.Now().UTC().Format(time.RFC3339)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// SchemaVersion возвращает последнюю применённую версию.
func (s *Store) SchemaVersion() (int, error) {
	var v sql.NullInt64
	err := s.DB.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v)
	return int(v.Int64), err
}
