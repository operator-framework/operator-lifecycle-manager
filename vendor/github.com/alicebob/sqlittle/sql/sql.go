package sql

import (
	"fmt"
	"strings"
)

type SortOrder int

const (
	Asc SortOrder = iota
	Desc
)

func (so SortOrder) String() string {
	switch so {
	case Asc:
		return "ASC"
	case Desc:
		return "DESC"
	default:
		return "???"
	}
}

// A `SELECT` statement
type SelectStmt struct {
	Columns []string
	Table   string
}

// A `CREATE TABLE` statement
type CreateTableStmt struct {
	Table        string
	Columns      []ColumnDef
	Constraints  []TableConstraint
	WithoutRowid bool
}

// Definition of a column, as found by CreateTableStmt
type ColumnDef struct {
	Name          string
	Type          string
	PrimaryKey    bool
	PrimaryKeyDir SortOrder
	AutoIncrement bool
	Null          bool
	Unique        bool
	Default       interface{}
	Collate       string
	References    *ForeignKeyClause
	Checks        []Expression
}

// column constraints, used while parsing a constraint list
type columnConstraint interface{}
type ccPrimaryKey struct {
	sort          SortOrder
	autoincrement bool
}
type ccUnique bool
type ccNull bool
type ccAutoincrement bool
type ccCollate string
type ccDefault interface{}
type ccReferences ForeignKeyClause
type ccCheck struct {
	expr Expression
}

func makeColumnDef(name string, typ string, cs []columnConstraint) ColumnDef {
	cd := ColumnDef{
		Name: name,
		Type: typ,
		Null: true,
	}
	for _, c := range cs {
		switch v := c.(type) {
		case ccNull:
			cd.Null = bool(v)
		case ccPrimaryKey:
			cd.PrimaryKey = true
			cd.PrimaryKeyDir = SortOrder(v.sort)
			cd.AutoIncrement = v.autoincrement
		case ccUnique:
			cd.Unique = bool(v)
		case ccCollate:
			cd.Collate = string(v)
		case ccReferences:
			clause := ForeignKeyClause(v)
			cd.References = &clause
		case ccCheck:
			cd.Checks = append(cd.Checks, v.expr)
		case ccDefault:
			cd.Default = interface{}(v)
		case nil:
			cd.Default = nil
		default:
			panic("unhandled constraint")
		}
	}
	return cd
}

type ForeignKeyClause struct {
	ForeignTable      string
	ForeignColumns    []string
	Deferrable        bool
	InitiallyDeferred bool
	Triggers          []Trigger
}

// CREATE TABLE constraint (primary key, index)
type TableConstraint interface{}
type TablePrimaryKey struct {
	IndexedColumns []IndexedColumn
}
type TableUnique struct {
	IndexedColumns []IndexedColumn
}
type TableForeignKey struct {
	Columns []string
	Clause  ForeignKeyClause
}
type Trigger interface{}
type TriggerOnDelete TriggerAction
type TriggerOnUpdate TriggerAction

type TriggerAction int

const (
	ActionSetNull TriggerAction = iota
	ActionSetDefault
	ActionCascade
	ActionRestrict
	ActionNoAction
)

// TriggerMatch string

// A `CREATE INDEX` statement
type CreateIndexStmt struct {
	Index          string
	Table          string
	Unique         bool
	IndexedColumns []IndexedColumn
	Where          Expression
}

// Indexed column, for CreateIndexStmt, and index table constraints.
// Either Column or Expression is filled. Column is filled if the expression is
// a single column (as is always the case for PRIMARY KEY and UNIQUE
// constraints), and Expression is filled in every other case.
type IndexedColumn struct {
	Column     string
	Expression string
	Collate    string
	SortOrder  SortOrder
}

func newIndexColumn(e Expression, collate string, sort SortOrder) IndexedColumn {
	col := AsColumn(e)
	ex := ""
	if col == "" {
		ex = AsString(e)
	}
	return IndexedColumn{
		Column:     col,
		Expression: ex,
		Collate:    collate,
		SortOrder:  sort,
	}
}

type Expression interface{}

type ExBinaryOp struct {
	Op          string
	Left, Right Expression
}

type ExColumn string

type ExFunction struct {
	F    string
	Args []Expression
}

func AsString(e Expression) string {
	switch v := e.(type) {
	case int64:
		return fmt.Sprintf("%d", v)
	case float64:
		return fmt.Sprintf("%g", v)
	case string:
		return fmt.Sprintf("'%s'", v)
	case ExColumn:
		return fmt.Sprintf(`"%s"`, string(v))
	case ExFunction:
		var args []string
		for _, a := range v.Args {
			args = append(args, AsString(a))
		}
		return fmt.Sprintf(`"%s"(%s)`, v.F, strings.Join(args, `, `))
	case ExBinaryOp:
		return fmt.Sprintf(`%s%s%s`, AsString(v.Left), v.Op, AsString(v.Right))
	default:
		return "bug"
	}
}

// gives the column name if the expression is a simple single column
func AsColumn(e Expression) string {
	switch v := e.(type) {
	case ExColumn:
		return fmt.Sprintf(`%s`, string(v))
	default:
		return ""
	}
}

// Parse is the main function. It will return either an error or a *Stmt
// struct.
func Parse(sql string) (interface{}, error) {
	ts, err := tokenize(sql)
	if err != nil {
		return nil, err
	}
	l := &lexer{tokens: ts}
	yyParse(l)
	return l.result, l.err
}
