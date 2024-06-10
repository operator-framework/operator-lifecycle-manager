package basic

import (
	"context"
	"fmt"
	"io"

	"github.com/operator-framework/operator-registry/alpha/action"
	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/pkg/image"
)

type Template struct {
	Registry image.Registry
}

func (t Template) Render(ctx context.Context, reader io.Reader) (*declcfg.DeclarativeConfig, error) {
	cfg, err := declcfg.LoadReader(reader)
	if err != nil {
		return cfg, err
	}

	outb := cfg.Bundles[:0] // allocate based on max size of input, but empty slice
	// populate registry, incl any flags from CLI, and enforce only rendering bundle images
	r := action.Render{
		Registry:       t.Registry,
		AllowedRefMask: action.RefBundleImage,
	}

	for _, b := range cfg.Bundles {
		if !isBundleTemplate(&b) {
			return nil, fmt.Errorf("unexpected fields present in basic template bundle")
		}
		r.Refs = []string{b.Image}
		contributor, err := r.Run(ctx)
		if err != nil {
			return nil, err
		}
		outb = append(outb, contributor.Bundles...)
	}

	cfg.Bundles = outb
	return cfg, nil
}

// isBundleTemplate identifies a Bundle template source as having a Schema and Image defined
// but no Properties, RelatedImages or Package defined
func isBundleTemplate(b *declcfg.Bundle) bool {
	return b.Schema != "" && b.Image != "" && b.Package == "" && len(b.Properties) == 0 && len(b.RelatedImages) == 0
}
