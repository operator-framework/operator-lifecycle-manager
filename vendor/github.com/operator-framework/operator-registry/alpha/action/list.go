package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/operator-framework/api/pkg/operators/v1alpha1"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	"github.com/operator-framework/operator-registry/alpha/model"
	"github.com/operator-framework/operator-registry/pkg/image"
)

type ListPackages struct {
	IndexReference string
	Registry       image.Registry
}

func (l *ListPackages) Run(ctx context.Context) (*ListPackagesResult, error) {
	m, err := indexRefToModel(ctx, l.IndexReference, l.Registry)
	if err != nil {
		return nil, err
	}

	pkgs := []model.Package{}
	for _, pkg := range m {
		pkgs = append(pkgs, *pkg)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		return pkgs[i].Name < pkgs[j].Name
	})
	return &ListPackagesResult{Packages: pkgs}, nil
}

type ListPackagesResult struct {
	Packages []model.Package
}

func (r *ListPackagesResult) WriteColumns(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tDISPLAY NAME\tDEFAULT CHANNEL"); err != nil {
		return err
	}
	for _, pkg := range r.Packages {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", pkg.Name, getDisplayName(pkg), pkg.DefaultChannel.Name); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func getDisplayName(pkg model.Package) string {
	if pkg.DefaultChannel == nil {
		return ""
	}
	head, err := pkg.DefaultChannel.Head()
	if err != nil || head == nil || head.CsvJSON == "" {
		return ""
	}

	csv := v1alpha1.ClusterServiceVersion{}
	if err := json.Unmarshal([]byte(head.CsvJSON), &csv); err != nil {
		return ""
	}
	return csv.Spec.DisplayName
}

type ListChannels struct {
	IndexReference string
	PackageName    string
	Registry       image.Registry
}

func (l *ListChannels) Run(ctx context.Context) (*ListChannelsResult, error) {
	m, err := indexRefToModel(ctx, l.IndexReference, l.Registry)
	if err != nil {
		return nil, err
	}

	pkgs, err := getPackages(m, l.PackageName)
	if err != nil {
		return nil, err
	}

	channels := []model.Channel{}
	for _, pkg := range pkgs {
		for _, ch := range pkg.Channels {
			channels = append(channels, *ch)
		}
	}

	sort.Slice(channels, func(i, j int) bool {
		if channels[i].Package.Name != channels[j].Package.Name {
			return channels[i].Package.Name < channels[j].Package.Name
		}
		return channels[i].Name < channels[j].Name
	})
	return &ListChannelsResult{Channels: channels}, nil
}

type ListChannelsResult struct {
	Channels []model.Channel
}

func (r *ListChannelsResult) WriteColumns(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PACKAGE\tCHANNEL\tHEAD"); err != nil {
		return err
	}
	for _, ch := range r.Channels {
		headStr := ""
		head, err := ch.Head()
		if err != nil {
			headStr = fmt.Sprintf("ERROR: %s", err)
		} else {
			headStr = head.Name
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\n", ch.Package.Name, ch.Name, headStr); err != nil {
			return err
		}
	}
	return tw.Flush()
}

type ListBundles struct {
	IndexReference string
	PackageName    string
	Registry       image.Registry
}

func (l *ListBundles) Run(ctx context.Context) (*ListBundlesResult, error) {
	m, err := indexRefToModel(ctx, l.IndexReference, l.Registry)
	if err != nil {
		return nil, err
	}

	pkgs, err := getPackages(m, l.PackageName)
	if err != nil {
		return nil, err
	}

	bundles := []model.Bundle{}
	for _, pkg := range pkgs {
		for _, ch := range pkg.Channels {
			for _, b := range ch.Bundles {
				bundles = append(bundles, *b)
			}
		}
	}

	sort.Slice(bundles, func(i, j int) bool {
		if bundles[i].Package.Name != bundles[j].Package.Name {
			return bundles[i].Package.Name < bundles[j].Package.Name
		}
		if bundles[i].Channel.Name != bundles[j].Channel.Name {
			return bundles[i].Channel.Name < bundles[j].Channel.Name
		}
		return bundles[i].Name < bundles[j].Name
	})
	return &ListBundlesResult{Bundles: bundles}, nil
}

type ListBundlesResult struct {
	Bundles []model.Bundle
}

func (r *ListBundlesResult) WriteColumns(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "PACKAGE\tCHANNEL\tBUNDLE\tREPLACES\tSKIPS\tSKIP RANGE\tIMAGE"); err != nil {
		return err
	}
	for _, b := range r.Bundles {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", b.Package.Name, b.Channel.Name, b.Name, b.Replaces, strings.Join(b.Skips, ","), b.SkipRange, b.Image); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func indexRefToModel(ctx context.Context, ref string, reg image.Registry) (model.Model, error) {
	render := Render{
		Refs:           []string{ref},
		AllowedRefMask: RefDCImage | RefDCDir | RefSqliteImage | RefSqliteFile,
		Registry:       reg,
	}
	cfg, err := render.Run(ctx)
	if err != nil {
		if errors.Is(err, ErrNotAllowed) {
			return nil, fmt.Errorf("cannot list non-index %q", ref)
		}
		return nil, err
	}

	return declcfg.ConvertToModel(*cfg)
}

func getPackages(m model.Model, packageName string) (model.Model, error) {
	if packageName == "" {
		return m, nil
	}
	pkg, ok := m[packageName]
	if !ok {
		return nil, fmt.Errorf("package %q not found", packageName)
	}
	return model.Model{packageName: pkg}, nil
}
