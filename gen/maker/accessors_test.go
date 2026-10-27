package maker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/smorm/gen"
)

func TestAccessorsForHandWrittenFields(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	if err := os.MkdirAll(entDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package entity

import "github.com/smatify/smorm"

type Team struct {
	smorm.Entity
	smorm.UUIDID
	title   string                    ` + "`smorm:\"length:50\"`" + `
	players smorm.Collection[Player]  ` + "`smorm:\"oneToMany,mappedBy:team\"`" + `
	cache   string                    ` + "`smorm:\"-\"`" + `
	members smorm.Collection[Player]  ` + "`smorm:\"manyToMany\"`" + `
}

// Title is written by hand and kept; so are the members, whose getter
// exists (no SetMembers is added).
func (t *Team) Members() []*Player { smorm.Touch(t); return t.members.All() }

// Title is written by hand and kept.
func (t *Team) Title() string { smorm.Touch(t); return "team " + t.title }

type Player struct {
	smorm.Entity
	smorm.UUIDID
	team    *Team ` + "`smorm:\"manyToOne,inversedBy:players\"`" + `
	version int32 ` + "`smorm:\"version\"`" + `
	tags    smorm.Collection[Tag] ` + "`smorm:\"manyToMany\"`" + `
}

type Tag struct {
	smorm.Entity
	smorm.UUIDID
}
`
	if err := os.WriteFile(filepath.Join(entDir, "team.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if only, err := (&Maker{Dir: entDir}).Accessors("Nope"); err != nil || len(only) != 0 {
		t.Fatalf("filtered = %v, %v", only, err)
	}
	added, err := (&Maker{Dir: entDir}).Accessors("Team", "Player")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Team.SetTitle", "Team.Players", "Team.AddPlayer", "Team.RemovePlayer", "Team.SetPlayers", "Player.Team", "Player.SetTeam",
		"Player.Tags", "Player.AddTag", "Player.RemoveTag", "Player.SetTags"}
	slices.Sort(added)
	slices.Sort(want)
	if !slices.Equal(added, want) {
		t.Fatalf("added = %v", added)
	}
	if _, err := gen.Generate(entDir); err != nil {
		t.Fatal(err)
	}
	goCmd(t, dir, "vet", "./...")
	out, _ := os.ReadFile(filepath.Join(entDir, "team.go"))
	for _, s := range []string{`return "team " + t.title`, "item.SetTeam(t)", "if item.Team() == t {", "\tp.tags.Remove(item)\n\treturn p",
		"import (\n\t\"slices\"\n\n\t\"github.com/smatify/smorm\"\n)"} {
		if !strings.Contains(string(out), s) {
			t.Errorf("team.go misses %q:\n%s", s, out)
		}
	}
	if again, err := (&Maker{Dir: entDir}).Accessors(); err != nil || len(again) != 0 {
		t.Fatalf("second run = %v, %v", again, err)
	}
}

// TestAccessorsOtherSide: completing only the side with the collection
// also adds the setter of the other side, so adding sets it.
func TestAccessorsOtherSide(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	if err := os.MkdirAll(entDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package entity

import "github.com/smatify/smorm"

type Game struct {
	smorm.Entity
	smorm.UUIDID
	halves smorm.Collection[Half] ` + "`smorm:\"oneToMany,mappedBy:game\"`" + `
}

type Half struct {
	smorm.Entity
	smorm.UUIDID
	game  *Game ` + "`smorm:\"manyToOne,inversedBy:halves\"`" + `
	score int32
}
`
	if err := os.WriteFile(filepath.Join(entDir, "game.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	added, err := (&Maker{Dir: entDir}).Accessors("Game")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Game.Halves", "Game.AddHalf", "Game.RemoveHalf", "Game.SetHalves", "Half.Game", "Half.SetGame"}
	slices.Sort(added)
	slices.Sort(want)
	if !slices.Equal(added, want) {
		t.Fatalf("added = %v", added)
	}
	out, _ := os.ReadFile(filepath.Join(entDir, "game.go"))
	if !strings.Contains(string(out), "item.SetGame(g)") {
		t.Errorf("AddHalf does not set the game:\n%s", out)
	}
}
