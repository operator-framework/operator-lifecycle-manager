package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/operator-framework/operator-registry/pkg/registry"
)

const RelatedImagesMigrationKey = 1

func init() {
	registerMigration(RelatedImagesMigrationKey, relatedImagesMigration)
}

// listBundles returns a list of operatorbundles as strings
func listBundles(ctx context.Context, tx *sql.Tx) ([]string, error) {
	query := "SELECT DISTINCT name FROM operatorbundle"
	rows, err := tx.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	bundles := []string{}
	for rows.Next() {
		var bundleName sql.NullString
		if err := rows.Scan(&bundleName); err != nil {
			return nil, err
		}
		if bundleName.Valid {
			bundles = append(bundles, bundleName.String)
		}
	}
	return bundles, nil
}

// getCSV pulls the csv from by name
func getCSV(ctx context.Context, tx *sql.Tx, name string) (*registry.ClusterServiceVersion, error) {
	query := `SELECT csv FROM operatorbundle WHERE operatorbundle.name=?`
	rows, err := tx.QueryContext(ctx, query, name)
	if err != nil {
		return nil, err
	}

	var csvJson sql.NullString
	if !rows.Next() {
		return nil, fmt.Errorf("bundle %s not found", name)
	}
	if err := rows.Scan(&csvJson); err != nil {
		return nil, err
	}
	if !csvJson.Valid {
		return nil, fmt.Errorf("bad value for csv")
	}
	csv := &registry.ClusterServiceVersion{}
	if err := json.Unmarshal([]byte(csvJson.String), csv); err != nil {
		return nil, err
	}
	return csv, nil
}

func extractRelatedImages(ctx context.Context, tx *sql.Tx, name string) error {
	addSql := `insert into related_image(image, operatorbundle_name) values(?,?)`
	csv, err := getCSV(ctx, tx, name)
	if err != nil {
		logrus.Warnf("error backfilling related images: %v", err)
		return err
	}
	images, err := csv.GetOperatorImages()
	if err != nil {
		logrus.Warnf("error backfilling related images: %v", err)
		return err
	}
	related, err := csv.GetRelatedImages()
	if err != nil {
		logrus.Warnf("error backfilling related images: %v", err)
		return err
	}
	for k := range related {
		images[k] = struct{}{}
	}
	for img := range images {
		if _, err := tx.ExecContext(ctx, addSql, img, name); err != nil {
			logrus.Warnf("error backfilling related images: %v", err)
			continue
		}
	}
	return nil
}

var relatedImagesMigration = &Migration{
	Id: RelatedImagesMigrationKey,
	Up: func(ctx context.Context, tx *sql.Tx) error {
		sql := `
		CREATE TABLE IF NOT EXISTS related_image (
			image TEXT,
     		operatorbundle_name TEXT,
     		FOREIGN KEY(operatorbundle_name) REFERENCES operatorbundle(name)
		);
		`
		_, err := tx.ExecContext(ctx, sql)

		bundles, err := listBundles(ctx, tx)
		if err != nil {
			return err
		}
		for _, bundle := range bundles {
			if err := extractRelatedImages(ctx, tx, bundle); err != nil {
				logrus.Warnf("error backfilling related images: %v", err)
				continue
			}
		}
		return err
	},
	Down: func(ctx context.Context, tx *sql.Tx) error {
		sql := `DROP TABLE related_image;`
		_, err := tx.ExecContext(ctx, sql)
		return err
	},
}
