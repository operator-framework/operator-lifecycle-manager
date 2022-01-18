package cache

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/blang/semver/v4"

	"github.com/operator-framework/api/pkg/constraints"
	opregistry "github.com/operator-framework/operator-registry/pkg/registry"
)

type Predicate interface {
	Test(*Entry) bool
	String() string
}

type csvNamePredicate string

func CSVNamePredicate(name string) Predicate {
	return csvNamePredicate(name)
}

func (c csvNamePredicate) Test(o *Entry) bool {
	return o.Name == string(c)
}

func (c csvNamePredicate) String() string {
	return fmt.Sprintf("with name: %s", string(c))
}

type channelPredicate string

func ChannelPredicate(channel string) Predicate {
	return channelPredicate(channel)
}

func (ch channelPredicate) Test(o *Entry) bool {
	// all operators match the empty channel
	if string(ch) == "" {
		return true
	}
	if si := o.SourceInfo; si != nil {
		return si.Channel == string(ch)
	}
	return false
}

func (ch channelPredicate) String() string {
	return fmt.Sprintf("with channel: %s", string(ch))
}

type pkgPredicate string

func PkgPredicate(pkg string) Predicate {
	return pkgPredicate(pkg)
}

func (pkg pkgPredicate) Test(o *Entry) bool {
	for _, p := range o.Properties {
		if p.Type != opregistry.PackageType {
			continue
		}
		var prop opregistry.PackageProperty
		err := json.Unmarshal([]byte(p.Value), &prop)
		if err != nil {
			continue
		}
		if prop.PackageName == string(pkg) {
			return true
		}
	}
	return o.Package() == string(pkg)
}

func (pkg pkgPredicate) String() string {
	return fmt.Sprintf("with package: %s", string(pkg))
}

type versionInRangePredicate struct {
	r   semver.Range
	str string
}

func VersionInRangePredicate(r semver.Range, version string) Predicate {
	return versionInRangePredicate{r: r, str: version}
}

func (v versionInRangePredicate) Test(o *Entry) bool {
	for _, p := range o.Properties {
		if p.Type != opregistry.PackageType {
			continue
		}
		var prop opregistry.PackageProperty
		err := json.Unmarshal([]byte(p.Value), &prop)
		if err != nil {
			continue
		}
		ver, err := semver.Parse(prop.Version)
		if err != nil {
			continue
		}
		if v.r(ver) {
			return true
		}
	}
	return o.Version != nil && v.r(*o.Version)
}

func (v versionInRangePredicate) String() string {
	return fmt.Sprintf("with version in range: %v", v.str)
}

type labelPredicate string

func LabelPredicate(label string) Predicate {
	return labelPredicate(label)
}
func (l labelPredicate) Test(o *Entry) bool {
	for _, p := range o.Properties {
		if p.Type != opregistry.LabelType {
			continue
		}
		var prop opregistry.LabelProperty
		err := json.Unmarshal([]byte(p.Value), &prop)
		if err != nil {
			continue
		}
		if prop.Label == string(l) {
			return true
		}
	}
	return false
}

func (l labelPredicate) String() string {
	return fmt.Sprintf("with label: %v", string(l))
}

type catalogPredicate struct {
	key SourceKey
}

func CatalogPredicate(key SourceKey) Predicate {
	return catalogPredicate{key: key}
}

func (c catalogPredicate) Test(o *Entry) bool {
	return c.key.Equal(o.SourceInfo.Catalog)
}

func (c catalogPredicate) String() string {
	return fmt.Sprintf("from catalog: %v/%v", c.key.Namespace, c.key.Name)
}

type gvkPredicate struct {
	api opregistry.APIKey
}

func ProvidingAPIPredicate(api opregistry.APIKey) Predicate {
	return gvkPredicate{
		api: api,
	}
}

func (g gvkPredicate) Test(o *Entry) bool {
	for _, p := range o.Properties {
		if p.Type != opregistry.GVKType {
			continue
		}
		var prop opregistry.GVKProperty
		err := json.Unmarshal([]byte(p.Value), &prop)
		if err != nil {
			continue
		}
		if prop.Kind == g.api.Kind && prop.Version == g.api.Version && prop.Group == g.api.Group {
			return true
		}
	}
	return false
}

func (g gvkPredicate) String() string {
	return fmt.Sprintf("providing an API with group: %s, version: %s, kind: %s", g.api.Group, g.api.Version, g.api.Kind)
}

type skipRangeIncludesPredication struct {
	version semver.Version
}

func SkipRangeIncludesPredicate(version semver.Version) Predicate {
	return skipRangeIncludesPredication{version: version}
}

func (s skipRangeIncludesPredication) Test(o *Entry) bool {
	return o.SkipRange != nil && o.SkipRange(s.version)
}

func (s skipRangeIncludesPredication) String() string {
	return fmt.Sprintf("skip range includes: %v", s.version.String())
}

type replacesPredicate string

func ReplacesPredicate(replaces string) Predicate {
	return replacesPredicate(replaces)
}

func (r replacesPredicate) Test(o *Entry) bool {
	if o.Replaces == string(r) {
		return true
	}
	for _, s := range o.Skips {
		if s == string(r) {
			return true
		}
	}
	return false
}

func (r replacesPredicate) String() string {
	return fmt.Sprintf("replaces: %v", string(r))
}

type andPredicate struct {
	predicates []Predicate
}

func And(p ...Predicate) Predicate {
	return andPredicate{
		predicates: p,
	}
}

func (p andPredicate) Test(o *Entry) bool {
	for _, predicate := range p.predicates {
		if !predicate.Test(o) {
			return false
		}
	}
	return true
}

func (p andPredicate) String() string {
	var b bytes.Buffer
	for i, predicate := range p.predicates {
		b.WriteString(predicate.String())
		if i != len(p.predicates)-1 {
			b.WriteString(" and ")
		}
	}
	return b.String()
}

func Or(p ...Predicate) Predicate {
	return orPredicate{
		predicates: p,
	}
}

type orPredicate struct {
	predicates []Predicate
}

func (p orPredicate) Test(o *Entry) bool {
	for _, predicate := range p.predicates {
		if predicate.Test(o) {
			return true
		}
	}
	return false
}

func (p orPredicate) String() string {
	var b bytes.Buffer
	for i, predicate := range p.predicates {
		b.WriteString(predicate.String())
		if i != len(p.predicates)-1 {
			b.WriteString(" or ")
		}
	}
	return b.String()
}

func Not(p ...Predicate) Predicate {
	return notPredicate{
		predicates: p,
	}
}

type notPredicate struct {
	predicates []Predicate
}

func (p notPredicate) Test(o *Entry) bool {
	// !pred && !pred is equivalent to !(pred || pred).
	return !orPredicate(p).Test(o)
}

func (p notPredicate) String() string {
	var b bytes.Buffer
	for i, predicate := range p.predicates {
		b.WriteString(predicate.String())
		if i != len(p.predicates)-1 {
			b.WriteString(" and not ")
		}
	}
	return b.String()
}

type booleanPredicate struct {
	result bool
}

func BooleanPredicate(result bool) Predicate {
	return booleanPredicate{result: result}
}

func (b booleanPredicate) Test(o *Entry) bool {
	return b.result
}

func (b booleanPredicate) String() string {
	if b.result {
		return "predicate is true"
	}
	return "predicate is false"
}

func True() Predicate {
	return BooleanPredicate(true)
}

func False() Predicate {
	return BooleanPredicate(false)
}

type countingPredicate struct {
	p Predicate
	n *int
}

func (c countingPredicate) Test(o *Entry) bool {
	if c.p.Test(o) {
		*c.n++
		return true
	}
	return false
}

func (c countingPredicate) String() string {
	return c.p.String()
}

func CountingPredicate(p Predicate, n *int) Predicate {
	return countingPredicate{p: p, n: n}
}

type celPredicate struct {
	program        constraints.CelProgram
	rule           string
	failureMessage string
}

func (cp *celPredicate) Test(entry *Entry) bool {
	props := make([]map[string]interface{}, len(entry.Properties))
	for i, p := range entry.Properties {
		var v interface{}
		if err := json.Unmarshal([]byte(p.Value), &v); err != nil {
			continue
		}
		props[i] = map[string]interface{}{
			"type":  p.Type,
			"value": v,
		}
	}

	ok, err := cp.program.Evaluate(map[string]interface{}{"properties": props})
	if err != nil {
		return false
	}
	return ok
}

func CreateCelPredicate(env *constraints.CelEnvironment, rule string, failureMessage string) (Predicate, error) {
	prog, err := env.Validate(rule)
	if err != nil {
		return nil, err
	}
	return &celPredicate{program: prog, rule: rule, failureMessage: failureMessage}, nil
}

func (cp *celPredicate) String() string {
	return fmt.Sprintf("with constraint: %q and message: %q", cp.rule, cp.failureMessage)
}
