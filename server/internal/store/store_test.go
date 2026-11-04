package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateAndBackup(t *testing.T) {
	d := t.TempDir()
	p, bk := filepath.Join(d, "t.db"), filepath.Join(d, "bk")
	s, err := Open(p, bk)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := s.SchemaVersion(); v != 2 {
		t.Fatalf("версия %d", v)
	}
	if ents, _ := os.ReadDir(bk); len(ents) != 0 {
		t.Fatal("для новой БД копия не нужна")
	}
	s.Close()
	// повторное открытие: миграций нет — копии нет и повторов нет
	s, err = Open(p, bk)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if ents, _ := os.ReadDir(bk); len(ents) != 0 {
		t.Fatal("копия без миграций")
	}
}

func TestGuestAuth(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, tok, err := s.CreateGuest()
	if err != nil || id == 0 || len(tok) != 64 {
		t.Fatal(id, tok, err)
	}
	got, err := s.AccountByToken(tok)
	if err != nil || got != id {
		t.Fatal(got, err)
	}
	if _, err := s.AccountByToken(tok[:63] + "x"); err != ErrUnauthorized {
		t.Fatal("неверный токен принят")
	}
	var h string
	s.DB.QueryRow(`SELECT token_hash FROM sessions`).Scan(&h)
	if h == tok {
		t.Fatal("токен хранится в открытом виде")
	}
}

func TestBackupOfExistingDB(t *testing.T) {
	d := t.TempDir()
	p, bk := filepath.Join(d, "t.db"), filepath.Join(d, "bk")
	// «старая» БД без таблицы миграций, но уже существующий файл → перед миграцией нужна копия
	s0, err := Open(p, "")
	if err != nil {
		t.Fatal(err)
	}
	s0.DB.Exec(`DROP TABLE battles`)
	s0.DB.Exec(`DROP TABLE sessions`)
	s0.DB.Exec(`DROP TABLE accounts`)
	s0.DB.Exec(`DELETE FROM schema_migrations`)
	s0.Close()
	s, err := Open(p, bk)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ents, _ := os.ReadDir(bk); len(ents) != 1 {
		t.Fatalf("ожидалась 1 копия, есть %d", len(ents))
	}
}
