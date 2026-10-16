package e2e

import (
	"context"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// TestJobsDashboard is a smoke test of jobsweb as the todo app mounts it:
// an admin opens the overview, which lists this node as the leader, a job
// is enqueued, and the page shows it complete without reloading, then
// lists it on the jobs page.
func TestJobsDashboard(t *testing.T) {
	app := todoApp(t)
	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)
	ctx := browser(t)

	// The default queue's "Done/min" cell. It may not start at 0: in the
	// first minutes of an hour, the built-in hourly prune runs on start.
	done := `document.querySelector('.queues tr[data-loom-key="default"] td.done')`
	var before string
	err := run(ctx,
		chromedp.Navigate(srv.URL+"/about"),
		settled(`document.cookie = "user=Admin; path=/"; true`, nil),
		chromedp.Navigate(srv.URL+"/admin/jobs"),
		// The page may load twice (a session the server ended reloads it), so
		// these steps wait across loads.
		waitJS(`document.querySelector('#loom-root.loom-connected .queues tr[data-loom-key="default"]') !== null`),
		// The Nodes panel lists this node, up and, once it has claimed
		// leadership, with the leader badge: a heartbeat re-renders it.
		waitFor(`#loom-root.loom-connected tr.node-row.up .badge`),
		chromedp.Sleep(200*time.Millisecond), // let a startup prune finish
		settled(done+`.textContent`, &before),
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := app.JobRunner().EnqueueJSON(ctx, "todo.PruneCompleted", []byte(`{}`))
			return err
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			return waitJS(done + `.textContent === "` + next(before) + `"`).Do(ctx)
		}),
		chromedp.Navigate(srv.URL+"/admin/jobs/jobs"),
		waitFor(`#loom-root.loom-connected .jobs .state.completed`),
	)
	if err != nil {
		t.Fatal(err)
	}
}

// settled evaluates js, trying again while the page is between documents
// (Chrome answers "Cannot find context with specified id" then).
func settled(js string, out any) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for {
			err := chromedp.Evaluate(js, out).Do(ctx)
			if err == nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return err
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
}

// next is the count after n.
func next(n string) string {
	i, _ := strconv.Atoi(n)
	return strconv.Itoa(i + 1)
}
