#!/usr/bin/env bash
# Интеграционная проверка загрузчика ресурсов: настоящий сервер + клиент Godot (headless).
# Использование: tools/smoke-assets.sh <путь к godot> <путь к бинарнику сервера>
# Сценарии: обрыв и докачка, повреждение файла, обновление изменённого ресурса, удаление, повреждённая часть, сервер недоступен.
set -euo pipefail
GODOT=$(readlink -f "$1"); SERVER=$(readlink -f "$2")
ROOT=$(cd "$(dirname "$0")/.." && pwd)
W=$(mktemp -d); trap 'kill $SRV 2>/dev/null || true; rm -rf "$W"' EXIT
PORT=18744
mkdir -p "$W/assets/start" "$W/assets/bg" "$W/xdg"
LUNEXGAME_LISTEN=127.0.0.1:$PORT LUNEXGAME_DB="$W/s.db" LUNEXGAME_ASSETS_DIR="$W/assets" "$SERVER" >"$W/server.log" 2>&1 &
SRV=$!
for _ in $(seq 40); do curl -fs "http://127.0.0.1:$PORT/LunexGame/api/health" >/dev/null && break; sleep 0.25; done
export XDG_DATA_HOME="$W/xdg" LUNEXGAME_BASE_URL="http://127.0.0.1:$PORT/LunexGame" LUNEXGAME_SMOKE=1
"$GODOT" --headless --path "$ROOT/client" --import >/dev/null 2>&1 || true

run() { # печатает вывод клиента, возвращает его код
  set +e; timeout 90 "$GODOT" --headless --path "$ROOT/client" >"$W/out.log" 2>&1; RC=$?; set -e
  grep -E "BOOT_|ASSETS_|BATTLE_|TEST_ABORT|SCRIPT ERROR" "$W/out.log" || true
}
expect() { # expect "<шаблон в выводе>" "описание"
  grep -qE "$1" "$W/out.log" || { echo "ПРОВАЛ: $2 (нет /$1/)"; cat "$W/out.log" | tail -20; exit 1; }
  echo "ок: $2"
}
adir() { find "$W/xdg" -type d -name assets -path "*app_userdata*" | head -1; }
verify() { python3 - "$(adir)" "$PORT" <<'PY'
import sys,json,hashlib,urllib.request,os
A,port=sys.argv[1],sys.argv[2]
m=json.load(urllib.request.urlopen(f"http://127.0.0.1:{port}/LunexGame/api/assets/manifest"))
bad=[f["path"] for f in m["files"] if not (os.path.exists(os.path.join(A,f["path"])) and hashlib.sha256(open(os.path.join(A,f["path"]),"rb").read()).hexdigest()==f["sha256"])]
local=sum(len(fs) for _,_,fs in os.walk(A))
print(f"сверка: в манифесте {len(m['files'])}, локально {local}, расхождений {len(bad)}")
sys.exit(1 if bad or local!=len(m['files']) else 0)
PY
}

echo "== 1. обрыв на 3 МБ"
LUNEXGAME_TEST_ABORT_AFTER_BYTES=3145728 run; [ "$RC" = 3 ] || { echo "ПРОВАЛ: код $RC"; exit 1; }
expect "TEST_ABORT part=bg/" "клиент оборван посреди загрузки"
[ -n "$(find "$(adir)" -name '*.part')" ] || { echo "ПРОВАЛ: нет .part"; exit 1; }
echo "== 2. докачка"
run; [ "$RC" = 0 ] || exit 1
expect "ASSETS_OK .*resumed_bytes=3145728" "продолжено с 3 МБ"
verify
echo "== 3. повреждение файла того же размера"
printf 'X' | dd of="$(adir)/start/placeholder-a.bin" bs=1 seek=100 conv=notrunc 2>/dev/null
run; expect "ASSETS_OK .*repaired=0" "быстрая проверка не пересчитывает хэш (известное ограничение)"
LUNEXGAME_VERIFY_FULL=1 run; expect "ASSETS_OK .*repaired=1" "полная проверка нашла и починила"
verify
echo "== 4. изменение ресурса на сервере"
printf 'новый' > "$W/assets/bg/new.bin"; head -c 1000 /dev/urandom > "$W/assets/start/placeholder-b.bin"; sleep 6
run; expect "ASSETS_OK downloaded=2 " "скачаны только 2 изменённых файла"
verify
echo "== 5. удаление на сервере"
rm "$W/assets/bg/new.bin"; sleep 6
run; expect "ASSETS_OK .*pruned=1" "лишний файл удалён локально"
verify
echo "== 6. повреждённая часть при докачке"
rm -rf "$(adir)" "$(dirname "$(adir)")/assets_state.json"
LUNEXGAME_TEST_ABORT_AFTER_BYTES=3145728 run >/dev/null
P=$(find "$(adir)" -name '*.part'); head -c 1000 /dev/urandom | dd of="$P" bs=1 seek=500 conv=notrunc 2>/dev/null
run; expect "ASSETS_OK" "загрузка завершена"
verify
echo "== 6b. бой: вход, ресурсы, расчёт на сервере, проигрывание записи, сверка"
LUNEXGAME_SMOKE_BATTLE=1 run; [ "$RC" = 0 ] || { echo "ПРОВАЛ: код $RC"; tail -20 "$W/out.log"; exit 1; }
expect "BATTLE_LOADED units=10 .*speed=1 allowed_count=1" "бой получен с серверной скоростью x1"
expect "BATTLE_SPEED_UI x1_enabled=true x1_selected=true" "кнопка x1 включена и выбрана (JSON-числа приведены к int)"
expect "BATTLE_OK winner=player .*inconsistencies=0" "запись проиграна, результат согласован с состоянием"
expect "BATTLE_MOVEMENT_OK moves=[1-9][0-9]* inconsistencies=0" "серверные перемещения, дальность атак и конечные координаты согласованы с клиентом"
expect "BATTLE_SKILLS_OK auto1=[1-9][0-9]* auto2=[1-9][0-9]* passives=10 inconsistencies=0" "оба автонавыка и пассивы проиграны без расхождений"
! grep -q "SCRIPT ERROR" "$W/out.log" || { echo "ПРОВАЛ: ошибки скриптов"; grep -B1 -A2 "SCRIPT ERROR" "$W/out.log" | head; exit 1; }
echo "ок: ошибок скриптов нет"
echo "== 7. сервер недоступен"
kill $SRV; wait $SRV 2>/dev/null || true
run; [ "$RC" = 1 ] || { echo "ПРОВАЛ: ожидался код 1, получен $RC"; exit 1; }
expect "BOOT_FAIL" "понятная ошибка вместо падения"
echo "ВСЕ СЦЕНАРИИ ПРОЙДЕНЫ"
