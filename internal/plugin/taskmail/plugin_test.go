package taskmail

import (
	"strings"
	"testing"
)

func TestPassiveForwardedMailFormats(t *testing.T) {
	for _, tc := range []struct{ headers, body, want, code string }{
		{"Content-Transfer-Encoding: base64\r\n", "U3RhdHVz", "Status", ""},
		{"Content-Type: multipart/mixed; boundary=example\r\n", "--example", "", "unsupported_message"},
		{"Content-Type: text/html\r\n", "<b>unopened</b>", "", "unsupported_message"},
		{"Content-Type: text/plain; charset=latin1\r\n", "other", "", "unsupported_message"},
		{"From: another@example.org\r\n", "duplicate", "", "invalid_message"},
		{"Content-Transfer-Encoding: base64\r\n", "broken#", "", "invalid_message"},
	} {
		m := parse([]byte("From: Vendor <vendor@example.org>\r\n" + tc.headers + "\r\n" + tc.body))
		if m.Error != tc.code || m.Text != tc.want {
			t.Fatal(tc, m)
		}
	}
	if got := parse([]byte("From: vendor@example.org\r\n\r\n" + strings.Repeat("a", 1024))); len(got.Text) != 1024 {
		t.Fatal("lost accepted body")
	}
}
