ALTER TABLE account_progress ADD COLUMN gold INTEGER NOT NULL DEFAULT 0 CHECK(gold >= 0);
ALTER TABLE account_progress ADD COLUMN hero_xp INTEGER NOT NULL DEFAULT 0 CHECK(hero_xp >= 0);
ALTER TABLE account_progress ADD COLUMN campaign_cleared INTEGER NOT NULL DEFAULT 0 CHECK(campaign_cleared >= 0);
CREATE TABLE account_heroes (
 account_id INTEGER NOT NULL REFERENCES accounts(id), hero_id TEXT NOT NULL,
 level INTEGER NOT NULL DEFAULT 1 CHECK(level >= 1 AND level <= 100), xp INTEGER NOT NULL DEFAULT 0 CHECK(xp >= 0),
 PRIMARY KEY(account_id, hero_id)
);
INSERT INTO account_heroes(account_id,hero_id) SELECT id,'guardian' FROM accounts;
INSERT INTO account_heroes(account_id,hero_id) SELECT id,'fighter' FROM accounts;
