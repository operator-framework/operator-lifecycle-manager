package resolver

import (
	"github.com/pkg/errors"

	"github.com/operator-framework/operator-registry/pkg/api"
)

// TODO: this should take a cancellable context for killing long resolution
// TODO: return a set of errors or warnings of unusual states to know about (we expect evolve to always succeed, because it can be a no-op)

// Evolvers modify a generation to a new state
type Evolver interface {
	Evolve(add map[OperatorSourceInfo]struct{}) error
}

type NamespaceGenerationEvolver struct {
	querier SourceQuerier
	gen     Generation
	replacements map[OperatorSurface]OperatorSurface
}

func NewNamespaceGenerationEvolver(querier SourceQuerier, gen Generation) Evolver {
	return &NamespaceGenerationEvolver{querier: querier, gen: gen}
}

// Evolve takes new requested operators, adds them to the generation, and attempts to resolve dependencies with querier
func (e *NamespaceGenerationEvolver) Evolve(add map[OperatorSourceInfo]struct{}) error {
	if err := e.querier.Queryable(); err != nil {
		return err
	}

	// check for updates to existing operators
	if err := e.checkForUpdates(); err != nil {
		return err
	}

	// fetch bundles for new operators (aren't yet tracked)
	if err := e.addNewOperators(add); err != nil {
		return err
	}

	// attempt to resolve any missing apis as a result expanding the generation of operators
	if err := e.queryForRequiredAPIs(); err != nil {
		return err
	}

	// if apis are missing, attempt to contract back to a good set by trying all combinations of rollbacks to updates
	if err := e.downgradeUpdates(); err != nil {
		return err
	}

	// for any remaining missing APIs, attempt to downgrade the operator that required them
	// this may contract the generation back to the original set!
	e.downgradeAPIs()
	return nil
}

func (e *NamespaceGenerationEvolver) checkForUpdates() error {
	e.replacements = make(map[OperatorSurface]OperatorSurface)

	// maps the old operator identifier to the new operator
	updates := EmptyOperatorSet()

	// take a snapshot of the current generation so that we don't update the same operator twice in one resolution
	snapshot := e.gen.Operators().Snapshot()

	for _, op := range snapshot {
		// only check for updates if we have sourceinfo
		if op.SourceInfo() == &ExistingOperator {
			continue
		}

		bundle, key, err := e.querier.FindReplacement(op.Version(), op.Identifier(), op.SourceInfo().Package, op.SourceInfo().Channel, op.SourceInfo().Catalog)
		if err != nil || bundle == nil {
			continue
		}

		o, err := NewOperatorFromBundle(bundle, op.SourceInfo().StartingCSV, *key)
		if err != nil {
			return errors.Wrap(err, "error parsing bundle")
		}
		o.SetReplaces(op.Identifier())
		updates[op.Identifier()] = o
		e.replacements[op] = o
	}

	// remove any operators we found updates for
	for old := range updates {
		e.gen.RemoveOperator(e.gen.Operators().Snapshot()[old])
	}

	// add the new operators we found
	for _, new := range updates {
		if err := e.gen.AddOperator(new); err != nil {
			return errors.Wrap(err, "error calculating generation changes due to new bundle")
		}
	}

	return nil
}

func (e *NamespaceGenerationEvolver) addNewOperators(add map[OperatorSourceInfo]struct{}) error {
	for s := range add {
		var bundle *api.Bundle
		var key *CatalogKey
		var err error
		if s.StartingCSV != "" {
			bundle, key, err = e.querier.FindBundle(s.Package, s.Channel, s.StartingCSV, s.Catalog)
		} else {
			bundle, key, err = e.querier.FindLatestBundle(s.Package, s.Channel, s.Catalog)
		}
		if err != nil {
			return errors.Wrapf(err, "%s not found", s)
		}

		o, err := NewOperatorFromBundle(bundle, s.StartingCSV, *key)
		if err != nil {
			return errors.Wrap(err, "error parsing bundle")
		}
		if err := e.gen.AddOperator(o); err != nil {
			return errors.Wrap(err, "error calculating generation changes due to new bundle")
		}
	}
	return nil
}

func (e *NamespaceGenerationEvolver) queryForRequiredAPIs() error {
	e.gen.ResetUnchecked()

	for {
		api := e.gen.UncheckedAPIs().PopAPIKey()
		if api == nil {
			break
		}
		e.gen.MarkAPIChecked(*api)

		// identify the initialSource
		var initialSource *OperatorSourceInfo
		for _, operator := range e.gen.MissingAPIs()[*api] {
			initialSource = operator.SourceInfo()
			break
		}
		// Get the list of installed operators in the namespace
		opList := make(map[string]struct{})
		for _, operator := range e.gen.Operators() {
			opList[operator.SourceInfo().Package] = struct{}{}
		}

		// attempt to find a bundle that provides that api
		if bundle, key, err := e.querier.FindProvider(*api, initialSource.Catalog, opList); err == nil {
			// add a bundle that provides the api to the generation
			o, err := NewOperatorFromBundle(bundle, "", *key)
			if err != nil {
				return errors.Wrap(err, "error parsing bundle")
			}
			if err := e.gen.AddOperator(o); err != nil {
				return errors.Wrap(err, "error calculating generation changes due to new bundle")
			}
		}
	}
	return nil
}

func (e *NamespaceGenerationEvolver) downgradeUpdates() error {
	// no need to attempt downgrades
	if len(e.gen.MissingAPIs()) == 0 {
		return nil
	}

	// smart downgrades are only supported if fewer than 64 updates are resolved at the same time
	// (this should be all but pathological cases)
	if len(e.replacements) > 64 {
		return nil
	}

	old := make([]OperatorSurface, 0)
	new := make([]OperatorSurface, 0)
	for o, n := range e.replacements {
		old = append(old, o)
		new = append(new, n)
	}
	flagToIndex := make(map[uint64]int)
	flags := make([]uint64, 0)
	var max uint64
	for i := 0; i < len(e.replacements); i++ {
		var f uint64 = 1 << i
		flags = append(flags, f)
		max += f
		flagToIndex[f] = i
	}

	var i uint64
	var g Generation
	for i = 0; i <= max; i++ {
		g = NewEmptyGeneration()
		for _, f := range flags {
			idx := flagToIndex[f]
			// if toggled, pick old
			if f&i != 0 {
				_ = g.AddOperator(old[idx])
			} else {
				_ = g.AddOperator(new[idx])
			}
		}
		// we found a good set, update the real generation and quit
		if len(g.MissingAPIs()) == 0 {
			for _, f := range flags {
				idx := flagToIndex[f]
				// if toggled, remove new and add old
				if f&i != 0 {
					e.gen.RemoveOperator(new[idx])
					if err := e.gen.AddOperator(old[idx]); err != nil {
						return err
					}
				}
			}
			return nil
		}
	}

	return nil
}

func (e *NamespaceGenerationEvolver) downgradeAPIs() {
	// remove anything we can't satisfy after trying to downgrade all possible combinations
	e.gen.ResetUnchecked()
	for missingAPIs := e.gen.MissingAPIs(); len(missingAPIs) > 0; {
		requirers := missingAPIs.PopAPIRequirers()
		for _, op := range requirers {
			e.gen.RemoveOperator(op)
		}
	}
}
