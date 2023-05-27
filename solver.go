package main

import (
	"log"
	"time"

	"github.com/nextmv-io/sdk/model"
	"github.com/nextmv-io/sdk/route"
	"github.com/nextmv-io/sdk/store"
)

func buildOptions(i input, opts store.Options) store.Options {
	// You can also fix solver options like the expansion limit below.
	opts.Diagram.Expansion.Limit = 1
	options := i.applySolveOptions(opts)

	// If the duration limit is unset, we set it to 10s. You can configure
	// longer solver run times here. For local runs there is no time limitation.
	// If you want to make cloud runs for longer than 5 minutes, please contact:
	// support@nextmv.io
	if options.Limits.Duration == 0 {
		options.Limits.Duration = 10 * time.Second
	}

	return options
}

func initRouter(i input, opts store.Options) (route.Router, *routerInput, *store.Options, error) {
	// In case you directly expose the solver to untrusted, external input,
	// it is advisable from a security point of view to add strong
	// input validations before passing the data to the solver.

	err := i.prepareInputData()
	if err != nil {
		return nil, nil, nil, err
	}

	routerInput := i.transform()
	// fmt.Println(*i.Defaults.Configs.AutomaticExtendHw)

	timeMeasures, err := buildTravelTimeMeasures(
		routerInput.Velocities,
		i.Vehicles,
		i.Stops,
		routerInput.Starts,
		routerInput.Ends,
		i.durationGroupsDomains,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	p := planData{
		earlinessPenalties: routerInput.EarlinessPenalty,
		latenessPenalties:  routerInput.LatenessPenalty,
		targetTimes:        routerInput.TargetTimes,
		penalties:          routerInput.Penalties,
		initCosts:          routerInput.InitializationCosts,
	}
	v := vehicleData{}

	// Define base router.
	router, err := route.NewRouter(
		routerInput.Stops,
		routerInput.Vehicles,
		route.FilterWithRoute(maxStopFilter(&routerInput)),
		route.Velocities(routerInput.Velocities),
		route.Unassigned(routerInput.Penalties),
		route.InitializationCosts(routerInput.InitializationCosts),
		route.ValueFunctionMeasures(timeMeasures),
		route.Update(v, p),
		route.LimitDurations(
			routerInput.DurationLimits,
			true, /*ignoreTriangular*/
		),
		route.LimitDistances(
			routerInput.DistanceLimits,
			true, /*ignoreTriangular*/
		),
	)
	if err != nil {
		return nil, nil, nil, err
	}

	// TODO: Coalesce into a switch, maybe more readable?
	if len(routerInput.Starts) > 0 {
		err = router.Options(route.Starts(routerInput.Starts))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Ends) > 0 {
		err = router.Options(route.Ends(routerInput.Ends))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Windows) > 0 {
		err = router.Options(route.Windows(routerInput.Windows))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Shifts) > 0 {
		err = router.Options(route.Shifts(routerInput.Shifts))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Backlogs) > 0 {
		err = router.Options(route.Backlogs(routerInput.Backlogs))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.StopAttributes) > 0 &&
		len(routerInput.VehicleAttributes) > 0 {
		err = router.Options(
			route.Attribute(
				routerInput.VehicleAttributes,
				routerInput.StopAttributes,
			),
		)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Groups) > 0 {
		err = router.Options(route.Grouper(routerInput.Groups))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.AlternateStops) > 0 {
		err = router.Options(route.Alternates(routerInput.AlternateStops))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.ServiceGroups) > 0 {
		err = router.Options(route.ServiceGroups(routerInput.ServiceGroups))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.Precedences) > 0 {
		err = router.Options(route.Precedence(routerInput.Precedences))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	if len(routerInput.ServiceTimes) > 0 {
		err = router.Options(route.Services(routerInput.ServiceTimes))
		if err != nil {
			return nil, nil, nil, err
		}
	}
	for kind := range routerInput.Kinds {
		err = router.Options(
			route.Capacity(
				routerInput.Quantities[kind],
				routerInput.Capacities[kind],
			),
		)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	options := buildOptions(i, opts)

	return router, &routerInput, &options, nil
}

// The solver builder takes care of adapting the router options each
// time a run is triggered. Initially, the solver will run as is, without
// any changes. On subsequent runs, we'll execute the strategy for changing
// the router options.
// TODO: Create strategy interface for multiple approaches. At the moment,
// we only support a dummy window enlarge. This could be passed to the builder,
// in order to avoid convoluting logic as we do now.
type DynamicSolverBuilder func(i input, opts store.Options) (store.Solver, route.Router, error)

// This is a wrapper around the function that actually returns the
// solver and router required by the runner. If using the default runner,
// the router is optional and hence `solver` can be passed directly, without
// using the dynamic solver.
// However, the router is necessary for checking the solution output,
// in the custom runner in order to rerun the solver dynamically.
var buildSolver = func() DynamicSolverBuilder {
	var router *route.Router
	var routerInput *routerInput
	var routerOptions *store.Options

	// The retry count will determine the change we have to apply
	// to the input windows for the current run.
	retryCount := 0
	windowStep := []time.Duration{-1 * time.Minute, 1 * time.Minute}

	return func(i input, opts store.Options) (store.Solver, route.Router, error) {
		if router == nil {
			// First run will require the router to be initialized and
			// the router input populated.
			// Similar to a singleton.
			initialRouter, initialInput, initialOptions,
				err := initRouter(i, opts)
			if err != nil {
				return nil, nil, err
			}
			router = &initialRouter
			routerInput = initialInput
			routerOptions = initialOptions

			log.Println(routerInput.Windows)
		} else if retryCount > 0 &&
			i.Defaults.Configs.AutomaticExtendHw == nil || !*i.Defaults.Configs.AutomaticExtendHw {
			windowStep[0], windowStep[1] = windowStep[0]*2, windowStep[1]*2

			// TODO: Add adjusting logic here.
		}

		retryCount += 1
		solver, err := (*router).Solver(*routerOptions)
		return solver, *router, err
	}
}

// solver takes the input and solver options and constructs a routing solver.
// All route features/options depend on the input format. Depending on your
// goal you can add, delete or fix options or add more input validations. Please
// see the [route package
// documentation](https://pkg.go.dev/github.com/nextmv-io/sdk/route) for further
// information on the options available to you.
var solver = func(i input, opts store.Options) (store.Solver, error) {
	router, _, options, err := initRouter(i, opts)
	if err != nil {
		return nil, err
	}

	return router.Solver(*options)
}

// planData implements the PlanUpdater interface.
type planData struct {
	earlinessPenalties []int
	latenessPenalties  []int
	targetTimes        []int
	penalties          []int
	initCosts          []float64
}

func (d planData) Update(
	s route.PartialPlan, _ []route.PartialVehicle,
) (route.PlanUpdater, int, bool) {
	// Prepare data to update the solution's value.
	newValue := 0
	for j, v := range s.Vehicles() {
		var totalEarliness, totalLateness int
		route := v.Route()
		etas := v.Times().EstimatedArrival
		etds := v.Times().EstimatedDeparture

		if len(route) > 2 {
			newValue += int(d.initCosts[j])
		}

		// The new solution value is the travel time with all waiting and
		// service times.
		newValue += etds[len(etds)-1] - etas[0]
		// Update individual (per stop) and total penalty costs
		for i, r := range route {
			// Calculate penalty at route index.
			target := d.targetTimes[r]
			if target >= 0 {
				if etas[i] < target && len(d.earlinessPenalties) > 0 {
					// Update total penalty.
					totalEarliness += (target - etas[i]) * d.earlinessPenalties[r]
				} else if etas[i] > target && len(d.latenessPenalties) > 0 {
					// Update total penalty.
					totalLateness += (etas[i] - target) * d.latenessPenalties[r]
				}
			}
		}
		newValue += totalEarliness + totalLateness
	}
	for _, u := range s.Unassigned().Slice() {
		newValue += d.penalties[u]
	}

	return d, newValue, true
}

// vehicleData implements the route.VehicleUpdater interface.
type vehicleData struct{}

func (v vehicleData) Update(
	s route.PartialVehicle,
) (route.VehicleUpdater, int, bool) {
	return v, s.Value(), false
}

func (i *input) prepareInputData() error {
	i.Stops = append(i.Stops, i.AlternateStops...)
	i.applyVehicleDefaults()
	i.applyStopDefaults()
	// Handle dynamic fields
	err := i.handleDynamics()
	if err != nil {
		return err
	}
	i.autoConfigureUnassigned()
	err = i.makeDurationGroups()
	if err != nil {
		return err
	}
	return nil
}

// maxStopFilter ensures that the MaxStops constraint
// is applied correctly.
func maxStopFilter(r *routerInput) func(
	vehicleCandidates,
	locations model.Domain,
	routes [][]int,
) model.Domain {
	return func(
		vehicleCandidates,
		locations model.Domain,
		routes [][]int,
	) model.Domain {
		vehiclesToRemove := model.NewDomain()
		locationCount := locations.Len()
		// Determine vehicles which can get the set of locations assigned
		iter := vehicleCandidates.Iterator()
		for iter.Next() {
			index := iter.Value()
			if r.MaxStops[index] >= 0 &&
				len(routes[index])-2+locationCount > r.MaxStops[index] {
				vehiclesToRemove = vehiclesToRemove.Add(index)
			}
		}
		return vehiclesToRemove
	}
}
