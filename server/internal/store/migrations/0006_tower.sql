CREATE TABLE tower_progress (
  account_id INTEGER PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
  floor_cleared INTEGER NOT NULL DEFAULT 0 CHECK(floor_cleared >= 0)
);
INSERT INTO tower_progress(account_id, floor_cleared)
SELECT id, 0 FROM accounts
WHERE NOT EXISTS (SELECT 1 FROM tower_progress WHERE tower_progress.account_id = accounts.id);
