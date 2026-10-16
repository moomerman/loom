package todo

import (
	"testing"

	"github.com/moomerman/loom"
	"github.com/moomerman/loom/loomtest"
)

// TestManifest keeps loom.manifest, the app's behaviour for review, in
// step with the code. It needs no database.
func TestManifest(t *testing.T) {
	app := loom.New()
	Setup(app)
	loomtest.Manifest(t, app)
}
