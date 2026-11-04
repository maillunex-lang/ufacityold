#!/usr/bin/env bash
# Однократная установка на VPS (запускать человеком под root после ревью). НЕ запускалась автоматически.
# Не трогает чужие сервисы; nginx только проверяется (nginx -t) и перезагружается (reload).
# Ничего не делает с nginx сам: печатает, что добавить, чтобы владелец применил include осознанно.
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "нужен root"; exit 1; }
HERE=$(cd "$(dirname "$0")" && pwd)
PORT=${1:-}
[[ "$PORT" =~ ^[0-9]+$ ]] || { echo "usage: install.sh <порт>   (порт из обследования, свободный)"; exit 1; }
if ss -Hltun | awk '{print $5}' | grep -qE "[:.]$PORT\$"; then echo "порт $PORT занят"; exit 1; fi

id lunexgame &>/dev/null || useradd --system --home /opt/lunexgame --shell /usr/sbin/nologin lunexgame
id lunexgame-deploy &>/dev/null || useradd --system --home /opt/lunexgame --shell /bin/bash lunexgame-deploy
install -d -o lunexgame-deploy -g lunexgame -m 0750 /opt/lunexgame /opt/lunexgame/releases
install -d -o root -g root -m 0755 /opt/lunexgame/bin /opt/lunexgame/etc
install -d -o lunexgame -g lunexgame -m 0750 /opt/lunexgame/data /opt/lunexgame/assets /opt/lunexgame/backups
install -m 0755 -o root -g root "$HERE/lunexgame-deploy" /opt/lunexgame/bin/lunexgame-deploy
echo "$PORT" >/opt/lunexgame/etc/port
[[ -e /opt/lunexgame/etc/lunexgame.env ]] || printf 'LUNEXGAME_LISTEN=127.0.0.1:%s\nLUNEXGAME_ASSETS_DIR=/opt/lunexgame/assets\n' "$PORT" >/opt/lunexgame/etc/lunexgame.env
chmod 0640 /opt/lunexgame/etc/lunexgame.env; chown root:lunexgame /opt/lunexgame/etc/lunexgame.env
install -m 0644 "$HERE/lunexgame.service" /etc/systemd/system/lunexgame.service
printf 'lunexgame-deploy ALL=(root) NOPASSWD: /usr/bin/systemctl restart lunexgame.service\n' >/etc/sudoers.d/lunexgame-deploy
chmod 0440 /etc/sudoers.d/lunexgame-deploy; visudo -cf /etc/sudoers.d/lunexgame-deploy
systemctl daemon-reload; systemctl enable lunexgame.service
sed "s/__PORT__/$PORT/" "$HERE/lunexgame.nginx.conf" >/etc/nginx/snippets/lunexgame.conf
cat <<MSG
Готово. Дальше вручную:
 1) в http{} nginx (если нет): map \$http_upgrade \$connection_upgrade_lunexgame { default upgrade; '' close; }
 2) в server{} tts-ufa.ru добавить:  include /etc/nginx/snippets/lunexgame.conf;   (сначала бэкап конфигурации сайта)
 3) nginx -t && systemctl reload nginx
 4) в ~lunexgame-deploy/.ssh/authorized_keys:
    command="/opt/lunexgame/bin/lunexgame-deploy",restrict ssh-ed25519 <ПУБЛИЧНЫЙ ключ доставки> lunexgame-deploy
MSG
