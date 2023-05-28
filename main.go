// package main holds the implementation of the cloud-routing template.
package main

import (
	"log"
)

func main() {
	// Alternatively, we can still use the legacy solver:
	// err := run.Run(solver)

	err := NoUnassignedRun(buildSolver)
	if err != nil {
		log.Fatal(err)
	}
}
