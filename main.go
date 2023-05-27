// package main holds the implementation of the cloud-routing template.
package main

import (
	"log"
)

func main() {
	err := NoUnassignedRun(buildSolver)
	if err != nil {
		log.Fatal(err)
	}
}
