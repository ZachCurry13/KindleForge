package main

// Package ratings. KFPM sends a rating to the ratings endpoint (a small
// Cloudflare Worker, see ratings-worker/) and reads averages from a
// ratings.json that a GitHub Action publishes into the repo. The Kindle never
// holds any credential: a vote is (package id, random install id, stars).
//
// The install id is random, created on first use, and is NOT derived from the
// Kindle's serial number or anything else about the device.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RatingAgg struct {
	Avg   float64 `json:"avg"`
	Count int     `json:"count"`
}

type RatingsFile struct {
	GeneratedAt string               `json:"generatedAt"`
	MinVotes    int                  `json:"minVotes"`
	Packages    map[string]RatingAgg `json:"packages"`
}

type MyRating struct {
	Stars int    `json:"stars"`
	Sent  bool   `json:"sent"`
	At    string `json:"at"`
}

// ---- install id + own ratings ---------------------------------------------

func installID(c Config) string {
	p := c.path("install-id")
	if data, err := os.ReadFile(p); err == nil {
		if id := strings.TrimSpace(string(data)); len(id) == 32 {
			return id
		}
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	id := hex.EncodeToString(b)
	_ = os.MkdirAll(c.DataDir, 0o755)
	_ = os.WriteFile(p, []byte(id+"\n"), 0o644)
	return id
}

func loadMine(c Config) map[string]MyRating {
	m := map[string]MyRating{}
	if data, err := os.ReadFile(c.path("my-ratings.json")); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	return m
}

func saveMine(c Config, m map[string]MyRating) {
	if data, err := json.MarshalIndent(m, "", "  "); err == nil {
		_ = os.MkdirAll(c.DataDir, 0o755)
		_ = writeAtomic(c.path("my-ratings.json"), data)
	}
}

// ---- sending --------------------------------------------------------------

func cleanEndpoint(ep string) string {
	ep = strings.TrimRight(strings.TrimSpace(ep), "/")
	if !strings.HasPrefix(ep, "https://") {
		return ""
	}
	return ep
}

type sendResult int

const (
	sendOK         sendResult = iota
	sendRetryLater            // offline, timeout, 5xx, rate limited
	sendRejected              // the server refused this vote (4xx)
)

func sendRating(endpoint, id, pkg string, stars int) (sendResult, error) {
	body, _ := json.Marshal(map[string]any{"pkg": pkg, "install": id, "stars": stars})
	req, _ := http.NewRequest("POST", endpoint+"/rate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "KindleForge-KFPM")
	resp, err := httpClient.Do(req)
	if err != nil {
		return sendRetryLater, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == http.StatusOK:
		return sendOK, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return sendRetryLater, fmt.Errorf("server said HTTP %d", resp.StatusCode)
	default:
		return sendRejected, fmt.Errorf("rating refused (HTTP %d)", resp.StatusCode)
	}
}

// rateCmd records the rating locally, then tries to send it. Returns the
// status for the page: "sent", "queued" (will be sent at the next sync) or an
// error ("failure").
func rateCmd(c Config, endpoint, pkg string, stars int) (string, error) {
	if stars < 1 || stars > 5 {
		return "failure", fmt.Errorf("stars must be 1 to 5")
	}
	if !validID.MatchString(pkg) {
		return "failure", fmt.Errorf("invalid package id")
	}
	if _, ok := listInstalled(c)[pkg]; !ok {
		return "failure", fmt.Errorf("install %s before rating it", pkg)
	}
	ep := cleanEndpoint(endpoint)
	if ep == "" {
		return "failure", fmt.Errorf("ratings are not enabled (no https endpoint configured)")
	}
	id := installID(c)
	if id == "" {
		return "failure", fmt.Errorf("could not create an install id")
	}

	mine := loadMine(c)
	mine[pkg] = MyRating{Stars: stars, Sent: false, At: time.Now().UTC().Format(time.RFC3339)}
	saveMine(c, mine)

	res, err := sendRating(ep, id, pkg, stars)
	switch res {
	case sendOK:
		mine[pkg] = MyRating{Stars: stars, Sent: true, At: mine[pkg].At}
		saveMine(c, mine)
		return "sent", nil
	case sendRetryLater:
		appendLog(c, "rating queued: "+err.Error())
		return "queued", nil
	default:
		delete(mine, pkg) // the server will never accept it; do not keep showing it
		saveMine(c, mine)
		return "failure", err
	}
}

// flushPending sends ratings that could not be sent earlier.
func flushPending(c Config, endpoint string) {
	ep := cleanEndpoint(endpoint)
	if ep == "" {
		return
	}
	mine := loadMine(c)
	id := installID(c)
	changed := false
	for pkg, r := range mine {
		if r.Sent {
			continue
		}
		res, err := sendRating(ep, id, pkg, r.Stars)
		switch res {
		case sendOK:
			r.Sent = true
			mine[pkg] = r
			changed = true
		case sendRejected:
			delete(mine, pkg)
			changed = true
			logf("rating for %s refused: %v", pkg, err)
		default:
			return // offline: stop trying for now
		}
	}
	if changed {
		saveMine(c, mine)
	}
}

// ---- reading published averages -------------------------------------------

func ratingsFileURL(c Config) string {
	if v := os.Getenv("KFPM_RATINGS_URL"); v != "" {
		return v
	}
	if strings.HasSuffix(c.ConfigURL, "/config.json") {
		return strings.TrimSuffix(c.ConfigURL, "config.json") + "ratings.json"
	}
	return ""
}

func loadRatingsFile(c Config, online bool) RatingsFile {
	cp := c.path("cache", "ratings.json")
	parse := func(data []byte) (RatingsFile, bool) {
		var r RatingsFile
		if json.Unmarshal(data, &r) != nil || r.Packages == nil {
			return r, false
		}
		return r, true
	}
	if u := ratingsFileURL(c); online && u != "" {
		if resp, err := httpClient.Get(u); err == nil {
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if rerr == nil && resp.StatusCode == http.StatusOK {
				if r, ok := parse(data); ok {
					_ = os.MkdirAll(filepath.Dir(cp), 0o755)
					_ = writeAtomic(cp, data)
					return r
				}
			}
		}
	}
	if data, err := os.ReadFile(cp); err == nil {
		if r, ok := parse(data); ok {
			return r
		}
	}
	return RatingsFile{Packages: map[string]RatingAgg{}}
}

// applyRatings merges published averages (only once a package has enough
// votes) and the user's own ratings into the catalog.
func applyRatings(c Config, rf ReposFile, rt RatingsFile, cat *Catalog) {
	cat.RatingsEnabled = cleanEndpoint(rf.RatingsURL) != ""
	min := rt.MinVotes
	if min < 1 {
		min = 3
	}
	mine := loadMine(c)
	for i := range cat.Packages {
		p := &cat.Packages[i]
		if a, ok := rt.Packages[p.ID]; ok && a.Count >= min {
			p.Rating, p.RatingCount = a.Avg, a.Count
		}
		if m, ok := mine[p.ID]; ok {
			p.MyRating = m.Stars
		}
	}
}
