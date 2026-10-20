// Command govulncheckpass calls only the unaffected symbol of vulnfixture, so
// govulncheck must report no reachable vulnerability (exit 0).
package main

import (
	"fmt"

	"example.invalid/vulnfixture"
)

func main() {
	fmt.Println(vulnfixture.Good())
}
