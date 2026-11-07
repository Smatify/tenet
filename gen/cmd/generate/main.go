// Command generate writes reflection-free entity accessors for the packages
// in the given directories (default "."). Use it from go generate:
//
//	//go:generate go run github.com/smatify/tenet/gen/cmd/generate
package main

import (
	"fmt"
	"os"

	"github.com/smatify/tenet/gen"
)

func main() {
	dirs := os.Args[1:]
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	if err := run(dirs); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(dirs []string) error {
	for _, d := range dirs {
		out, err := gen.Generate(d)
		if err != nil {
			return err
		}
		if out != "" {
			fmt.Println("generated", out)
		}
	}
	return nil
}
