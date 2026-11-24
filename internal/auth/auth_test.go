package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword("correct horse battery", h) {
		t.Fatal("correct password rejected")
	}
	if VerifyPassword("wrong", h) {
		t.Fatal("wrong password accepted")
	}
	if VerifyPassword("x", "garbage") || VerifyPassword("x", "pbkdf2-sha256$1$AAAA$") {
		t.Fatal("malformed hash accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
}

func TestRandomPassword(t *testing.T) {
	p := RandomPassword(24)
	if len(p) != 24 || strings.ContainsAny(p, "0O1lI") {
		t.Fatalf("bad password %q", p)
	}
}

// RFC 6238 appendix B, SHA1, secret "12345678901234567890".
func TestTOTPVectors(t *testing.T) {
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1234567890, "005924"}, {2000000000, "279037"}}
	for _, c := range cases {
		if !VerifyTOTP(secret, c.code, time.Unix(c.unix, 0)) {
			t.Errorf("t=%d code %s rejected", c.unix, c.code)
		}
	}
	if VerifyTOTP(secret, "000000", time.Unix(59, 0)) {
		t.Error("bad code accepted")
	}
	if VerifyTOTP(secret, "28708", time.Unix(59, 0)) {
		t.Error("short code accepted")
	}
	// one step of drift is tolerated, two is not
	if !VerifyTOTP(secret, "287082", time.Unix(59+30, 0)) {
		t.Error("one-step drift rejected")
	}
	if VerifyTOTP(secret, "287082", time.Unix(59+90, 0)) {
		t.Error("three-step drift accepted")
	}
}

func TestSessions(t *testing.T) {
	s := NewSessions(time.Hour)
	a := s.Create(1)
	b := s.Create(1)
	if _, ok := s.Get(a.Token); !ok {
		t.Fatal("session missing")
	}
	s.DeleteForUser(1, a.Token)
	if _, ok := s.Get(b.Token); ok {
		t.Fatal("other session should be gone")
	}
	if _, ok := s.Get(a.Token); !ok {
		t.Fatal("kept session removed")
	}
	short := NewSessions(time.Millisecond)
	x := short.Create(2)
	time.Sleep(5 * time.Millisecond)
	if _, ok := short.Get(x.Token); ok {
		t.Fatal("expired session valid")
	}
}

func TestTicketLockout(t *testing.T) {
	s := NewSessions(time.Hour)
	tk := s.NewTicket(7)
	for i := 0; i < ticketMaxAttempts; i++ {
		if _, ok := s.TicketUser(tk); !ok {
			t.Fatalf("ticket died early at %d", i)
		}
		s.TicketFail(tk)
	}
	if _, ok := s.TicketUser(tk); ok {
		t.Fatal("ticket should be dead after max failures")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute, time.Minute)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("ip"); !ok {
			t.Fatal("blocked too early")
		}
		l.Fail("ip")
	}
	if ok, wait := l.Allowed("ip"); ok || wait <= 0 {
		t.Fatal("should be locked")
	}
	if ok, _ := l.Allowed("other"); !ok {
		t.Fatal("unrelated key blocked")
	}
	l.Reset("ip")
	if ok, _ := l.Allowed("ip"); !ok {
		t.Fatal("reset did not unlock")
	}
}
