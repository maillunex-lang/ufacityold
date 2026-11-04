# Размещение, автодеплой, откат
**Статус: файлы подготовлены, на VPS ничего не применялось.**

## Целевая раскладка (после обследования порт может измениться)
```
/opt/lunexgame/releases/<sha>/lunexgame   релизы;  current -> релиз
/opt/lunexgame/{data,assets,backups}      данные (не трогаются деплоем)
/opt/lunexgame/etc/{port,lunexgame.env}   конфигурация (секреты только здесь, 0640)
/opt/lunexgame/bin/lunexgame-deploy       принимающий скрипт (root:root)
/etc/systemd/system/lunexgame.service     служба, пользователь lunexgame
/etc/nginx/snippets/lunexgame.conf        include в server{} tts-ufa.ru
```
Порт по умолчанию 18741 — **не проверен** на VPS.

## Порядок первой установки (человек, root, после ревью)
1. Обследование на чтение: `uname -a; nproc; free -h; df -h; ss -tulpn; systemctl list-units --type=service --state=running; nginx -T` (без вывода секретов), проверить, что `/LunexGame/` не занят.
2. Резервная копия: `tar czf /root/nginx-backup-$(date +%F).tgz /etc/nginx`.
3. `deploy/server/install.sh <свободный_порт>`; выполнить напечатанные шаги; `nginx -t && systemctl reload nginx`.
4. Создать ключ доставки на своей машине (`ssh-keygen -t ed25519`), публичный — в authorized_keys с forced command (см. install.sh); приватный — в секрет `DEPLOY_SSH_KEY`.
5. `ssh-keyscan -t ed25519 144.31.254.99`, **сверить отпечаток с самим сервером** (`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`), строку — в секрет `DEPLOY_KNOWN_HOSTS`.
6. Секреты: `DEPLOY_SSH_KEY`, `DEPLOY_KNOWN_HOSTS`, `DEPLOY_HOST`, `DEPLOY_USER` (=lunexgame-deploy) в environment `production`; переменные `DEPLOY_ENABLED=true`, при необходимости `DEPLOY_GOARCH` (arm64 для ARM).

## Автодеплой
Push в `main` → test (gofmt/vet/test -race/build) → deploy (только если test успешен): бинарник по SSH на stdin, `flock` сериализует (плюс `concurrency` в Actions), SHA-256 и 40-hex commit проверяются, `current` переключается атомарно, служба перезапускается, проверяется health и совпадение commit в `/api/version`; при неуспехе `current` возвращается на предыдущий релиз. Ошибки теста/доставки до переключения не затрагивают работающую версию. PR только проверяются, без секретов.

## Откат вручную
```
ln -sfn /opt/lunexgame/releases/<предыдущий_sha> /opt/lunexgame/current.new && mv -Tf /opt/lunexgame/current.new /opt/lunexgame/current
systemctl restart lunexgame.service
```
Полное удаление: `systemctl disable --now lunexgame`, убрать include из nginx (из бэкапа), `nginx -t && reload`, удалить `/opt/lunexgame` и пользователей `lunexgame*`.

## Резервное копирование и БД
Перед миграцией существующей БД сервер сам делает копию (`VACUUM INTO`) в `/opt/lunexgame/data/backups/` (реализовано, покрыто тестом). Копии не публиковать; ротация пока не реализована. Откат кода ≠ откат БД. Резервные копии не публиковать.

## Ограничения
Не проверено: работа скриптов на реальном VPS, Actions-запуск, host key, поведение рядом с другими проектами.


## Медленная передача и зависшая доставка (разбор 2026-11-04)
- **Что случилось:** шаг «Доставка» шёл 16+ минут; на VPS был виден растущий файл `/opt/lunexgame/releases/.incoming.*` (9,8 МБ за ~15 минут, ≈10 КБ/с, время изменения совпадало с текущим). Служба, скрипт приёма, диск и память были в порядке; работающая версия не затрагивалась. Причина — крайне низкая скорость сети между раннером GitHub и VPS в тот момент (источник замедления не установлен).
- **Что сделано в конвейере:** лимит задачи 20 минут; `ssh` со сжатием, `ConnectTimeout=30`, «пульсом» и лимитом 300 с; одна повторная попытка при таймауте/обрыве (коды 124/255), без повтора при коде 5 (откат сервером). В скрипте приёма: удаление недозагруженных `.incoming.*` старше 30 минут и `timeout 90` вокруг перезапуска службы.
- **Действие владельца (однократно, после завершения текущей доставки):** обновить установленный скрипт приёма:
  ```
  curl -fsSL https://raw.githubusercontent.com/maillunex-lang/ufacity/main/deploy/server/lunexgame-deploy -o /root/lunexgame-install/lunexgame-deploy && cat /root/lunexgame-install/lunexgame-deploy | head -40
  install -m 0755 -o root -g root /root/lunexgame-install/lunexgame-deploy /opt/lunexgame/bin/lunexgame-deploy
  ```
  Прежний скрипт остаётся рабочим; обновление необязательно, но желательно.
- **Не измерялось:** причина замедления; сколько займёт доставка при нормальной сети после включения сжатия (ожидаемо меньше, но число неизвестно).
