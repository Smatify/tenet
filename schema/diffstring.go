package schema

import "strings"

// String renders the diff as a human readable list of changes.
func (d *Diff) String() string {
	var b strings.Builder
	for _, t := range d.CreateTables {
		b.WriteString("create table " + t.Name + "\n")
	}
	for _, t := range d.DropTables {
		b.WriteString("drop table " + t.Name + "\n")
	}
	for _, s := range d.CreateSequences {
		b.WriteString("create sequence " + s.Name + "\n")
	}
	for _, s := range d.DropSequences {
		b.WriteString("drop sequence " + s.Name + "\n")
	}
	for _, td := range d.AlterTables {
		b.WriteString("alter table " + td.To.Name + "\n")
		for _, c := range td.AddColumns {
			b.WriteString("  add column " + c.Name + "\n")
		}
		for _, c := range td.DropColumns {
			b.WriteString("  drop column " + c.Name + "\n")
		}
		for _, c := range td.ChangeColumns {
			b.WriteString("  change column " + c.To.Name + ":")
			if c.Type {
				b.WriteString(" type(" + string(c.From.Type) + c.From.RawType + "->" + string(c.To.Type) + ")")
			}
			if c.Nullable {
				b.WriteString(" nullable")
			}
			if c.Default {
				from, to := "<nil>", "<nil>"
				if c.From.Default != nil {
					from = *c.From.Default
				}
				if c.To.Default != nil {
					to = *c.To.Default
				}
				b.WriteString(" default(" + from + " -> " + to + ")")
			}
			if c.AutoIncrement {
				b.WriteString(" autoincrement")
			}
			if c.Comment {
				b.WriteString(" comment(" + c.From.Comment + " -> " + c.To.Comment + ")")
			}
			b.WriteString("\n")
		}
		for _, i := range td.AddIndexes {
			b.WriteString("  add index " + i.Name + " (" + strings.Join(i.Columns, ",") + ") where " + i.Where + "\n")
		}
		for _, i := range td.DropIndexes {
			b.WriteString("  drop index " + i.Name + " (" + strings.Join(i.Columns, ",") + ") where " + i.Where + "\n")
		}
		for _, f := range td.AddForeignKeys {
			b.WriteString("  add fk " + f.Name + " " + string(f.OnDelete) + "/" + string(f.OnUpdate) + "\n")
		}
		for _, f := range td.DropForeignKeys {
			b.WriteString("  drop fk " + f.Name + " " + string(f.OnDelete) + "/" + string(f.OnUpdate) + "\n")
		}
		if td.PrimaryKeyChanged {
			b.WriteString("  primary key changed\n")
		}
		if td.CommentChanged {
			b.WriteString("  comment changed: " + td.From.Comment + " -> " + td.To.Comment + "\n")
		}
	}
	return b.String()
}
