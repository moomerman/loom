package loom_test

import (
	"os"
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/data"
	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/loomtest"
	"github.com/moomerman/loom/pgjobs"
)

func TestConfigDB(t *testing.T) {
	app := loom.New(loom.Config{DB: "postgres://nobody@127.0.0.1:1/none"})
	if app.DB().SQL() == nil {
		t.Fatal("Config.DB didn't give the app a Postgres DB")
	}
	if err := app.Run("127.0.0.1:0"); !data.IsCode(err, data.DB601) {
		t.Fatalf("Run with no database listening: %v, want DB601", err)
	}

	if _, ok := loom.New().JobRunner().Store().(*jobs.MemoryStore); !ok {
		t.Error("an app without a database should keep jobs in memory")
	}
	// A test transaction can't host the Postgres store's LISTEN.
	if url := os.Getenv("LOOM_TEST_DATABASE_URL"); url != "" {
		in := loom.New()
		in.UseDB(loomtest.DB(t))
		if _, ok := in.JobRunner().Store().(*jobs.MemoryStore); !ok {
			t.Error("an app on loomtest.DB should keep jobs in memory")
		}
		pg := loom.New(loom.Config{DB: url})
		if _, ok := pg.JobRunner().Store().(*pgjobs.Store); !ok {
			t.Error("an app with a Postgres database should store jobs in it")
		}
		pg.DB().Close()
	}
}

func TestBroadcast(t *testing.T) {
	app := loom.New()
	var got []loom.Message
	app.PubSub().Subscribe("todos:ada", func(m loom.Message) { got = append(got, m) })
	if err := app.Broadcast("todos:ada", "changed", nil); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Event != "changed" {
		t.Fatalf("got %v", got)
	}
}
