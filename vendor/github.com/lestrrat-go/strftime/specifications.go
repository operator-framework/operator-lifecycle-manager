package strftime

import (
	"sync"

	"github.com/pkg/errors"
)

// because there is no such thing was a sync.RWLocker
type rwLocker interface {
	RLock()
	RUnlock()
	sync.Locker
}

// SpecificationSet is a container for patterns that Strftime uses.
// If you want a custom strftime, you can copy the default
// SpecificationSet and tweak it
type SpecificationSet interface {
	Lookup(byte) (Appender, error)
	Delete(byte) error
	Set(byte, Appender) error
}

type specificationSet struct {
	mutable bool
	lock      rwLocker
	store     map[byte]Appender
}

// The default specification set does not need any locking as it is never
// accessed from the outside, and is never mutated.
var defaultSpecificationSet SpecificationSet

func init() {
	defaultSpecificationSet = newImmutableSpecificationSet()
}

func newImmutableSpecificationSet() SpecificationSet {
	// Create a mutable one so that populateDefaultSpecifications work through
	// its magic, then copy the associated map
	// (NOTE: this is done this way because there used to be
	// two struct types for specification set, united under an interface.
	// it can now be removed, but we would need to change the entire
	// populateDefaultSpecifications method, and I'm currently too lazy
	// PRs welcome)
	tmp := NewSpecificationSet()

	ss := &specificationSet{
		mutable: false,
		lock:      nil, // never used, so intentionally not initialized
		store:     tmp.(*specificationSet).store,
	}

	return ss
}

// NewSpecificationSet creates a specification set with the default specifications.
func NewSpecificationSet() SpecificationSet {
	ds := &specificationSet{
		mutable: true,
		lock:      &sync.RWMutex{},
		store:     make(map[byte]Appender),
	}
	populateDefaultSpecifications(ds)

	return ds
}

func populateDefaultSpecifications(ds SpecificationSet) {
	ds.Set('A', fullWeekDayName)
	ds.Set('a', abbrvWeekDayName)
	ds.Set('B', fullMonthName)
	ds.Set('b', abbrvMonthName)
	ds.Set('C', centuryDecimal)
	ds.Set('c', timeAndDate)
	ds.Set('D', mdy)
	ds.Set('d', dayOfMonthZeroPad)
	ds.Set('e', dayOfMonthSpacePad)
	ds.Set('F', ymd)
	ds.Set('H', twentyFourHourClockZeroPad)
	ds.Set('h', abbrvMonthName)
	ds.Set('I', twelveHourClockZeroPad)
	ds.Set('j', dayOfYear)
	ds.Set('k', twentyFourHourClockSpacePad)
	ds.Set('l', twelveHourClockSpacePad)
	ds.Set('M', minutesZeroPad)
	ds.Set('m', monthNumberZeroPad)
	ds.Set('n', newline)
	ds.Set('p', ampm)
	ds.Set('R', hm)
	ds.Set('r', imsp)
	ds.Set('S', secondsNumberZeroPad)
	ds.Set('T', hms)
	ds.Set('t', tab)
	ds.Set('U', weekNumberSundayOrigin)
	ds.Set('u', weekdayMondayOrigin)
	ds.Set('V', weekNumberMondayOriginOneOrigin)
	ds.Set('v', eby)
	ds.Set('W', weekNumberMondayOrigin)
	ds.Set('w', weekdaySundayOrigin)
	ds.Set('X', natReprTime)
	ds.Set('x', natReprDate)
	ds.Set('Y', year)
	ds.Set('y', yearNoCentury)
	ds.Set('Z', timezone)
	ds.Set('z', timezoneOffset)
	ds.Set('%', percent)
}

func (ds *specificationSet) Lookup(b byte) (Appender, error) {
	if ds.mutable {
		ds.lock.RLock()
		defer ds.lock.RLock()
	}
	v, ok := ds.store[b]
	if !ok {
		return nil, errors.Errorf(`lookup failed: '%%%c' was not found in specification set`, b)
	}
	return v, nil
}

func (ds *specificationSet) Delete(b byte) error {
	if !ds.mutable {
		return errors.New(`delete failed: this specification set is marked immutable`)
	}

	ds.lock.Lock()
	defer ds.lock.Unlock()
	delete(ds.store, b)
	return nil
}

func (ds *specificationSet) Set(b byte, a Appender) error {
	if !ds.mutable {
		return errors.New(`set failed: this specification set is marked immutable`)
	}

	ds.lock.Lock()
	defer ds.lock.Unlock()
	ds.store[b] = a
	return nil
}
