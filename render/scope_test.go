package render

import "testing"

func TestPath(t *testing.T) {
	mounted := &Scope{Prefix: "/admin/jobs"}
	for _, c := range []struct {
		s        *Scope
		in, want string
	}{
		{nil, "/", "/"},
		{nil, "/jobs/1", "/jobs/1"},
		{&Scope{}, "/jobs", "/jobs"},
		{mounted, "/", "/admin/jobs"},
		{mounted, "/jobs/1", "/admin/jobs/jobs/1"},
		{mounted, "/?state=discarded", "/admin/jobs?state=discarded"},
		{mounted, "/#top", "/admin/jobs#top"},
	} {
		if got := Path(c.s, c.in); got != c.want {
			t.Errorf("Path(%v, %q) = %q, want %q", c.s, c.in, got, c.want)
		}
	}
}

func TestAsset(t *testing.T) {
	if got := Asset(nil, "app.css"); got != "/_assets/app.css" {
		t.Errorf("Asset(nil) = %q", got)
	}
	if got := Asset(nil, "player/"); got != "/_assets/player/" {
		t.Errorf("Asset(nil, dir) = %q", got)
	}
	s := &Scope{Prefix: "/x", Asset: func(n string) (string, bool) { return "/x/_assets/app.1234.css", n == "app.css" }}
	if got := Asset(s, "app.css"); got != "/x/_assets/app.1234.css" {
		t.Errorf("Asset = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("unknown asset didn't panic")
		}
	}()
	Asset(s, "nope.css")
}
