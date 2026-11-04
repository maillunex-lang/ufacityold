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

## Ограничение зависшего CI-запуска (2026-11-04)
После слияния PR #1 доставка коммита `83d9c14` осталась в шаге SSH/публичной проверки более 10 минут. На момент диагностики публичные `/api/version` и `/api/health` отвечают 200, версия — прежняя `f1f8168`, БД — schemaVersion 2. Причина зависания пока не установлена: в старом шаге нет отметок этапов и таймаутов SSH/внешнего curl; это не доказательство сетевого сбоя или отката.

Исправление в `.github/scripts/deploy-server.sh` сохраняет существующий протокол доставки, forced command, права, host key и ключи. SSH: подключение 20 с, одна попытка, keepalive 15 с / 3 пропуска, общий лимит 300 с с завершением оставшегося процесса через 15 с. HTTP: подключение 5 с, весь запрос 10 с, максимум 10 проверок пары version/health. Шаг ограничен 10 минутами, job — 12. Логи отмечают начало/конец SSH и номер публичной проверки; секреты и окружение не печатаются. SSH не повторяется автоматически, код ошибки сохраняется. Таймаут/отмена не гарантирует откат — результат проверять отдельно.

Порядок восстановления:
1. В GitHub отменить зависший старый run кнопкой **Cancel workflow**. Не выполнять SSH-команды из рабочей среды агента.
2. Проверить публичные version/health после отмены. Если сервер недоступен, действия на VPS выполняет только владелец по отдельной инструкции после диагностики.
3. После проверок слить PR с исправлением. Новый push в main запустит доставку с таймаутами. **Re-run старого run использует старый workflow**, поэтому для применения исправления не подходит.
4. Дождаться успешного deploy и совпадения публичного commit с новым main. Если SSH завершится ошибкой/таймаутом, анализировать конкретное сообщение; не расширять права доставки и не отключать проверку host key.

Локальные проверки доставки без сети/VPS/секретов: `python3 -m unittest discover -s tools/tests -p 'test_deploy_server.py' -v`. Подмены SSH/curl проверяют успешную передачу stdin, зависание с реальным завершением процесса, ошибку SSH, старый публичный commit, недоступный HTTP и провал health. Реальная доставка с новыми таймаутами проверяется только в GitHub Actions после слияния.
