// KindleForge ratings: anonymous 1-5 star votes per package.
//
// Privacy: a vote is (package id, random install id, stars, time). No Kindle
// serial number, no account, no address is stored. Callers are rate-limited
// by a salted hash of their address that expires after two hours.
//
// Endpoints
//   POST   /rate               {pkg, install, stars}  -> {ok:true}
//   GET    /aggregate          -> {generatedAt, minVotes, packages:{id:{avg,count}}}
//   GET    /health             -> ok
//   DELETE /admin/pkg/<id>     (Authorization: Bearer ADMIN_TOKEN) removes a package's votes

const PKG_RE = /^[A-Za-z0-9._-]{1,64}$/;
const INSTALL_RE = /^[0-9a-f]{32}$/;
const MAX_BODY = 1024;
const MAX_PACKAGES = 500;        // stops junk ids from piling up
const VOTES_PER_HOUR = 40;       // per caller, across all packages

const JSON_HEADERS = { "content-type": "application/json; charset=utf-8" };

function json(body, status = 200, extra = {}) {
  return new Response(JSON.stringify(body), { status, headers: { ...JSON_HEADERS, ...extra } });
}

async function sha256Hex(text) {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

// Returns true if this caller is over the hourly limit.
async function limited(request, env, now = Date.now()) {
  const ip = request.headers.get("CF-Connecting-IP") || "unknown";
  const hour = Math.floor(now / 3600000);
  const key = (await sha256Hex(`${ip}|${env.SALT || ""}|${new Date(now).toISOString().slice(0, 10)}`)).slice(0, 24);
  await env.DB.prepare(
    "INSERT INTO ratelimit (key, window, n) VALUES (?, ?, 1) ON CONFLICT(key, window) DO UPDATE SET n = n + 1"
  ).bind(key, hour).run();
  const row = await env.DB.prepare("SELECT n FROM ratelimit WHERE key = ? AND window = ?").bind(key, hour).first();
  await env.DB.prepare("DELETE FROM ratelimit WHERE window < ?").bind(hour - 2).run();
  return (row ? row.n : 0) > VOTES_PER_HOUR;
}

async function rate(request, env) {
  const raw = await request.text();
  if (raw.length > MAX_BODY) return json({ error: "too large" }, 413);
  let body;
  try { body = JSON.parse(raw); } catch { return json({ error: "bad json" }, 400); }

  const { pkg, install, stars } = body || {};
  if (typeof pkg !== "string" || !PKG_RE.test(pkg)) return json({ error: "bad pkg" }, 400);
  if (typeof install !== "string" || !INSTALL_RE.test(install)) return json({ error: "bad install id" }, 400);
  if (!Number.isInteger(stars) || stars < 1 || stars > 5) return json({ error: "stars must be 1-5" }, 400);

  if (env.PKG_ALLOW) {
    const allowed = String(env.PKG_ALLOW).split(",").map((s) => s.trim());
    if (!allowed.includes(pkg)) return json({ error: "unknown package" }, 400);
  } else {
    const known = await env.DB.prepare("SELECT 1 AS x FROM votes WHERE pkg = ? LIMIT 1").bind(pkg).first();
    if (!known) {
      const n = await env.DB.prepare("SELECT COUNT(DISTINCT pkg) AS n FROM votes").first();
      if (n && n.n >= MAX_PACKAGES) return json({ error: "too many packages" }, 400);
    }
  }

  if (await limited(request, env)) return json({ error: "slow down" }, 429, { "retry-after": "3600" });

  await env.DB.prepare(
    "INSERT INTO votes (pkg, install, stars, at) VALUES (?, ?, ?, ?) " +
    "ON CONFLICT(pkg, install) DO UPDATE SET stars = excluded.stars, at = excluded.at"
  ).bind(pkg, install, stars, new Date().toISOString()).run();
  return json({ ok: true });
}

async function aggregate(env) {
  const { results } = await env.DB.prepare(
    "SELECT pkg, COUNT(*) AS n, AVG(stars) AS a FROM votes GROUP BY pkg ORDER BY pkg"
  ).all();
  const packages = {};
  for (const r of results) packages[r.pkg] = { avg: Math.round(r.a * 10) / 10, count: r.n };
  return json(
    { generatedAt: new Date().toISOString(), minVotes: parseInt(env.MIN_VOTES || "3", 10), packages },
    200,
    { "cache-control": "public, max-age=300" }
  );
}

async function adminDelete(request, env, pkg) {
  const auth = request.headers.get("Authorization") || "";
  if (!env.ADMIN_TOKEN || auth !== `Bearer ${env.ADMIN_TOKEN}`) return json({ error: "not found" }, 404);
  if (!PKG_RE.test(pkg)) return json({ error: "bad pkg" }, 400);
  const r = await env.DB.prepare("DELETE FROM votes WHERE pkg = ?").bind(pkg).run();
  return json({ ok: true, removed: r.meta ? r.meta.changes : undefined });
}

export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    try {
      if (request.method === "GET" && url.pathname === "/health") return new Response("ok");
      if (request.method === "GET" && url.pathname === "/aggregate") return aggregate(env);
      if (request.method === "POST" && url.pathname === "/rate") return rate(request, env);
      if (request.method === "DELETE" && url.pathname.startsWith("/admin/pkg/")) {
        return adminDelete(request, env, decodeURIComponent(url.pathname.slice("/admin/pkg/".length)));
      }
      return json({ error: "not found" }, 404);
    } catch (e) {
      return json({ error: "server error" }, 500);
    }
  },
};
