-- Основа серверных прав на скорость. Обучение/VIP наполнят будущие игровые модули;
-- клиент не может менять эти поля. Существующие аккаунты не получают новые права.
CREATE TABLE account_progress (
  account_id INTEGER PRIMARY KEY REFERENCES accounts(id),
  tutorial_completed INTEGER NOT NULL DEFAULT 0 CHECK(tutorial_completed IN (0, 1)),
  vip_level INTEGER NOT NULL DEFAULT 0 CHECK(vip_level >= 0)
);
INSERT INTO account_progress(account_id) SELECT id FROM accounts;

-- Старый бинарник может продолжать INSERT без нового столбца (скорость = 1).
ALTER TABLE battles ADD COLUMN speed INTEGER NOT NULL DEFAULT 1 CHECK(speed IN (1, 2, 4));
