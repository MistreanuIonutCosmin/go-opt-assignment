package main

import (
	"context"

	"github.com/nextmv-io/sdk/route"
	"github.com/nextmv-io/sdk/run"
	"github.com/nextmv-io/sdk/store"
)

// This is the same legacy CLI runner, but customized to run the solver
// until the number of unassigned stops falls below the threshold.
// The strategy it implements to do this is delegated to the solver itself,
// by calling it repetitively. This way this method can stay slim and we can
// easily substitute other solvers in the future.
func NoUnassignedRun(builder func() DynamicSolverBuilder,
	options ...run.RunnerOption[run.CLIRunnerConfig, input, store.Options, store.Solution],
) error {
	solverBuilder := builder()
	algorithm := func(
		ctx context.Context,
		input input, option store.Options, solutions chan<- store.Solution,
	) error {
		optimize_threshold := 0
		if input.Defaults.Configs.MaxUnassignedExpansion != nil {
			optimize_threshold = *input.Defaults.Configs.MaxUnassignedExpansion
		}

		// [NIT]: We can use this snippet, in case we want all solutions
		// to be optimized according to the criterion.
		// By default, we consider only solver.Last().
		// In this case, the solver should be run until all solutions
		// satisfy the expansion criterion.
		// for solution := range solver.All(ctx) {
		// 	solutions <- solution
		// }

		var unassigned_count int
		var solver store.Solver
		var last store.Solution
		for optimize := true; optimize; optimize = (unassigned_count > optimize_threshold) {
			// Rebuild the solver with adjusted router input,
			// in case the criterion isn't met.
			var router route.Router
			var err error
			solver, router, err = solverBuilder(input, option)
			if err != nil {
				return err
			}

			last = solver.Last(ctx)
			plan := router.Plan()
			unassigned_count = len(plan.Get(last.Store).Unassigned)
		}

		// Start pushing out the solutions, once we have one that
		// satisfies the criterion.
		solutions <- last
		for solution := range solver.All(ctx) {
			solutions <- solution
		}
		return nil
	}

	runner := run.NewCLIRunner(algorithm, options...)
	return runner.Run(context.Background())
}
