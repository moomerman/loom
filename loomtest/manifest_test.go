package loomtest

import "testing"

func TestManifestDiff(t *testing.T) {
	want := "routes\n  /\n    auth: none\njobs\n"
	got := "routes\n  /\n    auth: requireUser\n  /about\njobs\n  cron\n"
	if d := manifestDiff(want, got); d != "-     auth: none\n+     auth: requireUser\n+   /about\n+   cron\n" {
		t.Errorf("diff:\n%s", d)
	}
}
