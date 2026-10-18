package schema

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/smatify/smorm/dialect"
	"github.com/smatify/smorm/types"
)

// Queryer is the subset of dbal.IDB needed for introspection.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// Introspector reads the current schema from a database. Dialects outside
// this module can implement it on their Dialect type.
type Introspector interface {
	Introspect(ctx context.Context, q Queryer) (*Schema, error)
}

// Introspect reads every table and sequence of the current database schema.
func Introspect(ctx context.Context, q Queryer, d dialect.Dialect) (*Schema, error) {
	if in, ok := d.(Introspector); ok {
		return in.Introspect(ctx, q)
	}
	var in introspector
	switch d.Name() {
	case dialect.Postgres:
		in = pgIntrospector{}
	case dialect.MySQL, dialect.MariaDB:
		in = mysqlIntrospector{maria: d.Name() == dialect.MariaDB}
	case dialect.SQLite:
		in = sqliteIntrospector{}
	case dialect.MSSQL:
		in = mssqlIntrospector{}
	default:
		return nil, fmt.Errorf("schema: introspection not supported for dialect %q", d.Name())
	}
	b := &builder{s: New(), tables: map[string]*Table{}}
	if err := in.introspect(ctx, q, b); err != nil {
		return nil, err
	}
	for _, t := range b.s.Tables {
		slices.SortFunc(t.Indexes, func(a, b *Index) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(t.ForeignKeys, func(a, b *ForeignKey) int { return strings.Compare(a.Name, b.Name) })
	}
	return b.s, nil
}

type introspector interface {
	introspect(ctx context.Context, q Queryer, b *builder) error
}

// builder assembles a schema from row-oriented catalog queries.
type builder struct {
	s      *Schema
	tables map[string]*Table
}

func (b *builder) table(name string) *Table {
	t, ok := b.tables[name]
	if !ok {
		t = b.s.CreateTable(name)
		b.tables[name] = t
	}
	return t
}

func (b *builder) index(t *Table, name string, unique, primary bool, col, where string) {
	if primary {
		if t.PrimaryKey == nil {
			t.PrimaryKey = &Index{Name: name, Unique: true, Primary: true}
		}
		t.PrimaryKey.Columns = append(t.PrimaryKey.Columns, col)
		return
	}
	i := t.Index(name)
	if i == nil {
		i = &Index{Name: name, Unique: unique, Where: where}
		t.Indexes = append(t.Indexes, i)
	}
	i.Columns = append(i.Columns, col)
}

func (b *builder) foreignKey(t *Table, name, col, refTable, refCol string, onDelete, onUpdate Action) {
	fk := t.ForeignKey(name)
	if fk == nil {
		fk = &ForeignKey{Name: name, RefTable: refTable, OnDelete: onDelete, OnUpdate: onUpdate}
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}
	fk.Columns = append(fk.Columns, col)
	fk.RefColumns = append(fk.RefColumns, refCol)
}

func scanAll(ctx context.Context, q Queryer, query string, args []any, fn func(*sql.Rows) error) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("schema: introspection query failed: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func parseAction(s string) Action {
	switch a := Action(strings.ReplaceAll(strings.ToUpper(strings.TrimSpace(s)), "_", " ")); a {
	case Restrict, Cascade, SetNull, SetDefault:
		return a
	}
	return NoAction
}

var declRe = regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9 _]*?)\s*(?:\(\s*([0-9]+|MAX|max)\s*(?:,\s*([0-9]+)\s*)?\))?\s*$`)

// parseDecl splits "DECIMAL(10, 2)" into ("DECIMAL", 10, 2).
func parseDecl(decl string) (base string, a, b int, ok bool) {
	m := declRe.FindStringSubmatch(decl)
	if m == nil {
		return "", 0, 0, false
	}
	base = strings.ToUpper(m[1])
	if m[2] != "" && !strings.EqualFold(m[2], "max") {
		a, _ = strconv.Atoi(m[2])
	}
	if m[3] != "" {
		b, _ = strconv.Atoi(m[3])
	}
	return base, a, b, true
}

func nullInt(n sql.NullInt64) int {
	if n.Valid {
		return int(n.Int64)
	}
	return 0
}

func strPtr(n sql.NullString) *string {
	if !n.Valid {
		return nil
	}
	s := n.String
	return &s
}

// ---------------------------------------------------------------- Postgres

type pgIntrospector struct{}

func (pgIntrospector) introspect(ctx context.Context, q Queryer, b *builder) error {
	err := scanAll(ctx, q, `SELECT c.relname, COALESCE(obj_description(c.oid, 'pg_class'), '')
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p') ORDER BY c.relname`, nil, func(r *sql.Rows) error {
		var name, comment string
		if err := r.Scan(&name, &comment); err != nil {
			return err
		}
		b.table(name).Comment = comment
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT c.table_name, c.column_name, c.data_type, c.character_maximum_length,
			c.numeric_precision, c.numeric_scale, c.datetime_precision, c.is_nullable, c.column_default, c.is_identity,
			COALESCE(col_description(format('%I.%I', c.table_schema, c.table_name)::regclass::oid, c.ordinal_position), '')
		FROM information_schema.columns c JOIN information_schema.tables t
			ON t.table_schema = c.table_schema AND t.table_name = c.table_name AND t.table_type = 'BASE TABLE'
		WHERE c.table_schema = current_schema() ORDER BY c.table_name, c.ordinal_position`, nil, func(r *sql.Rows) error {
		var table, name, dataType, nullable, identity, comment string
		var length, prec, scale, dtPrec sql.NullInt64
		var def sql.NullString
		if err := r.Scan(&table, &name, &dataType, &length, &prec, &scale, &dtPrec, &nullable, &def, &identity, &comment); err != nil {
			return err
		}
		c := b.table(table).AddColumn(name, "")
		c.Nullable = nullable == "YES"
		c.Comment = comment
		c.AutoIncrement = identity == "YES"
		if def.Valid && strings.HasPrefix(def.String, "nextval(") {
			c.AutoIncrement = true
		} else {
			c.Default = strPtr(def)
		}
		switch dataType {
		case "character varying":
			c.Type, c.Options.Length = types.String, nullInt(length)
		case "character":
			c.Type, c.Options.Length, c.Options.Fixed = types.String, nullInt(length), true
		case "text":
			c.Type = types.Text
		case "smallint":
			c.Type = types.SmallInt
		case "integer":
			c.Type = types.Integer
		case "bigint":
			c.Type = types.BigInt
		case "boolean":
			c.Type = types.Boolean
		case "numeric":
			c.Type, c.Options.Precision, c.Options.Scale = types.Decimal, nullInt(prec), nullInt(scale)
		case "double precision", "real":
			c.Type = types.Float
		case "date":
			c.Type = types.Date
		case "time without time zone":
			c.Type = types.Time
		case "timestamp without time zone":
			c.Type, c.Options.Precision = types.DateTime, nullInt(dtPrec)
		case "timestamp with time zone":
			c.Type, c.Options.Precision = types.DateTimeTZ, nullInt(dtPrec)
		case "json", "jsonb":
			c.Type = types.JSON
		case "uuid":
			c.Type = types.UUID
		case "bytea":
			c.Type = types.Binary
		default:
			c.RawType = strings.ToUpper(dataType)
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT t.relname, i.relname, ix.indisunique, ix.indisprimary, a.attname,
			COALESCE(pg_get_expr(ix.indpred, ix.indrelid), '')
		FROM pg_index ix
		JOIN pg_class t ON t.oid = ix.indrelid
		JOIN pg_class i ON i.oid = ix.indexrelid
		JOIN pg_namespace ns ON ns.oid = t.relnamespace
		CROSS JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, n)
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE ns.nspname = current_schema() AND t.relkind IN ('r', 'p')
		ORDER BY t.relname, i.relname, k.n`, nil, func(r *sql.Rows) error {
		var table, name, col, where string
		var unique, primary bool
		if err := r.Scan(&table, &name, &unique, &primary, &col, &where); err != nil {
			return err
		}
		b.index(b.table(table), name, unique, primary, col, where)
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT con.conname, cl.relname, a.attname, ref.relname, ra.attname, con.confdeltype, con.confupdtype
		FROM pg_constraint con
		JOIN pg_class cl ON cl.oid = con.conrelid
		JOIN pg_class ref ON ref.oid = con.confrelid
		JOIN pg_namespace ns ON ns.oid = cl.relnamespace
		CROSS JOIN LATERAL unnest(con.conkey, con.confkey) WITH ORDINALITY AS k(attnum, refnum, n)
		JOIN pg_attribute a ON a.attrelid = con.conrelid AND a.attnum = k.attnum
		JOIN pg_attribute ra ON ra.attrelid = con.confrelid AND ra.attnum = k.refnum
		WHERE con.contype = 'f' AND ns.nspname = current_schema()
		ORDER BY cl.relname, con.conname, k.n`, nil, func(r *sql.Rows) error {
		var name, table, col, refTable, refCol, del, upd string
		if err := r.Scan(&name, &table, &col, &refTable, &refCol, &del, &upd); err != nil {
			return err
		}
		b.foreignKey(b.table(table), name, col, refTable, refCol, pgAction(del), pgAction(upd))
		return nil
	})
	if err != nil {
		return err
	}
	return scanAll(ctx, q, `SELECT s.relname, seq.seqstart, seq.seqincrement
		FROM pg_class s JOIN pg_sequence seq ON seq.seqrelid = s.oid JOIN pg_namespace ns ON ns.oid = s.relnamespace
		WHERE ns.nspname = current_schema()
		AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = s.oid AND d.deptype IN ('i', 'a'))
		ORDER BY s.relname`, nil, func(r *sql.Rows) error {
		var name string
		var start, inc int64
		if err := r.Scan(&name, &start, &inc); err != nil {
			return err
		}
		b.s.CreateSequence(name, start, inc)
		return nil
	})
}

func pgAction(c string) Action {
	switch c {
	case "r":
		return Restrict
	case "c":
		return Cascade
	case "n":
		return SetNull
	case "d":
		return SetDefault
	}
	return NoAction
}

// ---------------------------------------------------------------- MySQL / MariaDB

type mysqlIntrospector struct{ maria bool }

func (m mysqlIntrospector) introspect(ctx context.Context, q Queryer, b *builder) error {
	err := scanAll(ctx, q, `SELECT TABLE_NAME, TABLE_COMMENT FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = DATABASE() AND TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_NAME`, nil, func(r *sql.Rows) error {
		var name, comment string
		if err := r.Scan(&name, &comment); err != nil {
			return err
		}
		b.table(name).Comment = comment
		return nil
	})
	if err != nil {
		return err
	}
	jsonCols := map[string]bool{}
	if m.maria {
		err = scanAll(ctx, q, `SELECT TABLE_NAME, CONSTRAINT_NAME, CHECK_CLAUSE FROM information_schema.CHECK_CONSTRAINTS
			WHERE CONSTRAINT_SCHEMA = DATABASE()`, nil, func(r *sql.Rows) error {
			var table, name, clause string
			if err := r.Scan(&table, &name, &clause); err != nil {
				return err
			}
			if strings.Contains(strings.ToLower(clause), "json_valid") {
				jsonCols[table+"."+name] = true
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	err = scanAll(ctx, q, `SELECT c.TABLE_NAME, c.COLUMN_NAME, c.DATA_TYPE, c.COLUMN_TYPE, c.CHARACTER_MAXIMUM_LENGTH,
			c.NUMERIC_PRECISION, c.NUMERIC_SCALE, c.DATETIME_PRECISION, c.IS_NULLABLE, c.COLUMN_DEFAULT, c.EXTRA, c.COLUMN_COMMENT
		FROM information_schema.COLUMNS c JOIN information_schema.TABLES t
			ON t.TABLE_SCHEMA = c.TABLE_SCHEMA AND t.TABLE_NAME = c.TABLE_NAME AND t.TABLE_TYPE = 'BASE TABLE'
		WHERE c.TABLE_SCHEMA = DATABASE() ORDER BY c.TABLE_NAME, c.ORDINAL_POSITION`, nil, func(r *sql.Rows) error {
		var table, name, dataType, colType, nullable, extra, comment string
		var length, prec, scale, dtPrec sql.NullInt64
		var def sql.NullString
		if err := r.Scan(&table, &name, &dataType, &colType, &length, &prec, &scale, &dtPrec, &nullable, &def, &extra, &comment); err != nil {
			return err
		}
		c := b.table(table).AddColumn(name, "")
		c.Nullable = nullable == "YES"
		c.Comment = comment
		c.AutoIncrement = strings.Contains(strings.ToLower(extra), "auto_increment")
		dataType = strings.ToLower(dataType)
		isText := strings.Contains(dataType, "char") || strings.Contains(dataType, "text") || dataType == "enum" || dataType == "uuid"
		switch {
		case !def.Valid:
		case m.maria && def.String == "NULL":
		case !m.maria && isText && !strings.Contains(strings.ToLower(extra), "default_generated"):
			// MySQL reports string literals unquoted.
			v := "'" + strings.ReplaceAll(def.String, "'", "''") + "'"
			c.Default = &v
		default:
			c.Default = strPtr(def)
		}
		switch dataType {
		case "varchar":
			c.Type, c.Options.Length = types.String, nullInt(length)
		case "char":
			c.Type, c.Options.Length, c.Options.Fixed = types.String, nullInt(length), true
		case "tinytext", "text", "mediumtext", "longtext":
			c.Type = types.Text
			if jsonCols[table+"."+name] {
				c.Type = types.JSON
			}
		case "tinyint":
			if strings.HasPrefix(strings.ToLower(colType), "tinyint(1)") {
				c.Type = types.Boolean
			} else {
				c.RawType = strings.ToUpper(colType)
			}
		case "smallint":
			c.Type = types.SmallInt
		case "int":
			c.Type = types.Integer
		case "bigint":
			c.Type = types.BigInt
		case "decimal":
			c.Type, c.Options.Precision, c.Options.Scale = types.Decimal, nullInt(prec), nullInt(scale)
		case "double", "float":
			c.Type = types.Float
		case "date":
			c.Type = types.Date
		case "time":
			c.Type = types.Time
		case "datetime", "timestamp":
			c.Type, c.Options.Precision = types.DateTime, nullInt(dtPrec)
		case "json":
			c.Type = types.JSON
		case "uuid":
			c.Type = types.UUID
		case "longblob", "blob", "mediumblob", "tinyblob":
			c.Type = types.Binary
		case "varbinary":
			c.Type, c.Options.Length = types.Binary, nullInt(length)
		case "binary":
			c.Type, c.Options.Length, c.Options.Fixed = types.Binary, nullInt(length), true
		default:
			c.RawType = strings.ToUpper(colType)
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT kcu.CONSTRAINT_NAME, kcu.TABLE_NAME, kcu.COLUMN_NAME, kcu.REFERENCED_TABLE_NAME,
			kcu.REFERENCED_COLUMN_NAME, rc.DELETE_RULE, rc.UPDATE_RULE
		FROM information_schema.KEY_COLUMN_USAGE kcu
		JOIN information_schema.REFERENTIAL_CONSTRAINTS rc ON rc.CONSTRAINT_SCHEMA = kcu.CONSTRAINT_SCHEMA
			AND rc.CONSTRAINT_NAME = kcu.CONSTRAINT_NAME AND rc.TABLE_NAME = kcu.TABLE_NAME
		WHERE kcu.TABLE_SCHEMA = DATABASE() AND kcu.REFERENCED_TABLE_NAME IS NOT NULL
		ORDER BY kcu.TABLE_NAME, kcu.CONSTRAINT_NAME, kcu.ORDINAL_POSITION`, nil, func(r *sql.Rows) error {
		var name, table, col, refTable, refCol, del, upd string
		if err := r.Scan(&name, &table, &col, &refTable, &refCol, &del, &upd); err != nil {
			return err
		}
		b.foreignKey(b.table(table), name, col, refTable, refCol, parseAction(del), parseAction(upd))
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT TABLE_NAME, INDEX_NAME, NON_UNIQUE, COLUMN_NAME FROM information_schema.STATISTICS
		WHERE TABLE_SCHEMA = DATABASE() ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`, nil, func(r *sql.Rows) error {
		var table, name, col string
		var nonUnique int
		if err := r.Scan(&table, &name, &nonUnique, &col); err != nil {
			return err
		}
		t := b.table(table)
		if t.ForeignKey(name) != nil {
			return nil // implicit index InnoDB creates for a foreign key
		}
		if name == "PRIMARY" {
			b.index(t, IdentifierName("pk", table, nil), true, true, col, "")
			return nil
		}
		b.index(t, name, nonUnique == 0, false, col, "")
		return nil
	})
	if err != nil || !m.maria {
		return err
	}
	var seqs []string
	err = scanAll(ctx, q, `SELECT TABLE_NAME FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE()
		AND TABLE_TYPE = 'SEQUENCE' ORDER BY TABLE_NAME`, nil, func(r *sql.Rows) error {
		var name string
		if err := r.Scan(&name); err != nil {
			return err
		}
		seqs = append(seqs, name)
		return nil
	})
	if err != nil {
		return err
	}
	for _, name := range seqs {
		err := scanAll(ctx, q, "SELECT start_value, increment FROM `"+strings.ReplaceAll(name, "`", "``")+"`", nil, func(r *sql.Rows) error {
			var start, inc int64
			if err := r.Scan(&start, &inc); err != nil {
				return err
			}
			b.s.CreateSequence(name, start, inc)
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- SQLite

type sqliteIntrospector struct{}

var sqliteWhereRe = regexp.MustCompile(`(?is)\bWHERE\b(.*)$`)

func (sqliteIntrospector) introspect(ctx context.Context, q Queryer, b *builder) error {
	type tableInfo struct{ name, sql string }
	var tables []tableInfo
	err := scanAll(ctx, q, `SELECT name, sql FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`, nil,
		func(r *sql.Rows) error {
			var ti tableInfo
			if err := r.Scan(&ti.name, &ti.sql); err != nil {
				return err
			}
			tables = append(tables, ti)
			return nil
		})
	if err != nil {
		return err
	}
	for _, ti := range tables {
		t := b.table(ti.name)
		autoInc := strings.Contains(strings.ToUpper(ti.sql), "AUTOINCREMENT")
		var pkCols []struct {
			pos  int
			name string
		}
		err := scanAll(ctx, q, `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?) ORDER BY cid`, []any{ti.name},
			func(r *sql.Rows) error {
				var name, decl string
				var notNull, pk int
				var def sql.NullString
				if err := r.Scan(&name, &decl, &notNull, &def, &pk); err != nil {
					return err
				}
				c := t.AddColumn(name, "")
				c.Nullable = notNull == 0 && pk == 0
				c.Default = strPtr(def)
				sqliteType(c, decl)
				if pk > 0 {
					pkCols = append(pkCols, struct {
						pos  int
						name string
					}{pk, name})
				}
				return nil
			})
		if err != nil {
			return err
		}
		if len(pkCols) > 0 {
			slices.SortFunc(pkCols, func(a, b struct {
				pos  int
				name string
			}) int {
				return a.pos - b.pos
			})
			t.PrimaryKey = &Index{Name: IdentifierName("pk", t.Name, nil), Unique: true, Primary: true}
			for _, pc := range pkCols {
				t.PrimaryKey.Columns = append(t.PrimaryKey.Columns, pc.name)
			}
			if len(pkCols) == 1 && autoInc {
				t.Column(pkCols[0].name).AutoIncrement = true
			}
		}
		type idx struct {
			name    string
			unique  bool
			origin  string
			partial bool
		}
		var idxs []idx
		err = scanAll(ctx, q, `SELECT name, "unique", origin, partial FROM pragma_index_list(?)`, []any{ti.name}, func(r *sql.Rows) error {
			var i idx
			if err := r.Scan(&i.name, &i.unique, &i.origin, &i.partial); err != nil {
				return err
			}
			idxs = append(idxs, i)
			return nil
		})
		if err != nil {
			return err
		}
		for _, i := range idxs {
			if i.origin == "pk" {
				continue
			}
			where := ""
			if i.partial {
				err := scanAll(ctx, q, `SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, []any{i.name}, func(r *sql.Rows) error {
					var s string
					if err := r.Scan(&s); err != nil {
						return err
					}
					if m := sqliteWhereRe.FindStringSubmatch(s); m != nil {
						where = strings.TrimSpace(m[1])
					}
					return nil
				})
				if err != nil {
					return err
				}
			}
			err := scanAll(ctx, q, `SELECT name FROM pragma_index_info(?) ORDER BY seqno`, []any{i.name}, func(r *sql.Rows) error {
				var col string
				if err := r.Scan(&col); err != nil {
					return err
				}
				b.index(t, i.name, i.unique, false, col, where)
				return nil
			})
			if err != nil {
				return err
			}
		}
		type fkRow struct {
			id                           int
			refTable, from, to, upd, del string
		}
		var fks []fkRow
		err = scanAll(ctx, q, `SELECT id, "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?) ORDER BY id, seq`,
			[]any{ti.name}, func(r *sql.Rows) error {
				var f fkRow
				if err := r.Scan(&f.id, &f.refTable, &f.from, &f.to, &f.upd, &f.del); err != nil {
					return err
				}
				fks = append(fks, f)
				return nil
			})
		if err != nil {
			return err
		}
		// SQLite does not expose constraint names; rebuild the deterministic ones.
		byID := map[int][]fkRow{}
		var ids []int
		for _, f := range fks {
			if _, ok := byID[f.id]; !ok {
				ids = append(ids, f.id)
			}
			byID[f.id] = append(byID[f.id], f)
		}
		for _, id := range ids {
			rows := byID[id]
			var cols []string
			for _, f := range rows {
				cols = append(cols, f.from)
			}
			name := IdentifierName("fk", t.Name, cols)
			for _, f := range rows {
				b.foreignKey(t, name, f.from, f.refTable, f.to, parseAction(f.del), parseAction(f.upd))
			}
		}
	}
	return nil
}

func sqliteType(c *Column, decl string) {
	base, a, s, ok := parseDecl(decl)
	if !ok {
		c.RawType = decl
		return
	}
	switch base {
	case "VARCHAR":
		c.Type, c.Options.Length = types.String, a
	case "CHAR":
		c.Type, c.Options.Length, c.Options.Fixed = types.String, a, true
	case "TEXT":
		c.Type = types.Text
	case "SMALLINT":
		c.Type = types.SmallInt
	case "INTEGER", "INT":
		c.Type = types.Integer
	case "BIGINT":
		c.Type = types.BigInt
	case "BOOLEAN":
		c.Type = types.Boolean
	case "DECIMAL", "NUMERIC":
		c.Type, c.Options.Precision, c.Options.Scale = types.Decimal, a, s
	case "REAL", "DOUBLE", "FLOAT":
		c.Type = types.Float
	case "DATE":
		c.Type = types.Date
	case "TIME":
		c.Type = types.Time
	case "DATETIME", "TIMESTAMP":
		c.Type = types.DateTime
	case "BLOB":
		c.Type = types.Binary
	default:
		c.RawType = decl
	}
}

// ---------------------------------------------------------------- SQL Server

type mssqlIntrospector struct{}

func (mssqlIntrospector) introspect(ctx context.Context, q Queryer, b *builder) error {
	err := scanAll(ctx, q, `SELECT t.name, c.name, ty.name, c.max_length, c.precision, c.scale, c.is_nullable,
			c.is_identity, dc.definition
		FROM sys.columns c
		JOIN sys.tables t ON t.object_id = c.object_id
		JOIN sys.types ty ON ty.user_type_id = c.user_type_id
		LEFT JOIN sys.default_constraints dc ON dc.parent_object_id = c.object_id AND dc.parent_column_id = c.column_id
		WHERE t.schema_id = SCHEMA_ID() ORDER BY t.name, c.column_id`, nil, func(r *sql.Rows) error {
		var table, name, typ string
		var maxLen, prec, scale int
		var nullable, identity bool
		var def sql.NullString
		if err := r.Scan(&table, &name, &typ, &maxLen, &prec, &scale, &nullable, &identity, &def); err != nil {
			return err
		}
		c := b.table(table).AddColumn(name, "")
		c.Nullable = nullable
		c.AutoIncrement = identity
		c.Default = strPtr(def)
		switch typ {
		case "nvarchar":
			if maxLen == -1 {
				c.Type = types.Text
			} else {
				c.Type, c.Options.Length = types.String, maxLen/2
			}
		case "nchar":
			c.Type, c.Options.Length, c.Options.Fixed = types.String, maxLen/2, true
		case "smallint":
			c.Type = types.SmallInt
		case "int":
			c.Type = types.Integer
		case "bigint":
			c.Type = types.BigInt
		case "bit":
			c.Type = types.Boolean
		case "decimal", "numeric":
			c.Type, c.Options.Precision, c.Options.Scale = types.Decimal, prec, scale
		case "float":
			c.Type = types.Float
		case "date":
			c.Type = types.Date
		case "time":
			c.Type = types.Time
		case "datetime2":
			c.Type, c.Options.Precision = types.DateTime, scale
		case "datetimeoffset":
			c.Type, c.Options.Precision = types.DateTimeTZ, scale
		case "uniqueidentifier":
			c.Type = types.UUID
		case "varbinary":
			c.Type = types.Binary
			if maxLen > 0 {
				c.Options.Length = maxLen
			}
		case "binary":
			c.Type, c.Options.Length, c.Options.Fixed = types.Binary, maxLen, true
		case "varchar", "char":
			if maxLen == -1 {
				c.RawType = strings.ToUpper(typ) + "(MAX)"
			} else {
				c.RawType = strings.ToUpper(typ) + "(" + strconv.Itoa(maxLen) + ")"
			}
		default:
			c.RawType = strings.ToUpper(typ)
		}
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT t.name, i.name, i.is_unique, i.is_primary_key, c.name, COALESCE(i.filter_definition, '')
		FROM sys.indexes i
		JOIN sys.tables t ON t.object_id = i.object_id
		JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE t.schema_id = SCHEMA_ID() AND i.type > 0 AND ic.is_included_column = 0
		ORDER BY t.name, i.name, ic.key_ordinal`, nil, func(r *sql.Rows) error {
		var table, name, col, where string
		var unique, primary bool
		if err := r.Scan(&table, &name, &unique, &primary, &col, &where); err != nil {
			return err
		}
		b.index(b.table(table), name, unique, primary, col, where)
		return nil
	})
	if err != nil {
		return err
	}
	err = scanAll(ctx, q, `SELECT fk.name, tp.name, cp.name, tr.name, cr.name,
			fk.delete_referential_action_desc, fk.update_referential_action_desc
		FROM sys.foreign_keys fk
		JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
		JOIN sys.tables tp ON tp.object_id = fkc.parent_object_id
		JOIN sys.columns cp ON cp.object_id = fkc.parent_object_id AND cp.column_id = fkc.parent_column_id
		JOIN sys.tables tr ON tr.object_id = fkc.referenced_object_id
		JOIN sys.columns cr ON cr.object_id = fkc.referenced_object_id AND cr.column_id = fkc.referenced_column_id
		WHERE tp.schema_id = SCHEMA_ID()
		ORDER BY tp.name, fk.name, fkc.constraint_column_id`, nil, func(r *sql.Rows) error {
		var name, table, col, refTable, refCol, del, upd string
		if err := r.Scan(&name, &table, &col, &refTable, &refCol, &del, &upd); err != nil {
			return err
		}
		b.foreignKey(b.table(table), name, col, refTable, refCol, parseAction(del), parseAction(upd))
		return nil
	})
	if err != nil {
		return err
	}
	return scanAll(ctx, q, `SELECT name, CAST(start_value AS BIGINT), CAST(increment AS BIGINT)
		FROM sys.sequences WHERE schema_id = SCHEMA_ID() ORDER BY name`, nil, func(r *sql.Rows) error {
		var name string
		var start, inc int64
		if err := r.Scan(&name, &start, &inc); err != nil {
			return err
		}
		b.s.CreateSequence(name, start, inc)
		return nil
	})
}
