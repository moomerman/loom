package auth

import (
	"testing"
	"time"
)

// RFC 6238's SHA-1 test vectors, the last six of their eight digits.
func TestTOTPVectors(t *testing.T) {
	secret := base32NoPad.EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		unix int64
		code string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		got, err := totpCode(secret, totpStep(time.Unix(tc.unix, 0)))
		if err != nil || got != tc.code {
			t.Errorf("at %d: %q, %v, want %q", tc.unix, got, err, tc.code)
		}
	}
}

func TestCheckTOTP(t *testing.T) {
	secret, err := newTOTPSecret()
	if err != nil || len(secret) != 32 {
		t.Fatalf("secret %q, %v: want 32 base32 characters (160 bits)", secret, err)
	}
	now := time.Unix(1_700_000_000, 0)
	code := func(d time.Duration) string { c, _ := totpCode(secret, totpStep(now.Add(d))); return c }
	for _, tc := range []struct {
		code string
		ok   bool
		step int64
	}{
		{code(0), true, totpStep(now)},
		{code(-30 * time.Second), true, totpStep(now) - 1}, // a slow clock
		{code(30 * time.Second), true, totpStep(now) + 1},  // a fast one
		{code(-60 * time.Second), false, 0},
		{code(60 * time.Second), false, 0},
		{code(0)[:3] + " " + code(0)[3:], true, totpStep(now)}, // as some apps show it
		{"12345", false, 0},
		{"", false, 0},
	} {
		step, ok := checkTOTP(secret, tc.code, now)
		if ok != tc.ok || step != tc.step {
			t.Errorf("%q: step %d, %v; want %d, %v", tc.code, step, ok, tc.step, tc.ok)
		}
	}
}

func TestTOTPURI(t *testing.T) {
	got := totpURI("Loom Issues", "ada+work@example.com", "JBSWY3DPEHPK3PXP")
	want := "otpauth://totp/Loom%20Issues:ada%2Bwork%40example.com?secret=JBSWY3DPEHPK3PXP&issuer=Loom%20Issues"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if g := groupSecret("JBSWY3DPEHPK3PXP"); g != "JBSW Y3DP EHPK 3PXP" {
		t.Errorf("grouped %q", g)
	}
}
