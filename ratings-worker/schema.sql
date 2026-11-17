-- One vote per (package, install). Re-voting updates it.
CREATE TABLE IF NOT EXISTS votes (
  pkg     TEXT    NOT NULL,
  install TEXT    NOT NULL,
  stars   INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
  at      TEXT    NOT NULL,
  PRIMARY KEY (pkg, install)
);
CREATE INDEX IF NOT EXISTS idx_votes_pkg ON votes (pkg);

-- Coarse abuse limiter. Keys are salted hashes of the caller's address,
-- never the address itself, and rows expire after a couple of hours.
CREATE TABLE IF NOT EXISTS ratelimit (
  key    TEXT    NOT NULL,
  window INTEGER NOT NULL,
  n      INTEGER NOT NULL,
  PRIMARY KEY (key, window)
);
