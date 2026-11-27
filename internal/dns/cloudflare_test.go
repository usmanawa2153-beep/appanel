package dns

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const zid = "0123456789abcdef0123456789abcdef"
const rid = "fedcba9876543210fedcba9876543210"

type fakeCF struct {
	mu      sync.Mutex
	auth    []string
	records []Record
	calls   []string
}

func (f *fakeCF) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	reply := func(v any, pages int) {
		b, _ := json.Marshal(v)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"errors":[],"result":` + string(b) + `,"result_info":{"page":1,"total_pages":` + string(rune('0'+pages)) + `}}`))
	}
	switch {
	case r.URL.Path == "/user/tokens/verify":
		reply(map[string]string{"status": "active"}, 1)
	case r.URL.Path == "/zones":
		reply([]Zone{{ID: zid, Name: "example.com", Status: "active"}}, 1)
	case r.URL.Path == "/zones/"+zid+"/dns_records" && r.Method == "GET":
		reply(f.records, 1)
	case r.URL.Path == "/zones/"+zid+"/dns_records" && r.Method == "POST":
		var rec Record
		json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = rid
		f.records = append(f.records, rec)
		reply(rec, 1)
	case strings.HasPrefix(r.URL.Path, "/zones/"+zid+"/dns_records/") && r.Method == "PUT":
		var rec Record
		json.NewDecoder(r.Body).Decode(&rec)
		rec.ID = strings.TrimPrefix(r.URL.Path, "/zones/"+zid+"/dns_records/")
		reply(rec, 1)
	case r.Method == "DELETE":
		reply(map[string]string{"id": rid}, 1)
	default:
		w.WriteHeader(403)
		w.Write([]byte(`{"success":false,"errors":[{"code":9109,"message":"Invalid access token"}],"result":null}`))
	}
}

func client(t *testing.T, tok string) (*Client, *fakeCF) {
	f := &fakeCF{}
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(ts.Close)
	return &Client{BaseURL: ts.URL, Token: func() string { return tok }, HTTP: ts.Client()}, f
}

func TestNoToken(t *testing.T) {
	c, f := client(t, "  ")
	if _, err := c.Zones(context.Background()); err != ErrNoToken {
		t.Errorf("%v", err)
	}
	if len(f.calls) != 0 {
		t.Error("request sent without a token")
	}
}

func TestZonesSendBearerHeaderNotURL(t *testing.T) {
	c, f := client(t, "secret-token")
	zs, err := c.Zones(context.Background())
	if err != nil || len(zs) != 1 || zs[0].Name != "example.com" {
		t.Fatalf("%v %+v", err, zs)
	}
	if f.auth[0] != "Bearer secret-token" {
		t.Errorf("auth %q", f.auth[0])
	}
	if err := c.Verify(context.Background()); err != nil {
		t.Error(err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	f := &fakeCF{}
	ts := httptest.NewServer(http.HandlerFunc(f.handler))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL + "/nope", Token: func() string { return "x" }, HTTP: ts.Client()}
	_, err := c.Zones(context.Background())
	var ae *APIError
	if err == nil || !strings.Contains(err.Error(), "Invalid access token") {
		t.Fatalf("%v", err)
	}
	_ = ae
}

func TestValidate(t *testing.T) {
	p := 10
	ok := []Record{
		{Type: "a", Name: "www", Content: "203.0.113.5", Proxied: true},
		{Type: "AAAA", Name: "@", Content: "2001:db8::1"},
		{Type: "CNAME", Name: "blog", Content: "target.example.net."},
		{Type: "TXT", Name: "_dmarc", Content: "v=DMARC1; p=none"},
		{Type: "MX", Name: "@", Content: "mail.example.com", Priority: &p, TTL: 3600},
	}
	for _, r := range ok {
		if _, err := Validate(r); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
	bad := []Record{
		{Type: "A", Name: "www", Content: "2001:db8::1"},
		{Type: "AAAA", Name: "www", Content: "1.2.3.4"},
		{Type: "A", Name: "bad name", Content: "1.2.3.4"},
		{Type: "A", Name: "www", Content: "1.2.3.4", TTL: 5},
		{Type: "TXT", Name: "x", Content: "a\nb"},
		{Type: "TXT", Name: "x", Content: "a", Proxied: true},
		{Type: "MX", Name: "@", Content: "mail.example.com"},
		{Type: "SOA", Name: "@", Content: "x"},
		{Type: "CNAME", Name: "x", Content: "has space"},
	}
	for _, r := range bad {
		if _, err := Validate(r); err == nil {
			t.Errorf("accepted %+v", r)
		}
	}
	r, _ := Validate(Record{Type: "A", Name: "www", Content: "1.2.3.4"})
	if r.TTL != 1 {
		t.Errorf("default ttl %d", r.TTL)
	}
}

func TestRejectsBadIDsBeforeNetwork(t *testing.T) {
	c, f := client(t, "t")
	if _, err := c.Records(context.Background(), "../../x"); err == nil {
		t.Error("bad zone id accepted")
	}
	if err := c.DeleteRecord(context.Background(), zid, "../zones"); err == nil {
		t.Error("bad record id accepted")
	}
	if len(f.calls) != 0 {
		t.Errorf("network used: %v", f.calls)
	}
}

func TestPointToServer(t *testing.T) {
	c, f := client(t, "t")
	ctx := context.Background()
	rec, how, err := c.PointToServer(ctx, zid, "www", "203.0.113.5", false)
	if err != nil || how != "created" || rec.ID != rid {
		t.Fatalf("%v %q %+v", err, how, rec)
	}
	f.records = []Record{{ID: rid, Type: "A", Name: "www.example.com", Content: "203.0.113.5", TTL: 1}}
	if _, how, _ = c.PointToServer(ctx, zid, "www", "203.0.113.5", false); how != "already pointed" {
		t.Errorf("%q", how)
	}
	if _, how, _ = c.PointToServer(ctx, zid, "www", "198.51.100.9", false); how != "updated" {
		t.Errorf("%q", how)
	}
	f.records = []Record{{ID: rid, Type: "CNAME", Name: "www.example.com", Content: "other.net"}}
	if _, _, err = c.PointToServer(ctx, zid, "www", "203.0.113.5", false); err == nil || !strings.Contains(err.Error(), "CNAME") {
		t.Errorf("CNAME clobbered: %v", err)
	}
	f.records = []Record{{ID: rid, Type: "A", Name: "example.com", Content: "1.1.1.1"}}
	if _, how, _ = c.PointToServer(ctx, zid, "@", "203.0.113.5", false); how != "updated" {
		t.Errorf("apex: %q", how)
	}
}
