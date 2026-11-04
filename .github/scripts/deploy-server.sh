#!/usr/bin/env bash
# Выполняется только раннером GitHub Actions: ключ доставки остаётся на раннере.
# Не запускать повторно автоматически: после разрыва SSH результат на сервере может быть неизвестен.
set -euo pipefail

: "${GITHUB_SHA:?не задан коммит}"
: "${HOST:?не задан адрес доставки}"
: "${USER_:?не задан пользователь доставки}"
: "${PUBLIC_URL:?не задан публичный адрес}"
SUM=$(cat build/lunexgame.sha256)
[[ "$GITHUB_SHA" =~ ^[0-9a-f]{40}$ && "$SUM" =~ ^[0-9a-f]{64}$ ]] || { echo "неверный SHA коммита или бинарника" >&2; exit 2; }
[[ -s build/lunexgame ]] || { echo "нет бинарника для доставки" >&2; exit 2; }

echo "Этап 1/2: SSH-подключение, передача бинарника и серверная проверка (лимит 300 с)"
if timeout --signal=TERM --kill-after=15s 300s \
  ssh -T -i ~/.ssh/id_deploy \
    -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes -o BatchMode=yes \
    -o ConnectTimeout=20 -o ConnectionAttempts=1 \
    -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    "$USER_@$HOST" "deploy $GITHUB_SHA $SUM" < build/lunexgame; then
  echo "SSH-доставка и серверная проверка завершены"
else
  rc=$?
  if [[ "$rc" == 124 || "$rc" == 137 ]]; then
    echo "SSH-доставка превысила лимит времени (код $rc)" >&2
  else
    echo "SSH-доставка завершилась с ошибкой (код $rc)" >&2
  fi
  echo "Результат доставки не подтверждён. Перед повтором проверить публичные version/health; автоматического повтора нет." >&2
  exit "$rc"
fi

echo "Этап 2/2: публичная проверка коммита и health"
for i in $(seq 10); do
  echo "Публичная проверка: попытка $i/10"
  v=$(curl -fsS --connect-timeout 5 --max-time 10 "$PUBLIC_URL/api/version" || true)
  if grep -Fq "\"commit\":\"$GITHUB_SHA\"" <<<"$v" && \
     curl -fsS --connect-timeout 5 --max-time 10 "$PUBLIC_URL/api/health" >/dev/null; then
    echo "Публичные version/health подтверждены для $GITHUB_SHA"
    exit 0
  fi
  if [[ "$i" != 10 ]]; then sleep 3; fi
done
echo "Публичные version/health не подтвердили релиз. Не считать деплой успешным; это не подтверждение автоматического отката." >&2
exit 1
