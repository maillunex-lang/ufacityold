#!/usr/bin/env bash
# Отключает вход по паролю в SSH (остаётся вход по ключам). Выполняется владельцем под root.
#
# ВАЖНО (у владельца нет запасного входа в консоль): скрипт
#  1) отказывается работать, если в /root/.ssh/authorized_keys нет ключей;
#  2) требует подтверждения, что вход по ключу уже проверен вторым окном;
#  3) пишет изменение в ОТДЕЛЬНЫЙ файл, проверяет конфигурацию (sshd -t);
#  4) ставит таймер АВТООТКАТА на 10 минут: если вы не подтвердите, что новый вход работает, изменение само отменится;
#  5) применяет через reload (существующие сессии не обрываются).
# Подтверждение:  systemctl stop lunexgame-ssh-revert.timer
set -euo pipefail

AK="${AK:-/root/.ssh/authorized_keys}"
SSHD_DIR="${SSHD_DIR:-/etc/ssh/sshd_config.d}"
SSHD_MAIN="${SSHD_MAIN:-/etc/ssh/sshd_config}"
D="$SSHD_DIR/00-lunexgame-hardening.conf"   # 00-: у sshd действует ПЕРВОЕ значение, наш файл должен идти первым

[[ $EUID -eq 0 || -n "${LG_TEST:-}" ]] || { echo "нужен root"; exit 1; }
command -v sshd >/dev/null && command -v systemd-run >/dev/null || { echo "нужны sshd и systemd-run"; exit 1; }

# 1) ключи
n=0
[[ -f "$AK" ]] && n=$(grep -cE '^(ssh-(ed25519|rsa)|ecdsa-sha2-[a-z0-9]+|sk-(ssh-ed25519|ecdsa-sha2-nistp256)@openssh\.com) ' "$AK" || true)
if [[ "$n" -lt 1 ]]; then
  echo "ОТКАЗ: в $AK нет ни одного ключа. Сначала добавьте публичный ключ и проверьте вход по нему."
  exit 1
fi
echo "Ключей в authorized_keys: $n"
if [[ "$n" -lt 2 ]]; then
  echo "ВНИМАНИЕ: ключ один. Потеря этого устройства/ключа = потеря доступа к серверу (запасного входа нет)."
  echo "Рекомендуется добавить второй ключ (резервный) ДО отключения пароля."
fi

# 2) подтверждение проверки
echo "Прежде чем продолжить, должно быть выполнено: вы открыли ВТОРОЕ окно Termius и вошли по ключу БЕЗ пароля."
read -r -p "Вход по ключу проверен? Напишите yes: " ans
[[ "$ans" == "yes" ]] || { echo "Отмена: ничего не изменено."; exit 1; }

# что сейчас действует (для истории)
echo "== сейчас =="; sshd -T 2>/dev/null | grep -E '^(passwordauthentication|permitrootlogin|kbdinteractiveauthentication) ' || true

# 3) изменение в отдельном файле + проверка
BK=/root/sshd-backup-$(date +%F-%H%M); mkdir -p "$BK"; cp -a "$SSHD_MAIN" "$SSHD_DIR" "$BK/" 2>/dev/null || true
echo "Копия настроек: $BK"
cat > "$D" <<CONF
# Создано harden-ssh-keys.sh (проект LunexGame). Откат: rm $D && systemctl reload ssh
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin prohibit-password
PubkeyAuthentication yes
CONF
if ! sshd -t; then
  echo "Проверка sshd -t НЕ прошла — откат, ничего не применено."; rm -f "$D"; exit 1
fi
eff=$(sshd -T | grep -E '^passwordauthentication ' || true)
if [[ "$eff" != "passwordauthentication no" ]]; then
  echo "Эффективная настройка осталась: '$eff' (другой файл перекрывает наш). Откат, ничего не применено."
  rm -f "$D"; exit 1
fi

# 4) автооткат через 10 минут
systemctl stop lunexgame-ssh-revert.timer lunexgame-ssh-revert.service 2>/dev/null || true
systemd-run --unit=lunexgame-ssh-revert --on-active=10m /bin/sh -c "rm -f '$D' && systemctl reload ssh" >/dev/null
echo "Таймер автоотката поставлен на 10 минут."

# 5) применить
systemctl reload ssh
sleep 1
echo "== теперь =="; sshd -T | grep -E '^(passwordauthentication|permitrootlogin|kbdinteractiveauthentication) '
cat <<MSG

ГОТОВО, но изменение само отменится через 10 минут, если вы его не подтвердите.
Сейчас, НЕ закрывая это окно:
  1) откройте НОВОЕ окно Termius и войдите по ключу — должно получиться;
  2) (по желанию) попробуйте войти по паролю — должно быть отказано;
  3) если всё так, подтвердите:   systemctl stop lunexgame-ssh-revert.timer
Если что-то не так — просто подождите 10 минут: пароль вернётся сам. Или откат сразу:
  rm $D && systemctl reload ssh
MSG
