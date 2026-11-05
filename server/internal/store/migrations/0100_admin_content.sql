-- Админка и версионированный контент. Аддитивная миграция: прежние версии приложения эти таблицы не используют.
-- Номер 0100 выбран с запасом, чтобы не пересекаться с параллельными миграциями игровых модулей.

-- Сессии владельца (токен хранится только в виде SHA-256).
CREATE TABLE admin_sessions (
  token_hash TEXT PRIMARY KEY,
  csrf       TEXT NOT NULL,
  ip         TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  expires_at TEXT NOT NULL
);
CREATE INDEX admin_sessions_exp ON admin_sessions(expires_at);

-- Опубликованные версии контента. Версия растёт на единицу; откат = новая версия с копией старых данных.
CREATE TABLE content_versions (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  kind             TEXT NOT NULL,
  version          INTEGER NOT NULL,
  data             TEXT NOT NULL,
  note             TEXT NOT NULL DEFAULT '',
  source           TEXT NOT NULL CHECK(source IN ('publish','rollback')),
  rolled_back_from INTEGER,
  created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  UNIQUE(kind, version)
);

-- Черновик: не более одного на вид контента.
CREATE TABLE content_drafts (
  kind         TEXT PRIMARY KEY,
  data         TEXT NOT NULL,
  base_version INTEGER NOT NULL DEFAULT 0,
  updated_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

-- Журнал действий владельца.
CREATE TABLE admin_audit (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')),
  action TEXT NOT NULL,
  kind   TEXT NOT NULL DEFAULT '',
  version INTEGER NOT NULL DEFAULT 0,
  ip     TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT ''
);
