# LunexGame — «Самрая: Пробуждение легенд»

Онлайн RPG с коллекцией героев (Android-клиент, собственный сервер, веб-админка).
Адрес: https://tts-ufa.ru/LunexGame/ (на VPS пока **не развернуто**, см. docs/PROGRESS.md).

## Устройство (этап 0)
- `server/` — Go-сервер (только стандартная библиотека), все маршруты под `/LunexGame/`:
  `/` страница состояния, `/api/health`, `/api/version`, `/assets/…` (раздача ресурсов), `/admin/` закрыта (404).
- `deploy/server/` — установка на VPS: systemd-служба, фрагмент nginx, forced-command скрипт доставки.
- `.github/workflows/ci-deploy.yml` — проверки, сборка, доставка по SHA, откат.
- `docs/` — ROADMAP, DECISIONS, DEPLOYMENT, PROGRESS, ASSETS.

## Локальный запуск
```
cd server && go test ./... && go run ./cmd/lunexgame   # слушает 127.0.0.1:18741
curl http://127.0.0.1:18741/LunexGame/api/health
```
Конфигурация — переменные окружения, см. `.env.example`. Секреты в репозиторий не добавлять.
