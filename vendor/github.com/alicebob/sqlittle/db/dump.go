// dump table and index structure
// usage: go run dump.go ./../testdata/single.sqlite
// +build never

package main

import (
	"flag"
	"fmt"
	"os"

	sdb "github.com/alicebob/sqlittle/db"
)

func main() {
	flag.Parse()
	for _, f := range flag.Args() {
		db, err := sdb.OpenFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s", f, err)
			continue
		}

		info, err := db.Info()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s", f, err)
			continue
		}
		fmt.Printf("%s:\n%s", f, info)
	}
}
