package loomtest_test

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/internal/hxtest"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
)

// fatalT records the first Fatal and stops the goroutine, as testing.T does.
type fatalT struct {
	testing.TB
	fatal string
}

func (f *fatalT) Helper() {}
func (f *fatalT) Fatal(args ...any) {
	f.fatal = fmt.Sprint(args...)
	runtime.Goexit()
}
func (f *fatalT) Fatalf(format string, args ...any) {
	f.fatal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// fatal runs f as a test and returns its Fatal message, or "".
func fatal(f func(t testing.TB)) string {
	ft := &fatalT{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(ft)
	}()
	<-done
	return ft.fatal
}

// A queue with no workers fails app.Run, so it fails Mount too, rather
// than passing every test and failing the deploy.
func TestMountFailsWhereRunWould(t *testing.T) {
	app := loom.New()
	app.Live("/", &welcomePage{}, hxtest.Badge)
	app.Jobs(sendWelcome{}) // queue "mail", never configured
	msg := fatal(func(t testing.TB) { loomtest.Mount(t, app, "/") })
	if !strings.Contains(msg, "the app would fail to start") || !strings.Contains(msg, "JB510") {
		t.Fatalf("Mount: %q; want JB510", msg)
	}
	app.Queues(jobs.Queue{Name: "mail", Concurrency: 1})
	loomtest.Validate(t, app)
}
