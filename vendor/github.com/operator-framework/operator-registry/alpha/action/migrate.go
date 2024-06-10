package action

import (
	"context"
	"fmt"
	"os"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/image"
)

type Migrate struct {
	CatalogRef string
	OutputDir  string

	WriteFunc declcfg.WriteFunc
	FileExt   string
	Registry  image.Registry
}

func (m Migrate) Run(ctx context.Context) error {
	entries, err := os.ReadDir(m.OutputDir)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) > 0 {
		return fmt.Errorf("output dir %q must be empty", m.OutputDir)
	}

	r := Render{
		Refs:    []string{m.CatalogRef},
		Migrate: true,

		// Only allow catalogs to be migrated.
		AllowedRefMask: RefSqliteImage | RefSqliteFile | RefDCImage | RefDCDir,
	}
	if m.Registry != nil {
		r.Registry = m.Registry
	}

	cfg, err := r.Run(ctx)
	if err != nil {
		return fmt.Errorf("render catalog image: %w", err)
	}

	return declcfg.WriteFS(*cfg, m.OutputDir, m.WriteFunc, m.FileExt)
}
