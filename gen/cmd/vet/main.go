// Command vet runs the analyzer (field access without Touch, unknown field
// names in queries):
//
//	go run github.com/smatify/tenet/gen/cmd/vet ./...
//	go vet -vettool=$(which vet) ./...
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/smatify/tenet/gen/vet"
)

func main() { singlechecker.Main(vet.Analyzer) }
