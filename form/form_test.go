package form_test

import (
	"errors"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/moomerman/loom/form"
	"github.com/moomerman/loom/render"
)

type signup struct {
	Name    string
	Age     int
	Rating  float64
	Agree   bool
	Shares  map[string]string
	Members map[string]bool
	//lint:ignore U1000 unexported fields aren't form fields, which the test checks
	secret string
}

func (in *signup) Validate(e *form.Errors) {
	if strings.TrimSpace(in.Name) == "" {
		e.Add(&in.Name, "Enter a name.")
	}
	if !e.Has(&in.Age) && in.Age < 18 {
		e.Add(&in.Age, "You must be 18 or over.")
	}
	for k, v := range in.Shares {
		if v == "" {
			e.AddKey(&in.Shares, k, "Enter a share.")
		}
	}
}

func TestNewShowsNoErrors(t *testing.T) {
	f := form.New(signup{Age: 30, Members: map[string]bool{"sam": true, "ali": false}})
	if got := f.Field("name"); got != (form.Field{Name: "name"}) {
		t.Errorf("name = %+v, want no value and no error", got)
	}
	if got := f.Field("age").Value; got != "30" {
		t.Errorf("age = %q, want 30", got)
	}
	if got := f.Entries("members").Key("sam").Value; got != "true" {
		t.Errorf("members[sam] = %q, want true", got)
	}
	if got := f.Entries("members").Key("new").Value; got != "false" {
		t.Errorf("members[new] = %q, want false", got)
	}
	if f.Valid() {
		t.Error("an empty name should make the form invalid, though no error shows")
	}
}

func TestChangeShowsErrorsOfChangedFields(t *testing.T) {
	f := form.New(signup{Age: 30})
	f.Change(map[string]string{"name": "", "age": "12"})
	if got := f.Field("age").Error; got != "You must be 18 or over." {
		t.Errorf("age error = %q", got)
	}
	if got := f.Field("name").Error; got != "" {
		t.Errorf("name error = %q, want none: it wasn't changed", got)
	}
	f.Change(map[string]string{"name": "Al", "age": "12"})
	f.Change(map[string]string{"name": "", "age": "12"})
	if got := f.Field("name").Error; got != "Enter a name." {
		t.Errorf("name error = %q: a field once changed keeps showing its error", got)
	}
}

func TestParseErrorsComeFirst(t *testing.T) {
	f := form.New(signup{})
	f.Change(map[string]string{"age": "abc", "rating": "x"})
	if got := f.Field("age"); got.Error != "Enter a whole number." || got.Value != "abc" {
		t.Errorf("age = %+v, want what was typed and a parse error", got)
	}
	if got := f.Field("rating").Error; got != "Enter a number." {
		t.Errorf("rating error = %q", got)
	}
	if got := f.Value().Age; got != 0 {
		t.Errorf("Value().Age = %d, want 0 when it doesn't parse", got)
	}
}

func TestSubmit(t *testing.T) {
	f := form.New(signup{})
	if _, ok := f.Submit(map[string]string{"age": "40"}); ok {
		t.Fatal("Submit with no name succeeded")
	}
	if got := f.Field("name").Error; got != "Enter a name." {
		t.Errorf("after a failed submit every error shows; name error = %q", got)
	}
	in, ok := f.Submit(map[string]string{"name": "Ada", "age": "40", "agree": "true", "rating": "4.5"})
	if !ok {
		t.Fatalf("Submit failed: %+v", f.Field("name"))
	}
	if in.Name != "Ada" || in.Age != 40 || !in.Agree || in.Rating != 4.5 {
		t.Errorf("input = %+v", in)
	}
	f.AddError(&in.Name, "That name is taken.")
	if got := f.Field("name").Error; got != "That name is taken." {
		t.Errorf("AddError: name error = %q", got)
	}
}

func TestBoolsMissingFromThePayloadAreFalse(t *testing.T) {
	f := form.New(signup{Name: "x", Age: 20, Agree: true, Members: map[string]bool{"sam": true, "ali": true}})
	f.Change(map[string]string{"members[ali]": "on"})
	v := f.Value()
	if v.Agree {
		t.Error("agree should be false: an unchecked checkbox isn't sent")
	}
	if v.Members["sam"] || !v.Members["ali"] {
		t.Errorf("members = %v, want only ali", v.Members)
	}
}

func TestMapFields(t *testing.T) {
	f := form.New(signup{Name: "x", Age: 20, Shares: map[string]string{"sam": "1"}})
	f.Change(map[string]string{"shares[sam]": "", "shares[ali]": "2.50"})
	v := f.Value()
	if v.Shares["sam"] != "" || v.Shares["ali"] != "2.50" {
		t.Errorf("shares = %v", v.Shares)
	}
	if got := f.Entries("shares").Key("sam"); got.Name != "shares[sam]" || got.Error != "Enter a share." {
		t.Errorf("shares[sam] = %+v", got)
	}
}

func TestFormsDontShareState(t *testing.T) {
	proto := form.New(signup{Name: "x"})
	a, b := proto, proto
	a.Change(map[string]string{"name": "changed"})
	if got := b.Field("name").Value; got != "x" {
		t.Errorf("a copy saw another's change: %q", got)
	}
}

func TestZeroForm(t *testing.T) {
	var f form.Form[signup]
	if got := f.Field("agree").Value; got != "false" {
		t.Errorf("agree = %q, want false", got)
	}
	f.Change(map[string]string{"name": "Bo"})
	if got := f.Value().Name; got != "Bo" {
		t.Errorf("name = %q", got)
	}
}

type badField struct{ When struct{} }

type valueValidate struct{ A string }

func (valueValidate) Validate(e *form.Errors) {}

type wrongValidate struct{ A string }

func (*wrongValidate) Validate() error { return nil }

type addsOther struct{ A string }

func (in *addsOther) Validate(e *form.Errors) {
	var other string
	e.Add(&other, "no")
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		code string
		run  func()
	}{
		{"FM801 field type", form.FM801, func() { form.New(badField{}) }},
		{"FM801 not a struct", form.FM801, func() { form.New("x") }},
		{"FM802 value receiver", form.FM802, func() { form.New(valueValidate{}) }},
		{"FM802 signature", form.FM802, func() { form.New(wrongValidate{}) }},
		{"FM803 not a field", form.FM803, func() { form.New(addsOther{}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				var fe *form.Error
				err, _ := recover().(error)
				if !errors.As(err, &fe) || fe.Code != tc.code {
					t.Fatalf("panic = %v, want %s", err, tc.code)
				}
				if fe.Fix == "" {
					t.Error("no fix")
				}
			}()
			tc.run()
		})
	}
	if err := (form.Form[badField]{}).CheckInput(); err == nil {
		t.Error("CheckInput passed a bad input")
	}
}

// TestCodesDocumented keeps docs/errors.md in sync with the FM codes.
func TestCodesDocumented(t *testing.T) {
	data, err := os.ReadFile("../docs/errors.md")
	if err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^### (FM\d{3})$`).FindAllStringSubmatch(string(data), -1) {
		documented[m[1]] = true
	}
	src, err := os.ReadFile("errors.go")
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\t(FM\d{3}) = "FM\d{3}"`).FindAllStringSubmatch(string(src), -1) {
		known[m[1]] = true
		if !documented[m[1]] {
			t.Errorf("%s is not documented in docs/errors.md", m[1])
		}
	}
	for c := range documented {
		if !known[c] {
			t.Errorf("docs/errors.md documents %s, which errors.go doesn't define", c)
		}
	}
}

// TestDevTools checks a form in assigns as dev tools see it: loom
// inspect's JSON, and loom render's assigns from JSON.
func TestDevTools(t *testing.T) {
	type assigns struct{ Signup form.Form[signup] }
	f := form.New(signup{Name: "Ada", Age: 20})
	f.Submit(map[string]string{"name": "", "age": "20"})
	got := render.AssignsJSON(assigns{Signup: f}).(map[string]any)["signup"].(map[string]any)["name"]
	if want := map[string]any{"value": "", "error": "Enter a name."}; !reflect.DeepEqual(got, want) {
		t.Errorf("AssignsJSON name = %v, want %v", got, want)
	}
	var a assigns
	if err := render.DecodeAssigns([]byte(`{"signup": {"name": {"value": "Bo", "error": "Taken."}}}`), &a); err != nil {
		t.Fatal(err)
	}
	if got := a.Signup.Field("name"); got != (form.Field{Name: "name", Value: "Bo", Error: "Taken."}) {
		t.Errorf("decoded name = %+v", got)
	}
	if err := render.DecodeAssigns([]byte(`{"signup": "x"}`), &a); err == nil || !strings.Contains(err.Error(), "signup:") {
		t.Errorf("err = %v, want one naming signup", err)
	}
}

// TestSecretFields checks that dev tools never show a field tagged
// loom:"secret", in a form or in plain assigns, only whether it is set.
func TestSecretFields(t *testing.T) {
	type login struct {
		Email    string
		Password string `loom:"secret"`
	}
	type assigns struct {
		Login form.Form[login]
		Token string `loom:"secret"`
		Empty string `loom:"secret"`
	}
	f := form.New(login{})
	f.Submit(map[string]string{"email": "ada@example.com", "password": "hunter2"})
	got := render.AssignsJSON(assigns{Login: f, Token: "t0k"}).(map[string]any)
	fields := got["login"].(map[string]any)
	if v := fields["password"].(map[string]any)["value"]; v != render.Hidden {
		t.Errorf("password = %v, want %q", v, render.Hidden)
	}
	if v := fields["email"].(map[string]any)["value"]; v != "ada@example.com" {
		t.Errorf("email = %v", v)
	}
	if got["token"] != render.Hidden || got["empty"] != "" {
		t.Errorf("token = %v, empty = %v; want %q and \"\"", got["token"], got["empty"], render.Hidden)
	}
}
