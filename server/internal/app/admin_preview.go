package app

import (
	"encoding/json"
	"fmt"
	"sort"

	"lunexgame/internal/battle"
)

// adminPreview строит предпросмотр черновика для админки. Это единственное место, где админка касается боевого ядра:
// при изменении API боя достаточно обновить эту функцию.
func adminPreview(kind string, draft, published json.RawMessage) (map[string]any, error) {
	switch kind {
	case "energy":
		cur, err := runEnergyDemo(draft)
		if err != nil {
			return nil, err
		}
		sections := []map[string]any{{"title": "Демо-бой с черновиком", "rows": cur}}
		if len(published) > 0 {
			if old, err := runEnergyDemo(published); err == nil {
				sections = append(sections, map[string]any{"title": "Тот же бой с опубликованными значениями", "rows": old})
			}
		}
		sections = append(sections, map[string]any{"title": "Примечание", "rows": [][]string{{"Демо", "Временные отряды прототипа (battle.PrototypeSquads); это проверка влияния, а не финальный баланс."}}})
		return map[string]any{"sections": sections}, nil
	case "heroes":
		var d struct {
			Heroes []struct {
				ID, Name, Role, Rarity string
				EnemyOnly              bool
			} `json:"heroes"`
		}
		if err := json.Unmarshal(draft, &d); err != nil {
			return nil, err
		}
		roles, rarities := map[string]int{}, map[string]int{}
		player, enemy := 0, 0
		for _, h := range d.Heroes {
			roles[h.Role]++
			rarities[h.Rarity]++
			if h.EnemyOnly {
				enemy++
			} else {
				player++
			}
		}
		fmtMap := func(m map[string]int) string {
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			s := ""
			for _, k := range keys {
				s += fmt.Sprintf("%s: %d; ", k, m[k])
			}
			return s
		}
		return map[string]any{"sections": []map[string]any{{"title": "Состав", "rows": [][]string{
			{"Всего героев", fmt.Sprint(len(d.Heroes))},
			{"Игровых", fmt.Sprintf("%d (по ТЗ на старте: 5)", player)},
			{"Только вражеских", fmt.Sprintf("%d (по ТЗ на старте: 2)", enemy)},
			{"По ролям", fmtMap(roles)},
			{"По редкости", fmtMap(rarities)},
		}}, {"title": "Примечание", "rows": [][]string{{"Подключение", "Эти данные пока не используются в расчёте боя — подключение выполняется отдельным шагом."}}}}}, nil
	}
	return nil, fmt.Errorf("для вида %q предпросмотр не предусмотрен", kind)
}

func runEnergyDemo(data json.RawMessage) ([][]string, error) {
	var d struct{ Max, PerDealt, PerTaken, PerHealed float64 }
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, err
	}
	out, err := battle.Run(battle.PrototypeSquads(), battle.Energy{Max: d.Max, PerDealt: d.PerDealt, PerTaken: d.PerTaken, PerHealed: d.PerHealed})
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, e := range out.Events {
		counts[e.Type]++
	}
	winner := map[string]string{"player": "игрок", "enemy": "враг"}[out.Result.Winner]
	reason := map[string]string{"elimination": "уничтожение отряда", "timeout": "вышло время", "draw": "взаимное уничтожение"}[out.Result.Reason]
	if reason == "" {
		reason = out.Result.Reason
	}
	return [][]string{
		{"Победитель", winner + " (" + reason + ")"},
		{"Длительность боя", fmt.Sprintf("%.1f с", float64(out.Result.DurationMs)/1000)},
		{"Главных способностей", fmt.Sprint(counts["skill"])},
		{"Автонавыков", fmt.Sprint(counts["auto_skill"])},
		{"Обычных атак", fmt.Sprint(counts["attack"])},
		{"Всего событий", fmt.Sprint(len(out.Events))},
	}, nil
}
