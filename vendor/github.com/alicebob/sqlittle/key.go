package sqlittle

import (
	"fmt"
	"strings"

	sdb "github.com/alicebob/sqlittle/db"
	"github.com/alicebob/sqlittle/sql"
)

// Key is used to find a record.
//
// It accepts most Go datatypes, but they will be converted to the set SQLite
// supports:
// nil, int64, float64, string, []byte
type Key []interface{}

// asDbKey translates a Key to a db.Key. Applies DESC and collate, and changes
// values to the few datatypes db.Key accepts.
func asDbKey(k Key, cols []sdb.IndexColumn) (sdb.Key, error) {
	dbk := make(sdb.Key, len(k))
	for i, kv := range k {
		if i > len(cols)-1 {
			return nil, fmt.Errorf("too many columns in Key")
		}
		c := cols[i]
		dbk[i].Desc = c.SortOrder == sql.Desc
		if collate := strings.ToLower(c.Collate); collate != "" {
			if _, ok := sdb.CollateFuncs[collate]; !ok {
				return nil, fmt.Errorf("unknown collate function: %q", collate)
			}
			dbk[i].Collate = collate
		}
		switch kv := kv.(type) {
		case nil:
			dbk[i].V = nil
		case int64:
			dbk[i].V = kv
		case float64:
			dbk[i].V = kv
		case string:
			dbk[i].V = kv
		case []byte:
			dbk[i].V = kv

		case int:
			dbk[i].V = int64(kv)
		case uint:
			dbk[i].V = int64(kv)
		case int32:
			dbk[i].V = int64(kv)
		case uint32:
			dbk[i].V = int64(kv)
		case float32:
			dbk[i].V = float64(kv)
		case bool:
			v := int64(0)
			if kv {
				v = 1
			}
			dbk[i].V = v

		default:
			return nil, fmt.Errorf("unknown Key datatype: %T", kv)
		}
	}
	return dbk, nil
}
