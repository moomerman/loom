package auth

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestQRMatchesReference checks the encoder module for module against
// python-qrcode (testdata/qr.json, made by qrcode 8 with level M, no
// border and the mask given) across versions 1 to 20: byte mode, the
// 16-bit count from version 10, version information from 7.
func TestQRMatchesReference(t *testing.T) {
	b, err := os.ReadFile("testdata/qr.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Text    string
		Mask    int
		Version int
		Rows    []string
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		c, err := encodeQRMask(tc.Text, tc.Mask)
		if err != nil {
			t.Fatalf("%.20q: %v", tc.Text, err)
		}
		if want := 17 + 4*tc.Version; len(c) != want {
			t.Errorf("%.20q: %d modules wide, want %d (version %d)", tc.Text, len(c), want, tc.Version)
			continue
		}
		for r, row := range c {
			var got strings.Builder
			for _, dark := range row {
				got.WriteByte("01"[b2i(dark)])
			}
			if got.String() != tc.Rows[r] {
				t.Errorf("%.20q (version %d, mask %d) row %d:\n got %s\nwant %s", tc.Text, tc.Version, tc.Mask, r, got.String(), tc.Rows[r])
				break
			}
		}
	}
	if _, err := encodeQR(strings.Repeat("x", 667)); err != errQRTooLong {
		t.Errorf("667 bytes: %v, want errQRTooLong", err)
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
