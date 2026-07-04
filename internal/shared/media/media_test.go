package media

import "testing"

func TestURL(t *testing.T) {
	Configure("https://s3.us-east-005.backblazeb2.com/", "botspace")
	cases := map[string]string{
		"template/cover_107.png": "https://s3.us-east-005.backblazeb2.com/botspace/template/cover_107.png",
		"/template/cover_45.png": "https://s3.us-east-005.backblazeb2.com/botspace/template/cover_45.png",
		"":                       "",
		"https://cdn.x/a.png":    "https://cdn.x/a.png", // already absolute — unchanged
	}
	for in, want := range cases {
		if got := URL(in); got != want {
			t.Fatalf("URL(%q) = %q, want %q", in, got, want)
		}
	}

	// Unconfigured ⇒ pass-through (no broken URL).
	Configure("", "")
	if got := URL("template/x.png"); got != "template/x.png" {
		t.Fatalf("unconfigured URL = %q, want raw key", got)
	}
}
