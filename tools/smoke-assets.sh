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
echo "== 6c. живой бой: подключение, тик, команда, переподключение, результат"
python3 - "$PORT" << 'PY'
import base64, json, os, socket, struct, sys, time, urllib.request
port = int(sys.argv[1])

def guest():
    req = urllib.request.Request(f"http://127.0.0.1:{port}/LunexGame/api/auth/guest", data=b"", method="POST")
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.load(r)["token"]

def connect(token):
    s = socket.create_connection(("127.0.0.1", port), 5)
    key = base64.b64encode(os.urandom(16)).decode()
    req = (
        f"GET /LunexGame/api/battle/live HTTP/1.1\r\n"
        f"Host: 127.0.0.1:{port}\r\n"
        "Upgrade: websocket\r\nConnection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n"
        f"Authorization: Bearer {token}\r\n\r\n"
    )
    s.sendall(req.encode())
    buf = b""
    while b"\r\n\r\n" not in buf:
        chunk = s.recv(4096)
        if not chunk:
            raise SystemExit("рукопожатие оборвано")
        buf += chunk
    head, rest = buf.split(b"\r\n\r\n", 1)
    if b" 101 " not in head.split(b"\r\n", 1)[0]:
        raise SystemExit("нет 101: " + head[:180].decode("latin1", "replace"))
    s.settimeout(10)
    return s, bytearray(rest)

def send_text(s, obj):
    data = json.dumps(obj, separators=(",", ":")).encode()
    mask = os.urandom(4)
    n = len(data)
    hdr = bytearray([0x81])
    if n < 126:
        hdr.append(0x80 | n)
    else:
        hdr.append(0x80 | 126)
        hdr += struct.pack("!H", n)
    hdr += mask
    hdr += bytes(b ^ mask[i % 4] for i, b in enumerate(data))
    s.sendall(hdr)

def read_msg(s, buf):
    def fill(n):
        while len(buf) < n:
            chunk = s.recv(65536)
            if not chunk:
                return False
            buf.extend(chunk)
        return True
    while True:
        if not fill(2):
            return None
        ln = buf[1] & 0x7F
        i = 2
        if ln == 126:
            if not fill(4):
                return None
            ln = struct.unpack("!H", buf[2:4])[0]
            i = 4
        elif ln == 127:
            if not fill(10):
                return None
            ln = struct.unpack("!Q", buf[2:10])[0]
            i = 10
        if not fill(i + ln):
            return None
        payload = bytes(buf[i:i + ln])
        op = buf[0] & 0x0F
        del buf[:i + ln]
        if op == 0x9:
            mask = os.urandom(4)
            frame = bytearray([0x8A, 0x80 | len(payload)]) + mask
            frame += bytes(b ^ mask[k % 4] for k, b in enumerate(payload))
            s.sendall(frame)
            continue
        if op == 0x8:
            return None
        if op == 0x1:
            return json.loads(payload.decode())
        if op in (0x0, 0xA):
            continue
        return None

token = guest()
sock, buf = connect(token)
hello = read_msg(sock, buf)
if not hello or hello.get("type") != "hello":
    raise SystemExit(f"нет hello: {hello}")
send_text(sock, {"type": "start"})
roster = None
ticks = 0
command = None
deadline = time.time() + 8
while time.time() < deadline and (command is None or ticks < 1):
    msg = read_msg(sock, buf)
    if not msg:
        raise SystemExit("сокет закрыт до команды")
    if msg.get("type") == "state" and roster is None:
        roster = msg.get("roster") or []
        if len(roster) != 10 or "role" not in roster[0]:
            raise SystemExit(f"состав не пригоден для сцены: {len(roster) if isinstance(roster, list) else roster}")
        player = next(u["id"] for u in roster if u.get("side") == "player")
        send_text(sock, {"type": "main_skill", "unitId": player})
    elif msg.get("type") == "tick":
        ticks += 1
    elif msg.get("type") == "command":
        command = msg.get("accepted")
if roster is None or ticks < 1 or command is None:
    raise SystemExit(f"протокол: roster={roster is not None} ticks={ticks} command={command}")
sock.close()
sock, buf = connect(token)
again = read_msg(sock, buf)
if not again or again.get("type") != "state" or again.get("resumed") is not True or len(again.get("roster") or []) != 10:
    raise SystemExit(f"переподключение не вернуло снимок: {None if not again else again.get('type')}")
done = None
deadline = time.time() + 45
while time.time() < deadline and done is None:
    msg = read_msg(sock, buf)
    if not msg:
        raise SystemExit("обрыв до результата")
    if msg.get("type") == "tick":
        ticks += 1
    if msg.get("done") is True or (isinstance(msg.get("state"), dict) and msg["state"].get("result")):
        done = msg
if not done:
    raise SystemExit("нет результата живого боя")
winner = (done.get("state") or {}).get("result", {}).get("winner")
print(f"LIVE_PROTOCOL_OK ticks={ticks} command_accepted={str(command).lower()} resumed=true winner={winner}")
sock.close()
PY
echo "ок: живой протокол — подключение, тик, команда, переподключение, результат"

echo "== 6b. бой: вход, ресурсы, живой WebSocket, сверка"
LUNEXGAME_SMOKE_BATTLE=1 run; [ "$RC" = 0 ] || { echo "ПРОВАЛ: код $RC"; tail -20 "$W/out.log"; exit 1; }
expect "BATTLE_LOADED units=10 .*speed=1 allowed_count=1" "бой получен с серверной скоростью x1"
expect "BATTLE_SPEED_UI x1_enabled=true x1_selected=true" "кнопка x1 включена и выбрана (JSON-числа приведены к int)"
expect "BATTLE_AUTO_UI enabled=true" "переключатель полного автомата включён по умолчанию"
expect "BATTLE_LIVE_CONNECTED" "клиент открыл живой бой"
expect "BATTLE_LIVE_TICK" "клиент получил тик"
expect "BATTLE_LIVE_COMMAND accepted=(true|false)" "команда главной способности ушла в видимый бой"
expect "BATTLE_LIVE_RESUME resumed=true" "после обрыва клиент показал снимок"
expect "BATTLE_LINK_LOST" "клиент показал, что связь прервана и бой продолжается"
expect "BATTLE_LINK_BACK auto=true t=[0-9]+" "после возвращения клиент показал снимок и прежний автомат"
expect "BATTLE_OK winner=player .*inconsistencies=0" "живой бой доигран, результат согласован с состоянием"
expect "BATTLE_MOVEMENT_OK moves=[1-9][0-9]* inconsistencies=0" "серверные перемещения, дальность атак и конечные координаты согласованы с клиентом"
expect "BATTLE_SKILLS_OK auto1=[1-9][0-9]* auto2=[1-9][0-9]* passives=10 inconsistencies=0" "оба автонавыка и пассивы проиграны без расхождений"
expect "BATTLE_VISUAL_OK gap=1\.[2-9][0-9]* float_n=[3-9][0-9]* float_spread=(0\.[5-9][0-9]*|[1-9][0-9]*\.[0-9]+)" "модели не слипаются в центре, числа залпа разнесены"
! grep -q "SCRIPT ERROR" "$W/out.log" || { echo "ПРОВАЛ: ошибки скриптов"; grep -B1 -A2 "SCRIPT ERROR" "$W/out.log" | head; exit 1; }
echo "ок: ошибок скриптов нет"
echo "== 6d. новый бой после завершения предыдущего"
LUNEXGAME_SMOKE_BATTLE=1 LUNEXGAME_SMOKE_RESTART=1 run; [ "$RC" = 0 ] || { echo "ПРОВАЛ: повторный бой, код $RC"; tail -30 "$W/out.log"; exit 1; }
expect "BATTLE_RESTART_REQUESTED" "клиент запросил новый бой после результата"
expect "BATTLE_RESTART_OK" "клиент начал новый бой без старого результата"
! grep -q "SCRIPT ERROR" "$W/out.log" || { echo "ПРОВАЛ: ошибки скриптов при повторном бое"; grep -B1 -A2 "SCRIPT ERROR" "$W/out.log" | head; exit 1; }
echo "ок: повторный бой начат чисто"
echo "== 6c. город и кампания"
LUNEXGAME_SMOKE_CITY=1 LUNEXGAME_CITY_DEMO=1 run; [ "$RC" = 0 ] || { echo "ПРОВАЛ: город, код $RC"; exit 1; }
expect "CITY_OK gold=0 heroes=2 cleared=0" "город загрузил профиль"
expect "CAMPAIGN_OK stages=10 open=1" "список кампании загружен"
echo "== 7. сервер недоступен"
kill $SRV; wait $SRV 2>/dev/null || true
run; [ "$RC" = 1 ] || { echo "ПРОВАЛ: ожидался код 1, получен $RC"; exit 1; }
expect "BOOT_FAIL" "понятная ошибка вместо падения"
echo "ВСЕ СЦЕНАРИИ ПРОЙДЕНЫ"
