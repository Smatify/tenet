package maker

import (
	"context"
	"errors"

	"github.com/smatify/smorm/console"
)

func makeEntity() console.Command {
	return console.Command{
		Name:        "make:entity",
		Usage:       "[EntityName]",
		Description: "Create or update an entity class",
		Run: func(context.Context, *console.Ctx) error {
			return errors.New("make:entity is not implemented yet")
		},
	}
}
