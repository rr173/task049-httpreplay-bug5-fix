// Command task049-httpreplay runs the HTTP request record-and-replay mock
// server.
//
// Use --smoke-test to run the built-in self-check, which exits the process on
// completion.
package main

import (
	"flag"
	"fmt"
	"os"

	"task049-httpreplay/internal/selfcheck"
)

func main() {
	smoke := flag.Bool("smoke-test", false, "run the built-in self-check and exit")
	flag.Parse()

	if *smoke {
		if err := selfcheck.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "smoke-test FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("smoke-test PASSED")
		return
	}

	fmt.Println("HTTP request record-and-replay mock server; use --smoke-test to self-verify.")
}
