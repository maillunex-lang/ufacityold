#!/usr/bin/env bash
# Включает бан по IP за подбор паролей SSH через fail2ban (он уже установлен; скрипт ничего не устанавливает).
# Выполняется владельцем под root. Меняет ТОЛЬКО свой файл /etc/fail2ban/jail.d/lunexgame-sshd.local
# и только для jail sshd; другие jail и настройки fail2ban не затрагиваются. Перед изменением делается копия.
#
# Использование:  bash harden-ssh-ban.sh <ваш_IP> [ещё_IP_или_подсеть ...]
# Ваши адреса попадут в список «никогда не банить» (иначе ошибка при вводе пароля может заблокировать вас самих).
set -euo pipefail
[[ $EUID -eq 0 ]] || { echo "нужен root"; exit 1; }
command -v fail2ban-client >/dev/null || { echo "fail2ban не установлен; скрипт ничего не устанавливает"; exit 1; }
[[ $# -ge 1 ]] || { echo "usage: $0 <ваш_IP> [ещё_IP ...]"; exit 1; }
for a in "$@"; do [[ "$a" =~ ^[0-9a-fA-F:./]+$ ]] || { echo "некорректный адрес: $a"; exit 1; }; done

F=/etc/fail2ban/jail.d/lunexgame-sshd.local
BK=/root/fail2ban-backup-$(date +%F-%H%M)
mkdir -p "$BK"; cp -a /etc/fail2ban/jail.d "$BK/"; [[ -f /etc/fail2ban/jail.local ]] && cp -a /etc/fail2ban/jail.local "$BK/" || true
echo "Копия настроек: $BK"
echo "== до изменений =="; fail2ban-client status sshd 2>&1 | sed -n 1,10p || true

cat > "$F.new" <<CONF
# Создано harden-ssh-ban.sh (проект LunexGame). Удалить файл и выполнить «fail2ban-client reload» — откат.
[sshd]
enabled = true
mode = normal
# 3 неудачи за час — бан; повторные нарушители банятся всё дольше (1 сутки, 2, 4, ... до 4 недель)
maxretry = 3
findtime = 1h
bantime = 1d
bantime.increment = true
bantime.factor = 2
bantime.maxtime = 4w
ignoreip = 127.0.0.1/8 ::1 $*
CONF
PREV=""; [[ -f "$F" ]] && { PREV="$F.prev"; cp -a "$F" "$PREV"; }
mv "$F.new" "$F"
if ! fail2ban-client -t >/dev/null 2>&1; then
  echo "Проверка конфигурации fail2ban НЕ прошла — откат."; fail2ban-client -t 2>&1 | tail -5
  if [[ -n "$PREV" ]]; then mv "$PREV" "$F"; else rm -f "$F"; fi
  exit 1
fi
fail2ban-client reload
sleep 3
echo "== после изменений =="
fail2ban-client status sshd
echo "Параметры: maxretry=$(fail2ban-client get sshd maxretry) findtime=$(fail2ban-client get sshd findtime) bantime=$(fail2ban-client get sshd bantime)"
echo "Не банятся: $(fail2ban-client get sshd ignoreip | tr '\n' ' ')"
echo
echo "Готово. Откат:  rm $F && fail2ban-client reload"
echo "Разбанить адрес: fail2ban-client set sshd unbanip <IP>"
