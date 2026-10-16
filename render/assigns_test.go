package render_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/render"
)

func TestAssignsJSONRoundTrip(t *testing.T) {
	in := hxtest.ProfileAssigns{
		User:   hxtest.User{Name: "Ada", UserID: 7, HTMLBio: "<b>", CreatedAt: "today", Address: hxtest.Address{City: "London"}},
		Admin:  true,
		Scores: []int{1, 2},
		Ratio:  0.5,
	}
	b, err := json.Marshal(render.AssignsJSON(in))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"admin":true,"ratio":0.5,"scores":[1,2],"user":{"address":{"city":"London"},"created_at":"today","html_bio":"\u003cb\u003e","name":"Ada","user_id":7}}`
	if string(b) != want {
		t.Errorf("got  %s\nwant %s", b, want)
	}
	var out hxtest.ProfileAssigns
	if err := render.DecodeAssigns(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.User != in.User || out.Admin != in.Admin || len(out.Scores) != 2 || out.Ratio != 0.5 {
		t.Errorf("round trip: %+v", out)
	}
}

func TestDecodeAssignsErrors(t *testing.T) {
	for _, tc := range []struct{ json, want string }{
		{`{"user":{"nme":"x"}}`, `user: unknown field "nme"; fields are address, created_at, html_bio, name, user_id`},
		{`{"scores":[1,"2"]}`, `scores[1]: want an integer, got the string "2"`},
		{`{"admin":"yes"}`, `admin: want true or false, got the string "yes"`},
		{`{"scores":[1.5]}`, `scores[0]: want an integer, got the number 1.5`},
		{`[]`, `assigns: want an object, got an array`},
		{`{`, `assigns are not valid JSON`},
	} {
		var a hxtest.ProfileAssigns
		err := render.DecodeAssigns([]byte(tc.json), &a)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.json, err, tc.want)
		}
	}
}

func TestLookup(t *testing.T) {
	for _, name := range []string{"Badge", "hxtest.Badge"} {
		c, err := render.Lookup(name)
		if err != nil || c.ComponentName() != "Badge" {
			t.Errorf("Lookup(%q) = %v, %v", name, c, err)
		}
	}
	if _, err := render.Lookup("Nope"); err == nil || !strings.Contains(err.Error(), "hxtest.TodoList") {
		t.Errorf("got %v", err)
	}
	c, _ := render.Lookup("Badge")
	r, err := c.RenderJSON([]byte(`{"label":"hi"}`))
	if err != nil || r.HTML() != `<span class="badge">hi</span>` {
		t.Errorf("got %v, %v", r, err)
	}
}
