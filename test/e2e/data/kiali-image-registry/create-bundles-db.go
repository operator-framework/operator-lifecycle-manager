package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/operator-framework/operator-registry/pkg/sqlite"
)

func checkErr(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	dbFile := "bundles.db"
	bundleImage := "quay.io/olmtest/installplan_e2e-bundle-image:latest"
	dataPath := "../kiali-manifests"

	// start with a clean slate
	os.Remove(dbFile)

	// create database
	db, err := sql.Open("sqlite3", dbFile)
	checkErr(err)

	dbLoader, err := sqlite.NewSQLLiteLoader(db)
	checkErr(err)

	err = dbLoader.Migrate(context.TODO())
	checkErr(err)

	// populate database with data
	loader := sqlite.NewSQLLoaderForDirectory(dbLoader, dataPath)
	err = loader.Populate()
	checkErr(err)

	// add a bundlepath for kiali 1.4.2 so that later a bundle image lookup is performed
	updateSQL := fmt.Sprintf(`UPDATE operatorbundle SET bundlepath = '%v' WHERE version = "1.4.2";`, bundleImage)
	_, err = db.Exec(updateSQL)
	checkErr(err)
}
