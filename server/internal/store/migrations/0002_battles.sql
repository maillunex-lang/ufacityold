-- Записанные бои (прототип). Аддитивная миграция: старая версия приложения её не использует.
CREATE TABLE battles (
  id              TEXT PRIMARY KEY,
  account_id      INTEGER NOT NULL REFERENCES accounts(id),
  balance_version TEXT NOT NULL,
  winner          TEXT NOT NULL,
  outcome_json    TEXT NOT NULL,
  created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX battles_account ON battles(account_id, created_at);
