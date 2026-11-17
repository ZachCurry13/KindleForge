# KindleForge ratings (Cloudflare Worker + D1)

Anonymous 1-5 star ratings for packages. The Kindle sends `{pkg, install, stars}`; a scheduled GitHub Action publishes the totals to `catalog/ratings.json`, which the app reads.

## What is stored
One row per (package, install): the package id, a **random** install id (not the Kindle's serial number or anything about the device), the stars, and a timestamp. Nothing else. Callers are rate-limited by a **salted hash** of their address that is deleted after two hours; the address itself is never stored.

## Deploy (once)
You sign in yourself; nobody else needs your Cloudflare credentials.

```bash
cd ratings-worker
npm install
npx wrangler login                                   # opens your browser
npx wrangler d1 create kindleforge-ratings           # prints a database_id
#   -> paste it into wrangler.toml (database_id = "...")
npx wrangler d1 execute kindleforge-ratings --remote --file=schema.sql
npx wrangler secret put SALT                         # any long random string
npx wrangler secret put ADMIN_TOKEN                  # optional: lets you delete a package's votes
npx wrangler deploy                                  # prints https://kindleforge-ratings.kindleforge-ratings.workers.dev
```

Check it:

```bash
curl https://kindleforge-ratings.kindleforge-ratings.workers.dev/health        # ok
curl https://kindleforge-ratings.kindleforge-ratings.workers.dev/aggregate     # {"packages":{}...}
```

## Connect it to KindleForge
1. In `catalog/config.json` set `"ratingsURL": "https://kindleforge-ratings.kindleforge-ratings.workers.dev"` and push. Kindles pick it up on their next Refresh (no app update needed). Empty = ratings off.
2. In the GitHub repo: Settings > Secrets and variables > Actions > **Variables** > `RATINGS_URL` = the same URL. (On a fork, enable Actions on the Actions tab first.)
3. Run the **Update ratings** workflow once (Actions tab > Run workflow). It then runs every 6 hours.

## Abuse and moderation
- One vote per package per install id; re-voting changes it.
- 40 votes per hour per caller; at most 500 distinct package ids (or set `PKG_ALLOW` to a comma list to allow only real ones).
- A package shows an average only once it has `MIN_VOTES` (default 3) votes.
- Install ids are free to create, so a determined person can stuff votes. Remove a package's votes with
  `curl -X DELETE -H "Authorization: Bearer $ADMIN_TOKEN" https://<worker>/admin/pkg/<id>`.

## Develop / test
```bash
npm test                                    # Node tests against a SQLite-backed D1 stand-in
npx wrangler d1 execute kindleforge-ratings --local --file=schema.sql
npm run dev                                 # real workerd runtime on http://localhost:8787
```
