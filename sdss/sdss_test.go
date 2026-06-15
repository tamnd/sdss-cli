package sdss

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testServer starts an httptest server and returns a Client wired to it
// with pacing and retries disabled.
func testServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.baseURL = srv.URL
	c.Rate = 0
	c.Retries = 0
	return srv, c
}

// sqlResponse builds a two-element SDSS SQL response with the given rows.
func sqlResponse(rows []any) []wireTableResult {
	raw := make([]json.RawMessage, len(rows))
	for i, r := range rows {
		b, _ := json.Marshal(r)
		raw[i] = json.RawMessage(b)
	}
	return []wireTableResult{
		{TableName: "Table1", Rows: raw},
		{TableName: "SqlQuery", Rows: []json.RawMessage{[]byte(`{"query":"SELECT ..."}`)}},
	}
}

func TestGet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("request carried no User-Agent")
		}
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient()
	c.Rate = 0

	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestGetRetriesOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}

func TestQuerySQL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(sqlSearchPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("cmd")
		if q == "" {
			t.Error("missing cmd parameter")
		}
		if r.URL.Query().Get("format") != "json" {
			t.Error("missing format=json")
		}
		resp := sqlResponse([]any{
			map[string]any{"count": 1231051050},
		})
		json.NewEncoder(w).Encode(resp)
	})
	_, client := testServer(t, mux)

	rows, err := client.QuerySQL(context.Background(), "SELECT COUNT(*) FROM PhotoObjAll")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if _, ok := rows[0]["count"]; !ok {
		t.Errorf("expected 'count' key in row, got %v", rows[0])
	}
}

func TestSearchPhotometry(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(sqlSearchPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("cmd")
		if !strings.Contains(q, "PhotoObj") {
			t.Errorf("query does not mention PhotoObj: %q", q)
		}
		if !strings.Contains(q, "BETWEEN") {
			t.Errorf("query does not contain BETWEEN: %q", q)
		}
		resp := sqlResponse([]any{
			wirePhotoObj{ObjID: 1237645876861272067, RA: 336.44, Dec: -0.84, Type: 3, R: 14.19, G: 15.0, U: 16.0, I: 13.8, Z: 13.5},
			wirePhotoObj{ObjID: 1237645876861272068, RA: 100.0, Dec: 20.0, Type: 6, R: 15.5, G: 15.8, U: 16.2, I: 15.3, Z: 15.1},
		})
		json.NewEncoder(w).Encode(resp)
	})
	_, client := testServer(t, mux)

	objs, err := client.SearchPhotometry(context.Background(), 14, 16, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 2 {
		t.Fatalf("len = %d, want 2", len(objs))
	}
	if objs[0].ID != "1237645876861272067" {
		t.Errorf("ID = %q, want 1237645876861272067", objs[0].ID)
	}
	if objs[0].Type != "galaxy" {
		t.Errorf("Type = %q, want galaxy", objs[0].Type)
	}
	if objs[1].Type != "star" {
		t.Errorf("Type = %q, want star", objs[1].Type)
	}
	if objs[0].MagR != 14.19 {
		t.Errorf("MagR = %v, want 14.19", objs[0].MagR)
	}
	if objs[0].RA != 336.44 {
		t.Errorf("RA = %v, want 336.44", objs[0].RA)
	}
}

func TestSearchSpectra(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(sqlSearchPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("cmd")
		if !strings.Contains(q, "SpecObj") {
			t.Errorf("query does not mention SpecObj: %q", q)
		}
		if !strings.Contains(q, "GALAXY") {
			t.Errorf("query does not filter by GALAXY: %q", q)
		}
		resp := sqlResponse([]any{
			wireSpecObj{SpecObjID: 299489248914612224, RA: 185.0, Dec: 0.5, Redshift: 0.12, Class: "GALAXY", SubClass: ""},
			wireSpecObj{SpecObjID: 299489248914612225, RA: 186.0, Dec: 1.0, Redshift: 0.18, Class: "GALAXY", SubClass: "AGN"},
		})
		json.NewEncoder(w).Encode(resp)
	})
	_, client := testServer(t, mux)

	specs, err := client.SearchSpectra(context.Background(), "GALAXY", 0.1, 0.2, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("len = %d, want 2", len(specs))
	}
	if specs[0].ID != "299489248914612224" {
		t.Errorf("ID = %q, want 299489248914612224", specs[0].ID)
	}
	if specs[0].Redshift != 0.12 {
		t.Errorf("Redshift = %v, want 0.12", specs[0].Redshift)
	}
	if specs[0].Class != "GALAXY" {
		t.Errorf("Class = %q, want GALAXY", specs[0].Class)
	}
	if specs[1].SubClass != "AGN" {
		t.Errorf("SubClass = %q, want AGN", specs[1].SubClass)
	}
}

func TestNearestObjects(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(sqlSearchPath, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("cmd")
		if !strings.Contains(q, "fGetNearbyObjEq") {
			t.Errorf("query does not use fGetNearbyObjEq: %q", q)
		}
		resp := sqlResponse([]any{
			wirePhotoObj{ObjID: 1237645941826961408, RA: 185.01, Dec: 0.51, Type: 3, R: 17.2},
		})
		json.NewEncoder(w).Encode(resp)
	})
	_, client := testServer(t, mux)

	objs, err := client.NearestObjects(context.Background(), 185.0, 0.5, 1.0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(objs) != 1 {
		t.Fatalf("len = %d, want 1", len(objs))
	}
	if objs[0].ID != "1237645941826961408" {
		t.Errorf("ID = %q, want 1237645941826961408", objs[0].ID)
	}
	if objs[0].Type != "galaxy" {
		t.Errorf("Type = %q, want galaxy", objs[0].Type)
	}
}
