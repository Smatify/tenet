package mapping

import (
	"slices"
	"strings"

	"github.com/smatify/smorm/schema"
)

// BuildSchema returns the database schema the entities map to, including
// join tables and identifier sequences.
func BuildSchema(entities []*Entity) *schema.Schema {
	s := schema.New()
	sorted := slices.Clone(entities)
	slices.SortFunc(sorted, func(a, b *Entity) int { return strings.Compare(a.Table, b.Table) })
	for _, e := range sorted {
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
			if f.ID {
				pk = append(pk, f.Column)
				c.AutoIncrement = e.Generator == GenIdentity && len(e.ID) == 1
			}
		}
		t.SetPrimaryKey(pk...)
		for _, d := range e.Indexes {
			t.Indexes = append(t.Indexes, &schema.Index{Name: d.Name, Columns: slices.Clone(d.Columns), Unique: d.Unique})
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

// coveredByIndex reports whether an existing index or the primary key starts with col.
func coveredByIndex(t *schema.Table, col string) bool {
	if t.PrimaryKey != nil && strings.EqualFold(t.PrimaryKey.Columns[0], col) {
		return true
	}
	for _, i := range t.Indexes {
		if strings.EqualFold(i.Columns[0], col) {
			return true
		}
	}
	return false
}
