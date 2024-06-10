package config

import (
	"context"
	"io/fs"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
)

// Validate takes a filesystem containing the declarative config file(s)
// 1. Validate if declarative config file(s) are valid based on specified schema
// 2. Validate the `replaces` chains of the upgrade graph
// Inputs:
// directory: a filesystem where declarative config file(s) exist
// Outputs:
// error: a wrapped error that contains a tree of error strings
func Validate(ctx context.Context, root fs.FS) error {
	// Load config files and convert them to declcfg objects
	cfg, err := declcfg.LoadFS(ctx, root)
	if err != nil {
		return err
	}
	// Validate the config using model validation:
	// This will convert declcfg objects to intermediate model objects that are
	// also used for serve and add commands. The conversion process will run
	// validation for the model objects and ensure they are valid.
	_, err = declcfg.ConvertToModel(*cfg)
	if err != nil {
		return err
	}
	return nil
}
