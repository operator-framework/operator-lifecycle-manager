package internal

import (
	"testing"

	"github.com/operator-framework/api/pkg/validation/errors"

	"github.com/stretchr/testify/require"
)

type validatorFuncTest struct {
	description       string
	wantErr, wantWarn bool
	errors            []errors.Error
}

func (c validatorFuncTest) check(t *testing.T, result errors.ManifestResult) {
	if c.wantErr {
		if !result.HasError() {
			t.Errorf("%s: expected errors %#v, got nil", c.description, c.errors)
		} else {
			errs, _ := splitErrorsWarnings(c.errors)
			checkErrorsMatch(t, errs, result.Errors)
		}
	}
	if c.wantWarn {
		if !result.HasWarn() {
			t.Errorf("%s: expected warnings %#v, got nil", c.description, c.errors)
		} else {
			_, warns := splitErrorsWarnings(c.errors)
			checkErrorsMatch(t, warns, result.Warnings)
		}
	}
	if !c.wantErr && !c.wantWarn && (result.HasError() || result.HasWarn()) {
		t.Errorf("%s: expected no errors or warnings, got:\n%v", c.description, result)
	}
}

func splitErrorsWarnings(all []errors.Error) (errs, warns []errors.Error) {
	for _, a := range all {
		if a.Level == errors.LevelError {
			errs = append(errs, a)
		} else {
			warns = append(warns, a)
		}
	}
	return
}

func checkErrorsMatch(t *testing.T, errs1, errs2 []errors.Error) {
	// Do string matching on error types for test purposes.
	for i, err := range errs1 {
		if badErr, ok := err.BadValue.(error); ok && badErr != nil {
			errs1[i].BadValue = badErr.Error()
		}
	}
	for i, err := range errs2 {
		if badErr, ok := err.BadValue.(error); ok && badErr != nil {
			errs2[i].BadValue = badErr.Error()
		}
	}
	require.ElementsMatch(t, errs1, errs2)
}
