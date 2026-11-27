// Package dns talks to the Cloudflare API v4 to list zones and manage DNS
// records. The API token is the only credential; it is sent in an
// Authorization header, never in a URL, and never returned to the browser.
package dns

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

var (
	ErrNoToken = errors.New("no Cloudflare API token is set; add one first")
	idRe       = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

// APIError is an error reported by Cloudflare.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Client is a Cloudflare API client.
type Client struct {
	BaseURL string
	Token   func() string // read on every call so a changed token applies at once
	HTTP    *http.Client
}

// NewClient returns a client using the production API.
func NewClient(token func() string) *Client {
	return &Client{BaseURL: DefaultBaseURL, Token: token, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

// Zone is a domain in the Cloudflare account.
type Zone struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// Record is one DNS record.
type Record struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl"`
	Proxied  bool   `json:"proxied"`
	Priority *int   `json:"priority,omitempty"`
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result     json.RawMessage `json:"result"`
	ResultInfo struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*envelope, error) {
	tok := strings.TrimSpace(c.Token())
	if tok == "" {
		return nil, ErrNoToken
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach Cloudflare: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{res.StatusCode, fmt.Sprintf("unexpected reply from Cloudflare (HTTP %d)", res.StatusCode)}
	}
	if !env.Success {
		msg := fmt.Sprintf("Cloudflare returned HTTP %d", res.StatusCode)
		if len(env.Errors) > 0 {
			parts := make([]string, 0, len(env.Errors))
			for _, e := range env.Errors {
				parts = append(parts, e.Message)
			}
			msg = strings.Join(parts, "; ")
		}
		return nil, &APIError{res.StatusCode, msg}
	}
	return &env, nil
}

// Verify checks that the token is valid and active.
func (c *Client) Verify(ctx context.Context) error {
	env, err := c.do(ctx, "GET", "/user/tokens/verify", nil)
	if err != nil {
		return err
	}
	var r struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(env.Result, &r)
	if r.Status != "active" {
		return errors.New("the token is not active")
	}
	return nil
}

// Zones lists every zone the token can see.
func (c *Client) Zones(ctx context.Context) ([]Zone, error) {
	var all []Zone
	for page := 1; page <= 20; page++ {
		env, err := c.do(ctx, "GET", fmt.Sprintf("/zones?per_page=50&page=%d", page), nil)
		if err != nil {
			return nil, err
		}
		var z []Zone
		if err := json.Unmarshal(env.Result, &z); err != nil {
			return nil, err
		}
		all = append(all, z...)
		if page >= env.ResultInfo.TotalPages {
			break
		}
	}
	if all == nil {
		all = []Zone{}
	}
	return all, nil
}

func checkID(id string) error {
	if !idRe.MatchString(id) {
		return errors.New("invalid id")
	}
	return nil
}

// Records lists the DNS records of a zone.
func (c *Client) Records(ctx context.Context, zoneID string) ([]Record, error) {
	if err := checkID(zoneID); err != nil {
		return nil, err
	}
	var all []Record
	for page := 1; page <= 50; page++ {
		env, err := c.do(ctx, "GET", fmt.Sprintf("/zones/%s/dns_records?per_page=100&page=%d", zoneID, page), nil)
		if err != nil {
			return nil, err
		}
		var r []Record
		if err := json.Unmarshal(env.Result, &r); err != nil {
			return nil, err
		}
		all = append(all, r...)
		if page >= env.ResultInfo.TotalPages {
			break
		}
	}
	if all == nil {
		all = []Record{}
	}
	return all, nil
}

var (
	allowedTypes = map[string]bool{"A": true, "AAAA": true, "CNAME": true, "TXT": true, "MX": true, "NS": true, "SRV": true, "CAA": true}
	nameRe       = regexp.MustCompile(`^(?:@|\*|[A-Za-z0-9_*]([A-Za-z0-9_.*-]{0,251}[A-Za-z0-9_*])?)$`)
)

// Validate checks a record before it is sent to Cloudflare.
func Validate(r Record) (Record, error) {
	r.Type = strings.ToUpper(strings.TrimSpace(r.Type))
	r.Name = strings.TrimSpace(r.Name)
	r.Content = strings.TrimSpace(r.Content)
	if !allowedTypes[r.Type] {
		return r, errors.New("record type must be A, AAAA, CNAME, TXT, MX, NS, SRV or CAA")
	}
	if !nameRe.MatchString(r.Name) {
		return r, errors.New("invalid record name")
	}
	if r.Content == "" || len(r.Content) > 2048 || strings.ContainsAny(r.Content, "\x00\r\n") {
		return r, errors.New("invalid record content")
	}
	switch r.Type {
	case "A":
		if ip := net.ParseIP(r.Content); ip == nil || ip.To4() == nil {
			return r, errors.New("an A record needs an IPv4 address")
		}
	case "AAAA":
		if ip := net.ParseIP(r.Content); ip == nil || ip.To4() != nil {
			return r, errors.New("an AAAA record needs an IPv6 address")
		}
	case "CNAME", "NS":
		if !nameRe.MatchString(strings.TrimSuffix(r.Content, ".")) || strings.ContainsAny(r.Content, " *") {
			return r, errors.New("content must be a host name")
		}
	case "MX":
		if r.Priority == nil || *r.Priority < 0 || *r.Priority > 65535 {
			return r, errors.New("an MX record needs a priority between 0 and 65535")
		}
	}
	if r.Type != "MX" {
		r.Priority = nil
	}
	if r.TTL == 0 {
		r.TTL = 1
	}
	if r.TTL != 1 && (r.TTL < 60 || r.TTL > 86400) {
		return r, errors.New("TTL must be Auto (1) or between 60 and 86400 seconds")
	}
	if r.Proxied && r.Type != "A" && r.Type != "AAAA" && r.Type != "CNAME" {
		return r, errors.New("only A, AAAA and CNAME records can be proxied")
	}
	return r, nil
}

func (c *Client) write(ctx context.Context, method, zoneID, recID string, r Record) (Record, error) {
	if err := checkID(zoneID); err != nil {
		return r, err
	}
	r, err := Validate(r)
	if err != nil {
		return r, err
	}
	path := "/zones/" + zoneID + "/dns_records"
	if recID != "" {
		if err := checkID(recID); err != nil {
			return r, err
		}
		path += "/" + recID
	}
	r.ID = ""
	env, err := c.do(ctx, method, path, r)
	if err != nil {
		return r, err
	}
	var out Record
	if err := json.Unmarshal(env.Result, &out); err != nil {
		return r, err
	}
	return out, nil
}

// CreateRecord adds a record.
func (c *Client) CreateRecord(ctx context.Context, zoneID string, r Record) (Record, error) {
	return c.write(ctx, "POST", zoneID, "", r)
}

// UpdateRecord replaces a record.
func (c *Client) UpdateRecord(ctx context.Context, zoneID, recID string, r Record) (Record, error) {
	return c.write(ctx, "PUT", zoneID, recID, r)
}

// DeleteRecord removes a record.
func (c *Client) DeleteRecord(ctx context.Context, zoneID, recID string) error {
	if err := checkID(zoneID); err != nil {
		return err
	}
	if err := checkID(recID); err != nil {
		return err
	}
	_, err := c.do(ctx, "DELETE", "/zones/"+zoneID+"/dns_records/"+recID, nil)
	return err
}

// PointToServer makes name resolve to ip with an A record: an existing A
// record for that name is updated, otherwise one is created. A CNAME or any
// other record on the same name is left alone and reported, because replacing
// it silently could break mail or other services.
func (c *Client) PointToServer(ctx context.Context, zoneID, name, ip string, proxied bool) (Record, string, error) {
	want := Record{Type: "A", Name: name, Content: ip, TTL: 1, Proxied: proxied}
	if _, err := Validate(want); err != nil {
		return want, "", err
	}
	recs, err := c.Records(ctx, zoneID)
	if err != nil {
		return want, "", err
	}
	zname := ""
	if zs, err := c.Zones(ctx); err == nil {
		for _, z := range zs {
			if z.ID == zoneID {
				zname = z.Name
			}
		}
	}
	full := name
	if zname != "" && name != zname && !strings.HasSuffix(name, "."+zname) {
		full = name + "." + zname
	}
	if name == "@" && zname != "" {
		full = zname
	}
	for _, r := range recs {
		if !strings.EqualFold(r.Name, full) {
			continue
		}
		switch r.Type {
		case "A":
			if r.Content == ip && r.Proxied == proxied {
				return r, "already pointed", nil
			}
			out, err := c.UpdateRecord(ctx, zoneID, r.ID, want)
			return out, "updated", err
		case "CNAME", "AAAA":
			return r, "", fmt.Errorf("%s already has a %s record; remove or change it first", full, r.Type)
		}
	}
	out, err := c.CreateRecord(ctx, zoneID, want)
	return out, "created", err
}

// PublicIPv4 returns the first globally routable IPv4 address of this machine,
// without contacting any external service. On a server behind NAT this is the
// private address, so the UI lets the user edit it.
func PublicIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			return ip.String()
		}
	}
	return ""
}
