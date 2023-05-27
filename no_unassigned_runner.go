package main

import (
	"context"
	"log"

	"github.com/nextmv-io/sdk/run"
	"github.com/nextmv-io/sdk/store"
)

// This is the same legacy CLI runner, but customized to run the solver
// until there area no unassigned stops. The strategy it implements to
// to have all stops assigned is delegated to the solver itself.
func NoUnassignedRun(builder func() DynamicSolverBuilder,
	options ...run.RunnerOption[run.CLIRunnerConfig, input, store.Options, store.Solution],
) error {
	solverBuilder := builder()
	algorithm := func(
		ctx context.Context,
		input input, option store.Options, solutions chan<- store.Solution,
	) error {
		solver, _, err := solverBuilder(input, option)
		if err != nil {
			return err
		}

		// for solution := range solver.All(ctx) {
		// 	solutions <- solution
		// }

		// This is where we decide whether we rerun or not.
		// TODO: We only care about the best solution?
		best := solver.Last(ctx)
		log.Println(best.Statistics.Time.Elapsed)
		solutions <- best
		return nil
	}

	runner := run.NewCLIRunner(algorithm, options...)
	return runner.Run(context.Background())
}
