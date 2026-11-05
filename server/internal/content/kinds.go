// Package content — версионированные игровые данные, которыми управляет админка:
// черновик → проверка → предпросмотр → ручная публикация → история и возврат.
// Возврат конфигурации создаёт НОВУЮ версию с копией старых данных и не затрагивает прогресс игроков.
package content

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Kind описывает вид контента и его проверку. Добавление вида — это добавление записи в Kinds
// (и, при необходимости, предпросмотра на стороне приложения).
type Kind struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Example     json.RawMessage `json:"example"`
	// Validate возвращает список понятных владельцу ошибок; пустой — данные корректны.
	Validate func(data []byte) []string `json:"-"`
}

// Kinds — порядок важен (так показываются во вкладках).
var Kinds = []*Kind{energyKind, heroesKind}

func Find(name string) *Kind {
	for _, k := range Kinds {
		if k.Name == name {
			return k
		}
	}
	return nil
}

// strictDecode: ровно одно значение JSON, без неизвестных полей и «хвоста».
func strictDecode(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return fmt.Errorf("после данных есть лишний текст")
	}
	return nil
}

func jsonProblem(err error) []string {
	return []string{"некорректный JSON: " + err.Error()}
}

// ---- energy: коэффициенты энергии боя ----

type energyData struct {
	Max       *float64 `json:"max"`
	PerDealt  *float64 `json:"perDealt"`
	PerTaken  *float64 `json:"perTaken"`
	PerHealed *float64 `json:"perHealed"`
}

var energyKind = &Kind{
	Name:        "energy",
	Title:       "Энергия боя",
	Description: "Запас энергии для главной способности и коэффициенты начисления: за нанесённый урон, полученный урон и фактически восстановленное здоровье. Действует на новые бои.",
	Example:     json.RawMessage(`{"max":100,"perDealt":0.08,"perTaken":0.06,"perHealed":0.05}`),
	Validate: func(data []byte) []string {
		var d energyData
		if err := strictDecode(data, &d); err != nil {
			return jsonProblem(err)
		}
		var errs []string
		check := func(name string, v *float64, minV float64, allowZero bool) {
			switch {
			case v == nil:
				errs = append(errs, fmt.Sprintf("поле %q обязательно", name))
			case math.IsNaN(*v) || math.IsInf(*v, 0):
				errs = append(errs, fmt.Sprintf("поле %q должно быть числом", name))
			case *v < minV || (!allowZero && *v == 0):
				errs = append(errs, fmt.Sprintf("поле %q: значение вне допустимого диапазона", name))
			case *v > 1_000_000:
				errs = append(errs, fmt.Sprintf("поле %q: слишком большое значение (максимум 1 000 000)", name))
			}
		}
		check("max", d.Max, 0, false)
		check("perDealt", d.PerDealt, 0, true)
		check("perTaken", d.PerTaken, 0, true)
		check("perHealed", d.PerHealed, 0, true)
		return errs
	},
}

// ---- heroes: описание героев ----

var (
	heroID    = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)
	hexColor  = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	roles     = map[string]bool{"tank": true, "fighter": true, "archer": true, "mage": true, "healer": true}
	rarities  = map[string]bool{"common": true, "rare": true, "epic": true, "legendary": true}
	meleeRole = map[string]bool{"tank": true, "fighter": true}
)

type heroStats struct {
	MaxHP       *int  `json:"maxHp"`
	Attack      *int  `json:"attack"`
	IntervalMs  *int  `json:"intervalMs"`
	Melee       *bool `json:"melee"`
	AttackRange *int  `json:"attackRange"`
	RunSpeed    *int  `json:"runSpeed"`
}

type heroArt struct {
	Color string `json:"color"`
}

type hero struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Role        string    `json:"role"`
	Rarity      string    `json:"rarity"`
	Faction     string    `json:"faction"`
	EnemyOnly   bool      `json:"enemyOnly"`
	Description string    `json:"description"`
	Art         heroArt   `json:"art"`
	Stats       heroStats `json:"stats"`
}

type heroesData struct {
	Schema int    `json:"schema"`
	Heroes []hero `json:"heroes"`
}

var heroesKind = &Kind{
	Name:        "heroes",
	Title:       "Герои",
	Description: "Описания героев: имя, роль, редкость, оформление и базовые характеристики. Редкость на силу не влияет. Пока не подключено к расчёту боя (подключение — отдельный шаг).",
	Example: json.RawMessage(`{"schema":1,"heroes":[{"id":"guardian","name":"Страж","role":"tank","rarity":"rare","faction":"","enemyOnly":false,` +
		`"description":"Крепкий защитник переднего ряда.","art":{"color":"#5b7fa6"},` +
		`"stats":{"maxHp":4500,"attack":60,"intervalMs":1500,"melee":true,"attackRange":1100,"runSpeed":3000}}]}`),
	Validate: func(data []byte) []string {
		var d heroesData
		if err := strictDecode(data, &d); err != nil {
			return jsonProblem(err)
		}
		var errs []string
		if d.Schema != 1 {
			errs = append(errs, `поле "schema" должно быть равно 1`)
		}
		if len(d.Heroes) == 0 {
			errs = append(errs, "список героев пуст")
		}
		if len(d.Heroes) > 200 {
			errs = append(errs, "слишком много героев (максимум 200)")
		}
		seen := map[string]bool{}
		for i, h := range d.Heroes {
			at := fmt.Sprintf("герой №%d", i+1)
			if h.ID != "" {
				at = fmt.Sprintf("герой %q", h.ID)
			}
			add := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }
			switch {
			case !heroID.MatchString(h.ID):
				add("id должен состоять из латиницы, цифр, «_» и «-» (1–40 символов)")
			case seen[h.ID]:
				add("повторный id")
			}
			seen[h.ID] = true
			if n := utf8.RuneCountInString(strings.TrimSpace(h.Name)); n < 1 || n > 40 {
				add("имя должно быть от 1 до 40 символов")
			}
			if !roles[h.Role] {
				add("неизвестная роль %q (tank, fighter, archer, mage, healer)", h.Role)
			}
			if !rarities[h.Rarity] {
				add("неизвестная редкость %q (common, rare, epic, legendary)", h.Rarity)
			}
			if utf8.RuneCountInString(h.Faction) > 40 {
				add("название фракции длиннее 40 символов")
			}
			if utf8.RuneCountInString(h.Description) > 500 {
				add("описание длиннее 500 символов")
			}
			if !hexColor.MatchString(h.Art.Color) {
				add("цвет art.color должен быть в формате #rrggbb")
			}
			rng := func(name string, v *int, lo, hi int) {
				switch {
				case v == nil:
					add("поле stats.%s обязательно", name)
				case *v < lo || *v > hi:
					add("stats.%s должно быть от %d до %d", name, lo, hi)
				}
			}
			rng("maxHp", h.Stats.MaxHP, 1, 10_000_000)
			rng("attack", h.Stats.Attack, 0, 10_000_000)
			rng("intervalMs", h.Stats.IntervalMs, 100, 60_000)
			rng("attackRange", h.Stats.AttackRange, 100, 50_000)
			rng("runSpeed", h.Stats.RunSpeed, 100, 20_000)
			if h.Stats.Melee == nil {
				add("поле stats.melee обязательно")
			} else if meleeRole[h.Role] && !*h.Stats.Melee {
				add("роль %s — ближний бой: stats.melee должно быть true", h.Role)
			} else if !meleeRole[h.Role] && roles[h.Role] && *h.Stats.Melee {
				add("роль %s — дальний бой: stats.melee должно быть false", h.Role)
			}
		}
		return errs
	},
}
