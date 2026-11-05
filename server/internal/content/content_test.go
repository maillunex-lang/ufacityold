package content

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lunexgame/internal/store"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Store{DB: st.DB}
}

func validate(t *testing.T, kind, data string) []string {
	t.Helper()
	k := Find(kind)
	if k == nil {
		t.Fatal("нет вида", kind)
	}
	return k.Validate([]byte(data))
}

func TestKindExamplesAreValid(t *testing.T) {
	for _, k := range Kinds {
		if errs := k.Validate(k.Example); len(errs) != 0 {
			t.Errorf("пример вида %s невалиден: %v", k.Name, errs)
		}
	}
}

func TestEnergyValidation(t *testing.T) {
	ok := `{"max":100,"perDealt":0.08,"perTaken":0.06,"perHealed":0.05}`
	if e := validate(t, "energy", ok); len(e) != 0 {
		t.Fatal(e)
	}
	bad := map[string]string{
		"нет поля":             `{"max":100,"perDealt":0.1,"perTaken":0.1}`,
		"max ноль":             `{"max":0,"perDealt":0.1,"perTaken":0.1,"perHealed":0.1}`,
		"отрицательное":        `{"max":100,"perDealt":-1,"perTaken":0.1,"perHealed":0.1}`,
		"огромное":             `{"max":100,"perDealt":1e12,"perTaken":0.1,"perHealed":0.1}`,
		"лишнее поле":          `{"max":100,"perDealt":0.1,"perTaken":0.1,"perHealed":0.1,"secret":1}`,
		"не JSON":              `{max:100}`,
		"хвост после значения": ok + ` {"x":1}`,
		"строка вместо числа":  `{"max":"100","perDealt":0.1,"perTaken":0.1,"perHealed":0.1}`,
		"массив":               `[1,2]`,
	}
	for name, data := range bad {
		if e := validate(t, "energy", data); len(e) == 0 {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
}

func heroJSON(mod func(map[string]any)) string {
	h := map[string]any{"id": "guardian", "name": "Страж", "role": "tank", "rarity": "rare", "faction": "", "enemyOnly": false,
		"description": "d", "art": map[string]any{"color": "#5b7fa6"},
		"stats": map[string]any{"maxHp": 4500, "attack": 60, "intervalMs": 1500, "melee": true, "attackRange": 1100, "runSpeed": 3000}}
	if mod != nil {
		mod(h)
	}
	b, _ := json.Marshal(map[string]any{"schema": 1, "heroes": []any{h}})
	return string(b)
}

func TestHeroesValidation(t *testing.T) {
	if e := validate(t, "heroes", heroJSON(nil)); len(e) != 0 {
		t.Fatal(e)
	}
	cases := map[string]func(map[string]any){
		"плохой id":         func(h map[string]any) { h["id"] = "Bad ID" },
		"пустое имя":        func(h map[string]any) { h["name"] = "  " },
		"роль":              func(h map[string]any) { h["role"] = "ninja" },
		"редкость":          func(h map[string]any) { h["rarity"] = "mythic" },
		"цвет":              func(h map[string]any) { h["art"] = map[string]any{"color": "red"} },
		"здоровье 0":        func(h map[string]any) { h["stats"].(map[string]any)["maxHp"] = 0 },
		"интервал мал":      func(h map[string]any) { h["stats"].(map[string]any)["intervalMs"] = 1 },
		"танк дальнего боя": func(h map[string]any) { h["stats"].(map[string]any)["melee"] = false },
		"маг ближнего боя":  func(h map[string]any) { h["role"] = "mage" },
		"длинное описание":  func(h map[string]any) { h["description"] = strings.Repeat("я", 501) },
		"нет stats.melee":   func(h map[string]any) { delete(h["stats"].(map[string]any), "melee") },
		"неизвестное поле":  func(h map[string]any) { h["power"] = 9000 },
	}
	for name, mod := range cases {
		if e := validate(t, "heroes", heroJSON(mod)); len(e) == 0 {
			t.Errorf("%s: ожидалась ошибка", name)
		}
	}
	// повторный id и пустой список
	dup := `{"schema":1,"heroes":[` + strings.TrimSuffix(strings.TrimPrefix(heroJSON(nil), `{"schema":1,"heroes":[`), `]}`) + `,` +
		strings.TrimSuffix(strings.TrimPrefix(heroJSON(nil), `{"schema":1,"heroes":[`), `]}`) + `]}`
	if e := validate(t, "heroes", dup); len(e) == 0 {
		t.Error("повторный id не найден")
	}
	if e := validate(t, "heroes", `{"schema":1,"heroes":[]}`); len(e) == 0 {
		t.Error("пустой список")
	}
	if e := validate(t, "heroes", `{"schema":2,"heroes":[]}`); len(e) == 0 {
		t.Error("схема")
	}
}

func TestPublishHistoryRollback(t *testing.T) {
	s := newStore(t)
	if h, err := s.Head("energy"); err != nil || h != nil {
		t.Fatal("в начале ничего не опубликовано", h, err)
	}
	v1 := `{"max":100,"perDealt":0.08,"perTaken":0.06,"perHealed":0.05}`
	v2 := `{ "max": 150, "perDealt": 0.1, "perTaken": 0.06, "perHealed": 0.05 }`
	n, err := s.Publish("energy", []byte(v1), "первая", "publish", nil, 0)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// конфликт: ожидали пустую историю, а версия 1 уже есть
	if _, err := s.Publish("energy", []byte(v2), "", "publish", nil, 0); err != ErrConflict {
		t.Fatal("ожидался конфликт, есть", err)
	}
	n, err = s.Publish("energy", []byte(v2), "вторая", "publish", nil, 1)
	if err != nil || n != 2 {
		t.Fatal(n, err)
	}
	head, _ := s.Head("energy")
	if head.Version != 2 || strings.Contains(string(head.Data), " ") {
		t.Fatalf("данные должны храниться в компактном виде: %s", head.Data)
	}
	// откат = новая версия 3 с копией данных версии 1; версии 1 и 2 остаются
	old, err := s.Version("energy", 1)
	if err != nil {
		t.Fatal(err)
	}
	from := 1
	n, err = s.Publish("energy", old.Data, "возврат", "rollback", &from, 2)
	if err != nil || n != 3 {
		t.Fatal(n, err)
	}
	head, _ = s.Head("energy")
	if head.Version != 3 || head.Source != "rollback" || head.RolledBackFrom == nil || *head.RolledBackFrom != 1 || string(head.Data) != string(old.Data) {
		t.Fatalf("%+v", head)
	}
	hist, err := s.History("energy", 50)
	if err != nil || len(hist) != 3 || hist[0].Version != 3 || hist[2].Version != 1 {
		t.Fatal(hist, err)
	}
	if _, err := s.Version("energy", 99); err != ErrNotFound {
		t.Fatal("несуществующая версия")
	}
	// другой вид контента версионируется независимо
	if h, _ := s.Head("heroes"); h != nil {
		t.Fatal("heroes не должны затрагиваться")
	}
}

func TestDraftLifecycle(t *testing.T) {
	s := newStore(t)
	if d, err := s.GetDraft("energy"); err != nil || d != nil {
		t.Fatal(d, err)
	}
	if err := s.SaveDraft("energy", []byte(`{"max": 1}`), 0); err != nil { // невалидное по смыслу, но допустимое как черновик
		t.Fatal(err)
	}
	if err := s.SaveDraft("energy", []byte(`{"max": 2}`), 0); err != nil {
		t.Fatal(err)
	}
	d, _ := s.GetDraft("energy")
	if string(d.Data) != `{"max":2}` {
		t.Fatal(string(d.Data))
	}
	if err := s.SaveDraft("energy", []byte(`не json`), 0); err == nil {
		t.Fatal("мусор не должен сохраняться")
	}
	// публикация удаляет черновик
	if _, err := s.Publish("energy", []byte(`{"max":3}`), "", "publish", nil, 0); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.GetDraft("energy"); d != nil {
		t.Fatal("черновик должен быть удалён после публикации")
	}
}

func TestAuditLog(t *testing.T) {
	s := newStore(t)
	s.Audit("publish", "energy", 1, "1.2.3.4", "заметка")
	s.Audit("login", "", 0, "1.2.3.4", "")
	rows, err := s.AuditLog(10)
	if err != nil || len(rows) != 2 || rows[0].Action != "login" {
		t.Fatal(rows, err)
	}
}
