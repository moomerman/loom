package jobs_test

import (
	"testing"

	"github.com/moomerman/loom/jobs"
	"github.com/moomerman/loom/jobs/jobstest"
)

func TestMemoryConformance(t *testing.T) {
	jobstest.Run(t, func(t *testing.T, clock jobs.Clock) func() jobs.Store {
		s := jobs.NewMemoryStore(jobs.MemoryOptions{Clock: clock})
		return func() jobs.Store { return s }
	})
}
