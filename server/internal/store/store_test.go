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
	if v, _ := s.SchemaVersion(); v != 3 {
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

func TestBattleSpeedAccessAndIdempotentUpdate(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	id, _, err := s.CreateGuest()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SaveBattle(id, "v1", "player", []byte(`{"result":{}}`), 2); err != ErrSpeedForbidden {
		t.Fatalf("x2 без обучения: %v", err)
	}
	if _, _, err := s.SaveBattle(id, "v1", "player", []byte(`{"result":{}}`), 3); err != ErrInvalidSpeed {
		t.Fatalf("x3: %v", err)
	}
	if _, err := s.DB.Exec(`UPDATE account_progress SET tutorial_completed=1,vip_level=2 WHERE account_id=?`, id); err != nil {
		t.Fatal(err)
	}
	battleID, options, err := s.SaveBattle(id, "v1", "player", []byte(`{"result":{}}`), 4)
	if err != nil || battleID == "" || options.Speed != 4 || len(options.AllowedSpeeds) != 3 {
		t.Fatalf("x4: id=%q options=%+v err=%v", battleID, options, err)
	}
	updated, err := s.SetBattleSpeed(id, battleID, 2)
	if err != nil || updated.Speed != 2 {
		t.Fatalf("переключение: %+v %v", updated, err)
	}
	if _, err := s.SetBattleSpeed(id, battleID, 2); err != nil {
		t.Fatalf("повторное переключение: %v", err)
	}
	if _, err := s.SetBattleSpeed(id+1, battleID, 1); err != ErrNotFound {
		t.Fatalf("чужой бой: %v", err)
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
	s0.DB.Exec(`DROP TABLE account_progress`)
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
