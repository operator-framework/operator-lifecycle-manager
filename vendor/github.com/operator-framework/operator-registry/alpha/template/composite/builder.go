package composite

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
	basictemplate "github.com/operator-framework/operator-registry/alpha/template/basic"
	semvertemplate "github.com/operator-framework/operator-registry/alpha/template/semver"
	"github.com/operator-framework/operator-registry/pkg/image"
	"github.com/operator-framework/operator-registry/pkg/lib/config"
)

const (
	BasicBuilderSchema  = "olm.builder.basic"
	SemverBuilderSchema = "olm.builder.semver"
	RawBuilderSchema    = "olm.builder.raw"
	CustomBuilderSchema = "olm.builder.custom"
)

type BuilderConfig struct {
	WorkingDir       string
	OutputType       string
	ContributionPath string
}

type Builder interface {
	Build(ctx context.Context, reg image.Registry, dir string, td TemplateDefinition) error
	Validate(ctx context.Context, dir string) error
}

type BasicBuilder struct {
	builderCfg BuilderConfig
}

var _ Builder = &BasicBuilder{}

func NewBasicBuilder(builderCfg BuilderConfig) *BasicBuilder {
	return &BasicBuilder{
		builderCfg: builderCfg,
	}
}

func (bb *BasicBuilder) Build(ctx context.Context, reg image.Registry, dir string, td TemplateDefinition) error {
	if td.Schema != BasicBuilderSchema {
		return fmt.Errorf("schema %q does not match the basic template builder schema %q", td.Schema, BasicBuilderSchema)
	}
	// Parse out the basic template configuration
	basicConfig := &BasicConfig{}
	err := yaml.UnmarshalStrict(td.Config, basicConfig)
	if err != nil {
		return fmt.Errorf("unmarshalling basic template config: %w", err)
	}

	// validate the basic config fields
	valid := true
	validationErrs := []string{}
	if basicConfig.Input == "" {
		valid = false
		validationErrs = append(validationErrs, "basic template config must have a non-empty input (templateDefinition.config.input)")
	}

	if basicConfig.Output == "" {
		valid = false
		validationErrs = append(validationErrs, "basic template config must have a non-empty output (templateDefinition.config.output)")
	}

	if !valid {
		return fmt.Errorf("basic template configuration is invalid: %s", strings.Join(validationErrs, ","))
	}

	b := basictemplate.Template{Registry: reg}
	reader, err := os.Open(basicConfig.Input)
	if err != nil {
		if os.IsNotExist(err) && bb.builderCfg.ContributionPath != "" {
			reader, err = os.Open(path.Join(bb.builderCfg.ContributionPath, basicConfig.Input))
			if err != nil {
				return fmt.Errorf("error reading basic template: %v (tried contribution-local path: %q)", err, bb.builderCfg.ContributionPath)
			}
		} else {
			return fmt.Errorf("error reading basic template: %v", err)
		}
	}
	defer reader.Close()

	dcfg, err := b.Render(ctx, reader)
	if err != nil {
		return fmt.Errorf("error rendering basic template: %v", err)
	}

	destPath := path.Join(bb.builderCfg.WorkingDir, dir, basicConfig.Output)

	return build(dcfg, destPath, bb.builderCfg.OutputType)
}

func (bb *BasicBuilder) Validate(ctx context.Context, dir string) error {
	return validate(ctx, bb.builderCfg, dir)
}

type SemverBuilder struct {
	builderCfg BuilderConfig
}

var _ Builder = &SemverBuilder{}

func NewSemverBuilder(builderCfg BuilderConfig) *SemverBuilder {
	return &SemverBuilder{
		builderCfg: builderCfg,
	}
}

func (sb *SemverBuilder) Build(ctx context.Context, reg image.Registry, dir string, td TemplateDefinition) error {
	if td.Schema != SemverBuilderSchema {
		return fmt.Errorf("schema %q does not match the semver template builder schema %q", td.Schema, SemverBuilderSchema)
	}
	// Parse out the semver template configuration
	semverConfig := &SemverConfig{}
	err := yaml.UnmarshalStrict(td.Config, semverConfig)
	if err != nil {
		return fmt.Errorf("unmarshalling semver template config: %w", err)
	}

	// validate the semver config fields
	valid := true
	validationErrs := []string{}
	if semverConfig.Input == "" {
		valid = false
		validationErrs = append(validationErrs, "semver template config must have a non-empty input (templateDefinition.config.input)")
	}

	if semverConfig.Output == "" {
		valid = false
		validationErrs = append(validationErrs, "semver template config must have a non-empty output (templateDefinition.config.output)")
	}

	if !valid {
		return fmt.Errorf("semver template configuration is invalid: %s", strings.Join(validationErrs, ","))
	}

	reader, err := os.Open(semverConfig.Input)
	if err != nil {
		if os.IsNotExist(err) && sb.builderCfg.ContributionPath != "" {
			reader, err = os.Open(path.Join(sb.builderCfg.ContributionPath, semverConfig.Input))
			if err != nil {
				return fmt.Errorf("error reading semver template: %v (tried contribution-local path: %q)", err, sb.builderCfg.ContributionPath)
			}
		} else {
			return fmt.Errorf("error reading semver template: %v", err)
		}
	}
	defer reader.Close()

	s := semvertemplate.Template{Registry: reg, Data: reader}

	dcfg, err := s.Render(ctx)
	if err != nil {
		return fmt.Errorf("error rendering semver template: %v", err)
	}

	destPath := path.Join(sb.builderCfg.WorkingDir, dir, semverConfig.Output)

	return build(dcfg, destPath, sb.builderCfg.OutputType)
}

func (sb *SemverBuilder) Validate(ctx context.Context, dir string) error {
	return validate(ctx, sb.builderCfg, dir)
}

type RawBuilder struct {
	builderCfg BuilderConfig
}

var _ Builder = &RawBuilder{}

func NewRawBuilder(builderCfg BuilderConfig) *RawBuilder {
	return &RawBuilder{
		builderCfg: builderCfg,
	}
}

func (rb *RawBuilder) Build(ctx context.Context, _ image.Registry, dir string, td TemplateDefinition) error {
	if td.Schema != RawBuilderSchema {
		return fmt.Errorf("schema %q does not match the raw template builder schema %q", td.Schema, RawBuilderSchema)
	}
	// Parse out the raw template configuration
	rawConfig := &RawConfig{}
	err := yaml.UnmarshalStrict(td.Config, rawConfig)
	if err != nil {
		return fmt.Errorf("unmarshalling raw template config: %w", err)
	}

	// validate the raw config fields
	valid := true
	validationErrs := []string{}
	if rawConfig.Input == "" {
		valid = false
		validationErrs = append(validationErrs, "raw template config must have a non-empty input (templateDefinition.config.input)")
	}

	if rawConfig.Output == "" {
		valid = false
		validationErrs = append(validationErrs, "raw template config must have a non-empty output (templateDefinition.config.output)")
	}

	if !valid {
		return fmt.Errorf("raw template configuration is invalid: %s", strings.Join(validationErrs, ","))
	}

	reader, err := os.Open(rawConfig.Input)
	if err != nil {
		if os.IsNotExist(err) && rb.builderCfg.ContributionPath != "" {
			reader, err = os.Open(path.Join(rb.builderCfg.ContributionPath, rawConfig.Input))
			if err != nil {
				return fmt.Errorf("error reading raw input file: %v (tried contribution-local path: %q)", err, rb.builderCfg.ContributionPath)
			}
		} else {
			return fmt.Errorf("error reading raw input file: %v", err)
		}
	}
	defer reader.Close()

	dcfg, err := declcfg.LoadReader(reader)
	if err != nil {
		return fmt.Errorf("error parsing raw input file: %s, %v", rawConfig.Input, err)
	}

	destPath := path.Join(rb.builderCfg.WorkingDir, dir, rawConfig.Output)

	return build(dcfg, destPath, rb.builderCfg.OutputType)
}

func (rb *RawBuilder) Validate(ctx context.Context, dir string) error {
	return validate(ctx, rb.builderCfg, dir)
}

type CustomBuilder struct {
	builderCfg BuilderConfig
}

var _ Builder = &CustomBuilder{}

func NewCustomBuilder(builderCfg BuilderConfig) *CustomBuilder {
	return &CustomBuilder{
		builderCfg: builderCfg,
	}
}

func (cb *CustomBuilder) Build(ctx context.Context, reg image.Registry, dir string, td TemplateDefinition) error {
	if td.Schema != CustomBuilderSchema {
		return fmt.Errorf("schema %q does not match the custom template builder schema %q", td.Schema, CustomBuilderSchema)
	}
	// Parse out the raw template configuration
	customConfig := &CustomConfig{}
	err := yaml.UnmarshalStrict(td.Config, customConfig)
	if err != nil {
		return fmt.Errorf("unmarshalling custom template config: %w", err)
	}

	// validate the custom config fields
	valid := true
	validationErrs := []string{}
	if customConfig.Command == "" {
		valid = false
		validationErrs = append(validationErrs, "custom template config must have a non-empty command (templateDefinition.config.command)")
	}

	if customConfig.Output == "" {
		valid = false
		validationErrs = append(validationErrs, "custom template config must have a non-empty output (templateDefinition.config.output)")
	}

	if !valid {
		return fmt.Errorf("custom template configuration is invalid: %s", strings.Join(validationErrs, ","))
	}
	// build the command to execute
	cmd := exec.Command(customConfig.Command, customConfig.Args...)

	// custom template should output a valid FBC to STDOUT so we can
	// build the FBC just like all the other templates.
	v, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("running command %q: %v: %v", cmd.String(), err, v)
	}

	reader := bytes.NewReader(v)

	dcfg, err := declcfg.LoadReader(reader)
	cmdString := []string{customConfig.Command}
	cmdString = append(cmdString, customConfig.Args...)
	if err != nil {
		return fmt.Errorf("error parsing custom command output: %s, %v", strings.Join(cmdString, "'"), err)
	}

	destPath := path.Join(cb.builderCfg.WorkingDir, dir, customConfig.Output)

	// custom template should output a valid FBC to STDOUT so we can
	// build the FBC just like all the other templates.
	return build(dcfg, destPath, cb.builderCfg.OutputType)
}

func (cb *CustomBuilder) Validate(ctx context.Context, dir string) error {
	return validate(ctx, cb.builderCfg, dir)
}

func writeDeclCfg(dcfg declcfg.DeclarativeConfig, w io.Writer, output string) error {
	switch output {
	case "yaml":
		return declcfg.WriteYAML(dcfg, w)
	case "json":
		return declcfg.WriteJSON(dcfg, w)
	default:
		return fmt.Errorf("invalid --output value %q, expected (json|yaml)", output)
	}
}

func validate(ctx context.Context, builderCfg BuilderConfig, dir string) error {

	path := path.Join(builderCfg.WorkingDir, dir)
	s, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("directory not found. validation path needs to be composed of BuilderConfig.WorkingDir+Component[].Destination.Path: %q: %v", path, err)
	}
	if !s.IsDir() {
		return fmt.Errorf("%q is not a directory", path)
	}

	if err := config.Validate(ctx, os.DirFS(path)); err != nil {
		return fmt.Errorf("validation failure in path %q: %v", path, err)
	}
	return nil
}

func build(dcfg *declcfg.DeclarativeConfig, outPath string, outType string) error {
	// create the destination for output, if it does not exist
	outDir := filepath.Dir(outPath)
	err := os.MkdirAll(outDir, 0o777)
	if err != nil {
		return fmt.Errorf("creating output directory %q: %v", outPath, err)
	}

	// write the dcfg
	file, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("creating output file %q: %v", outPath, err)
	}
	defer file.Close()

	err = writeDeclCfg(*dcfg, file, outType)
	if err != nil {
		return fmt.Errorf("writing to output file %q: %v", outPath, err)
	}

	return nil
}
