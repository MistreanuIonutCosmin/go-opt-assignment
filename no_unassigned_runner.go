package main

import (
	"context"
	"log"

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
		if input.Defaults.Configs == nil ||
			input.Defaults.Configs.AutomaticExtendHw == nil ||
			!*input.Defaults.Configs.AutomaticExtendHw {
			// We should do a plain run, nothing fancy.
			solver, _, err := solverBuilder(input, option)
			if err != nil {
				return err
			}

			for solution := range solver.All(ctx) {
				solutions <- solution
			}
			log.Println("Hard window extensions is disabled. Will not rerun if there are unassigned stops.")

			return nil
		}

		maxRetries := 10
		if input.Defaults.Configs.MaxUnassignedExpansion != nil {
			maxRetries = *input.Defaults.Configs.MaxUnassignedExpansion
		}

		// [NIT]: We can use this snippet, in case we want all solutions
		// to be optimized according to the criterion.
		// By default, we consider only solver.Last().
		// In this case, the solver should be run until all solutions
		// satisfy the expansion criterion.
		// for solution := range solver.All(ctx) {
		// 	solutions <- solution
		// }

		var unassignedCount int
		var solver store.Solver
		var last store.Solution
		retryCount := 0
		// Rebuild the solver with adjusted router input,
		// in case the criterion isn't met.
		for optimize := true; optimize; optimize, retryCount = (unassignedCount > 0), retryCount+1 {
			var router route.Router
			var err error
			solver, router, err = solverBuilder(input, option)
			if err != nil {
				return err
			}

			last = solver.Last(ctx)
			plan := router.Plan()
			unassignedCount = len(plan.Get(last.Store).Unassigned)

			if retryCount > maxRetries {
				log.Println("Couldn't find a complete solution in max_unassigned_expansion retries.")
				log.Println("Will output the last solution")
				break
			}
		}
		if retryCount <= maxRetries {
			log.Printf("Found a complete solution in %d retries.", retryCount-1)
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
