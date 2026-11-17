package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestSyncedAtOnlyAdvancesOnRealNetworkData(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "hello\nkwordle\n") }))
	defer srv.Close()
	old := httpClient
	httpClient = srv.Client()
	defer func() { httpClient = old }()

	c := gingrEnvConfig(t, "ok")
	os.MkdirAll(c.path("cache"), 0o755)
	os.WriteFile(c.path("cache", "config.json"), []byte(`{"repos":[{"id":"x","url":"https://127.0.0.1:1/none"}]}`), 0o644)
	os.WriteFile(c.GingrMirror, []byte(srv.URL+"/list "+srv.URL+"/{pkg}.zip\n"), 0o644)

	read := func() Catalog {
		var cat Catalog
		readJSON(t, c.path("catalog.json"), &cat)
		return cat
	}

	// 1) a sync with the network up: both times set
	if err := refreshCatalog(c, true, false); err != nil {
		t.Fatalf("online sync: %v", err)
	}
	first := read()
	if first.SyncedAt == "" || first.AttemptedAt == "" || first.SyncedAt != first.AttemptedAt {
		t.Fatalf("times after a real sync: %+v / %+v", first.SyncedAt, first.AttemptedAt)
	}

	// 2) a local rebuild (after an install) must not touch either
	time.Sleep(1100 * time.Millisecond)
	if err := refreshCatalog(c, false, false); err != nil {
		t.Fatal(err)
	}
	if got := read(); got.SyncedAt != first.SyncedAt || got.AttemptedAt != first.AttemptedAt {
		t.Errorf("a local rebuild changed the sync times: %+v", got)
	}

	// 3) a sync with the network down: it must FAIL, keep SyncedAt, move AttemptedAt
	os.WriteFile(c.GingrMirror, []byte("https://127.0.0.1:1/list https://127.0.0.1:1/{pkg}.zip\n"), 0o644)
	err := refreshCatalog(c, true, false)
	if err == nil {
		t.Fatal("a sync that only used the saved copy must be reported as a failure")
	}
	failed := read()
	if failed.SyncedAt != first.SyncedAt {
		t.Errorf("SyncedAt advanced without network data: %s -> %s", first.SyncedAt, failed.SyncedAt)
	}
	if failed.AttemptedAt == first.AttemptedAt {
		t.Error("AttemptedAt should record the failed attempt")
	}
	if len(failed.Packages) == 0 {
		t.Error("the saved list must still be usable after a failed sync")
	}
}
