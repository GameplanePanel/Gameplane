// Command govulncheckfail calls the vulnerable symbol of vulnfixture, so
// govulncheck must report a reachable vulnerability (exit 3).
package main

import (
	"fmt"

	"example.invalid/vulnfixture"
)

func main() {
	fmt.Println(vulnfixture.Bad())
}
