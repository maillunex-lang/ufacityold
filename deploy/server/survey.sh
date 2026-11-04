#!/usr/bin/env bash
# Обследование VPS ТОЛЬКО НА ЧТЕНИЕ. Ничего не меняет, секреты не печатает.
# Запуск: sudo bash survey.sh > survey.txt   (проверьте файл и пришлите содержимое)
set -u
h(){ printf '\n=== %s ===\n' "$1"; }
h OS; . /etc/os-release; echo "$PRETTY_NAME"; uname -mr
h CPU/RAM/DISK; nproc; free -h | sed -n 1,2p; df -hT / /opt /var 2>/dev/null
h LISTEN; ss -Htulpn 2>/dev/null | awk '{print $1,$5,$7}'
h SERVICES; systemctl list-units --type=service --state=running --no-legend 2>/dev/null | awk '{print $1}'
h DOCKER; command -v docker && docker ps --format '{{.Names}}\t{{.Image}}\t{{.Ports}}' 2>&1 | head -30
h TOOLS; for c in nginx apache2 caddy psql mysql sqlite3 go node python3 curl flock rsync restic borg; do command -v $c >/dev/null && echo "$c: $($c --version 2>&1 | head -1)"; done
h NGINX-SITES; ls /etc/nginx/sites-enabled /etc/nginx/conf.d 2>&1
h NGINX-tts-ufa; nginx -T 2>/dev/null | grep -vEi 'password|secret|token|key\b|ssl_certificate_key|auth_basic_user' | awk '/server_name.*tts-ufa/{f=1} f' | head -150
h LunexGame-USED?; nginx -T 2>/dev/null | grep -in 'lunexgame' || echo "не используется в nginx"; ls -d /opt/lunexgame /var/www/*/LunexGame 2>&1 | head
h CERT; ls /etc/letsencrypt/live 2>&1
h BACKUP/CRON; ls /etc/cron.d 2>&1; crontab -l 2>&1 | grep -vi pass | head
h USERS; getent passwd lunexgame lunexgame-deploy || echo "lunexgame* нет"
h SSH; sshd -T 2>/dev/null | grep -Ei '^(port|passwordauthentication|permitrootlogin|pubkeyauthentication) '
h FIREWALL; (ufw status 2>&1 | head -15) ; echo "443 reachable via ss above"
