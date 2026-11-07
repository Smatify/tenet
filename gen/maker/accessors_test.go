package maker

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smatify/tenet/gen"
)

func TestAccessorsForHandWrittenFields(t *testing.T) {
	dir := tempModule(t)
	entDir := filepath.Join(dir, "entity")
	if err := os.MkdirAll(entDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := `package entity

import "github.com/smatify/tenet"

type Team struct {
	tenet.Entity
	tenet.UUIDID
	title   string                    ` + "`tenet:\"length:50\"`" + `
	players tenet.Collection[Player]  ` + "`tenet:\"oneToMany,mappedBy:team\"`" + `
	cache   string                    ` + "`tenet:\"-\"`" + `
	members tenet.Collection[Player]  ` + "`tenet:\"manyToMany\"`" + `
}

// Title is written by hand and kept; so are the members, whose getter
// exists (no SetMembers is added).
func (t *Team) Members() []*Player { tenet.Touch(t); return t.members.All() }

// Title is written by hand and kept.
func (t *Team) Title() string { tenet.Touch(t); return "team " + t.title }

type Player struct {
	tenet.Entity
	tenet.UUIDID
	team    *Team ` + "`tenet:\"manyToOne,inversedBy:players\"`" + `
	version int32 ` + "`tenet:\"version\"`" + `
	tags    tenet.Collection[Tag] ` + "`tenet:\"manyToMany\"`" + `
}

type Tag struct {
	tenet.Entity
	tenet.UUIDID
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
		"import (\n\t\"slices\"\n\n\t\"github.com/smatify/tenet\"\n)"} {
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

import "github.com/smatify/tenet"

type Game struct {
	tenet.Entity
	tenet.UUIDID
	halves tenet.Collection[Half] ` + "`tenet:\"oneToMany,mappedBy:game\"`" + `
}

type Half struct {
	tenet.Entity
	tenet.UUIDID
	game  *Game ` + "`tenet:\"manyToOne,inversedBy:halves\"`" + `
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
