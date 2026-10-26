package mapping

import (
	"slices"
	"strings"

	"github.com/smatify/smorm/schema"
	"github.com/smatify/smorm/types"
)

// BuildSchema returns the database schema the entities map to, including
// join tables and identifier sequences.
func BuildSchema(entities []*Entity) *schema.Schema {
	s := schema.New()
	sorted := withHierarchies(entities)
	slices.SortFunc(sorted, func(a, b *Entity) int { return strings.Compare(a.Table, b.Table) })
	var subclasses []*Entity
	for _, e := range sorted {
		if e.Parent != nil {
			subclasses = append(subclasses, e)
			continue
		}
		if s.HasTable(e.Table) {
			continue
		}
		t := s.CreateTable(e.Table)
		t.Comment = e.Comment
		var pk []string
		for _, f := range e.Fields {
			c := t.AddColumn(f.Column, f.Type.Name())
			c.Options = f.Options
			c.Nullable = f.Nullable
			c.Default = f.Default
			c.Comment = f.Comment
			c.AutoIncrement = f.Generated
			if f.ID {
				pk = append(pk, f.Column)
				c.AutoIncrement = e.Generator == GenIdentity && len(e.ID) == 1
			}
		}
		if e.Inheritance != "" {
			t.AddColumn(e.DiscriminatorColumn, types.String).SetLength(255)
			t.AddIndex(e.DiscriminatorColumn)
		}
		t.SetPrimaryKey(pk...)
		for _, d := range e.Indexes {
			t.Indexes = append(t.Indexes, &schema.Index{Name: d.Name, Columns: slices.Clone(d.Columns), Unique: d.Unique, Where: d.Where})
		}
		for _, a := range e.Assocs {
			if !a.Owning || !a.Kind.IsToOne() {
				continue
			}
			jc := a.JoinColumns[0]
			fk := t.AddForeignKey([]string{jc.Name}, a.Target.Table, []string{jc.Referenced})
			fk.OnDelete = schema.Action(jc.OnDelete)
			if !coveredByIndex(t, jc.Name) {
				t.AddIndex(jc.Name)
			}
		}
		if e.Generator == GenSequence && s.Sequence(e.Sequence) == nil {
			s.CreateSequence(e.Sequence, 1, 1)
		}
	}
	slices.SortFunc(subclasses, func(a, b *Entity) int { return depth(a) - depth(b) })
	for _, e := range subclasses {
		addSubclass(s, e)
	}
	for _, e := range sorted {
		for _, a := range e.Assocs {
			if a.JoinTable == nil || s.HasTable(a.JoinTable.Name) {
				continue
			}
			jt := a.JoinTable
			t := s.CreateTable(jt.Name)
			src, tgt := e.SingleID(), a.Target.SingleID()
			for _, jc := range jt.JoinColumns {
				c := t.AddColumn(jc.Name, src.Type.Name())
				c.Options = src.Options
			}
			for _, jc := range jt.InverseJoinColumns {
				c := t.AddColumn(jc.Name, tgt.Type.Name())
				c.Options = tgt.Options
			}
			src0, tgt0 := jt.JoinColumns[0], jt.InverseJoinColumns[0]
			t.SetPrimaryKey(src0.Name, tgt0.Name)
			t.AddIndex(tgt0.Name)
			t.AddForeignKey([]string{src0.Name}, e.Table, []string{src0.Referenced}).SetOnDelete(schema.Cascade)
			t.AddForeignKey([]string{tgt0.Name}, a.Target.Table, []string{tgt0.Referenced}).SetOnDelete(schema.Cascade)
		}
	}
	return s
}

// coveredByIndex reports whether an existing index or the primary key starts with col;
// a partial index does not cover it (it holds some rows only).
func coveredByIndex(t *schema.Table, col string) bool {
	if t.PrimaryKey != nil && strings.EqualFold(t.PrimaryKey.Columns[0], col) {
		return true
	}
	for _, i := range t.Indexes {
		if i.Where == "" && strings.EqualFold(i.Columns[0], col) {
			return true
		}
	}
	return false
}

func depth(e *Entity) int {
	n := 0
	for p := e.Parent; p != nil; p = p.Parent {
		n++
	}
	return n
}

// withHierarchies adds the parents of subclasses (and roots' known
// subclasses) so hierarchies are complete.
func withHierarchies(entities []*Entity) []*Entity {
	out := slices.Clone(entities)
	for i := 0; i < len(out); i++ {
		for _, rel := range append([]*Entity{out[i].Parent}, out[i].Subclasses...) {
			if rel != nil && !slices.Contains(out, rel) {
				out = append(out, rel)
			}
		}
	}
	return out
}

// ownFields returns the fields a subclass declares itself.
func ownFields(e *Entity) []*Field {
	var out []*Field
	for _, f := range e.Fields {
		if e.Parent.FieldByColumn(f.Column) == nil {
			out = append(out, f)
		}
	}
	return out
}

func addSubclass(s *schema.Schema, e *Entity) {
	if e.Inheritance == SingleTable {
		t := s.Table(e.Table)
		for _, f := range ownFields(e) {
			if t.Column(f.Column) != nil {
				continue // the same column declared by a sibling class
			}
			c := t.AddColumn(f.Column, f.Type.Name())
			c.Options, c.Default, c.Comment = f.Options, f.Default, f.Comment
			c.Nullable = true
		}
		addOwnConstraints(t, e)
		return
	}
	t := s.CreateTable(e.Table)
	var pk []string
	for _, id := range e.ID {
		c := t.AddColumn(id.Column, id.Type.Name())
		c.Options = id.Options
		pk = append(pk, id.Column)
	}
	t.SetPrimaryKey(pk...)
	t.AddForeignKey(pk, e.Parent.Table, pk).SetOnDelete(schema.Cascade)
	for _, f := range ownFields(e) {
		c := t.AddColumn(f.Column, f.Type.Name())
		c.Options, c.Nullable, c.Default, c.Comment = f.Options, f.Nullable, f.Default, f.Comment
	}
	addOwnConstraints(t, e)
}

func addOwnConstraints(t *schema.Table, e *Entity) {
	for _, d := range e.Indexes {
		if t.Index(d.Name) == nil {
			t.Indexes = append(t.Indexes, &schema.Index{Name: d.Name, Columns: slices.Clone(d.Columns), Unique: d.Unique, Where: d.Where})
		}
	}
	for _, a := range e.Assocs {
		if a.Inherited || !a.Owning || !a.Kind.IsToOne() {
			continue
		}
		jc := a.JoinColumns[0]
		if t.ForeignKey(schema.IdentifierName("fk", t.Name, []string{jc.Name})) != nil {
			continue
		}
		fk := t.AddForeignKey([]string{jc.Name}, a.Target.Table, []string{jc.Referenced})
		fk.OnDelete = schema.Action(jc.OnDelete)
		if !coveredByIndex(t, jc.Name) {
			t.AddIndex(jc.Name)
		}
	}
}
