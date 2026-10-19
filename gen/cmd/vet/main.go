// Command vet runs the lazy loading analyzer:
//
//	go run github.com/smatify/smorm/gen/cmd/vet ./...
//	go vet -vettool=$(which vet) ./...
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/smatify/smorm/gen/vet"
)

func main() { singlechecker.Main(vet.Analyzer) }
