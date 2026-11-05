ALTER TABLE account_progress ADD COLUMN afk_last_claim INTEGER NOT NULL DEFAULT 0 CHECK(afk_last_claim >= 0);
