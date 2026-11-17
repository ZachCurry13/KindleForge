package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type recorded struct {
	Pkg     string `json:"pkg"`
	Install string `json:"install"`
	Stars   int    `json:"stars"`
}

// ratingsServer is a fake endpoint; status controls its answer.
func ratingsServer(t *testing.T, status *int, got *[]recorded) *httptest.Server {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate" || r.Method != "POST" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var rec recorded
		json.Unmarshal(b, &rec)
		if *status == 200 {
			*got = append(*got, rec)
		}
		w.WriteHeader(*status)
		fmt.Fprint(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	old := httpClient
	httpClient = srv.Client()
	t.Cleanup(func() { httpClient = old })
	return srv
}

func TestInstallIDIsStableRandomHex(t *testing.T) {
	c := gingrEnvConfig(t, "ok")
	a := installID(c)
	if len(a) != 32 || strings.Trim(a, "0123456789abcdef") != "" {
		t.Fatalf("bad id %q", a)
	}
	if installID(c) != a {
		t.Error("id must persist")
	}
	c2 := gingrEnvConfig(t, "ok")
	if installID(c2) == a {
		t.Error("different installs must get different ids")
	}
}

func TestRateSendsAndRemembers(t *testing.T) {
	status := 200
	var got []recorded
	srv := ratingsServer(t, &status, &got)
	c := gingrEnvConfig(t, "ok") // "hello" is installed

	st, err := rateCmd(c, srv.URL+"/", "hello", 4)
	if err != nil || st != "sent" {
		t.Fatalf("%s %v", st, err)
	}
	if len(got) != 1 || got[0].Pkg != "hello" || got[0].Stars != 4 || got[0].Install != installID(c) {
		t.Errorf("server got %+v", got)
	}
	if m := loadMine(c)["hello"]; m.Stars != 4 || !m.Sent {
		t.Errorf("mine %+v", m)
	}
	// the payload carries nothing but pkg/install/stars
	b, _ := json.Marshal(got[0])
	if strings.Count(string(b), ":") != 3 {
		t.Errorf("unexpected fields: %s", b)
	}
}

func TestRateQueuesOfflineThenFlushes(t *testing.T) {
	status := 503
	var got []recorded
	srv := ratingsServer(t, &status, &got)
	c := gingrEnvConfig(t, "ok")

	st, err := rateCmd(c, srv.URL, "hello", 5)
	if err != nil || st != "queued" {
		t.Fatalf("503 should queue: %s %v", st, err)
	}
	if m := loadMine(c)["hello"]; m.Stars != 5 || m.Sent {
		t.Errorf("should be remembered unsent: %+v", m)
	}
	flushPending(c, srv.URL) // still down
	if m := loadMine(c)["hello"]; m.Sent {
		t.Error("sent while the server is down")
	}
	status = 200
	flushPending(c, srv.URL)
	if m := loadMine(c)["hello"]; !m.Sent || len(got) != 1 {
		t.Errorf("flush failed: %+v got=%v", m, got)
	}
	// unreachable server also queues
	c2 := gingrEnvConfig(t, "ok")
	st, err = rateCmd(c2, "https://127.0.0.1:1", "hello", 2)
	if err != nil || st != "queued" {
		t.Errorf("unreachable: %s %v", st, err)
	}
}

func TestRateRefusals(t *testing.T) {
	status := 200
	var got []recorded
	srv := ratingsServer(t, &status, &got)
	c := gingrEnvConfig(t, "ok")

	for _, tc := range []struct {
		name, ep, pkg string
		stars         int
	}{
		{"not installed", srv.URL, "kwordle", 5},
		{"zero stars", srv.URL, "hello", 0},
		{"six stars", srv.URL, "hello", 6},
		{"bad id", srv.URL, "he llo", 5},
		{"no endpoint", "", "hello", 5},
		{"plain http endpoint", "http://example.org", "hello", 5},
	} {
		if st, err := rateCmd(c, tc.ep, tc.pkg, tc.stars); err == nil || st != "failure" {
			t.Errorf("%s accepted: %s", tc.name, st)
		}
	}
	if len(got) != 0 || len(loadMine(c)) != 0 {
		t.Errorf("a refused rating leaked through: %v %v", got, loadMine(c))
	}
	// the server refusing a vote (400) is a failure and is not remembered
	status = 400
	if st, err := rateCmd(c, srv.URL, "hello", 5); err == nil || st != "failure" {
		t.Errorf("400 should fail: %s %v", st, err)
	}
	if len(loadMine(c)) != 0 {
		t.Error("rejected vote kept")
	}
}

func TestApplyRatingsMinVotesAndMine(t *testing.T) {
	c := gingrEnvConfig(t, "ok")
	saveMine(c, map[string]MyRating{"hello": {Stars: 5, Sent: true}})
	rt := RatingsFile{MinVotes: 3, Packages: map[string]RatingAgg{
		"hello":   {Avg: 4.3, Count: 3},
		"kwordle": {Avg: 5, Count: 2}, // too few votes: hidden
	}}
	cat := Catalog{Packages: []CatPackage{{ID: "hello"}, {ID: "kwordle"}, {ID: "none"}}}
	applyRatings(c, ReposFile{RatingsURL: "https://r.example/"}, rt, &cat)
	if !cat.RatingsEnabled {
		t.Error("ratings should be enabled")
	}
	h, k, n := cat.Packages[0], cat.Packages[1], cat.Packages[2]
	if h.Rating != 4.3 || h.RatingCount != 3 || h.MyRating != 5 {
		t.Errorf("hello %+v", h)
	}
	if k.Rating != 0 || k.RatingCount != 0 {
		t.Errorf("kwordle should be hidden until %d votes: %+v", 3, k)
	}
	if n.Rating != 0 || n.MyRating != 0 {
		t.Errorf("none %+v", n)
	}
	cat2 := Catalog{Packages: []CatPackage{{ID: "hello"}}}
	applyRatings(c, ReposFile{}, rt, &cat2)
	if cat2.RatingsEnabled {
		t.Error("no endpoint means ratings are off")
	}
}

func TestLoadRatingsFileCaches(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"generatedAt":"x","minVotes":3,"packages":{"hello":{"avg":4.5,"count":8}}}`)
	}))
	defer srv.Close()
	old := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = old }()
	t.Setenv("KFPM_RATINGS_URL", srv.URL+"/ratings.json")

	c := gingrEnvConfig(t, "ok")
	if r := loadRatingsFile(c, true); r.Packages["hello"].Count != 8 {
		t.Fatalf("online: %+v", r)
	}
	t.Setenv("KFPM_RATINGS_URL", "https://127.0.0.1:1/x")
	if r := loadRatingsFile(c, true); r.Packages["hello"].Count != 8 {
		t.Errorf("cache fallback: %+v", r)
	}
	c2 := gingrEnvConfig(t, "ok")
	if r := loadRatingsFile(c2, true); len(r.Packages) != 0 || r.Packages == nil {
		t.Errorf("nothing cached: %+v", r)
	}
	os.Remove(c.path("cache", "ratings.json"))
}

func TestRatingsFileURLDerivedFromConfigURL(t *testing.T) {
	c := Config{ConfigURL: "https://raw.githubusercontent.com/o/r/master/catalog/config.json"}
	if got := ratingsFileURL(c); got != "https://raw.githubusercontent.com/o/r/master/catalog/ratings.json" {
		t.Errorf("got %s", got)
	}
}
