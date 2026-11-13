package timefmt

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Parse time string using the format.
func Parse(source, format string) (time.Time, error) {
	return parse(source, format, time.UTC, time.Local)
}

// ParseInLocation parses time string with the default location.
// The location is also used to parse the time zone name (%Z).
func ParseInLocation(source, format string, loc *time.Location) (time.Time, error) {
	return parse(source, format, loc, loc)
}

func parse(source, format string, loc, base *time.Location) (time.Time, error) {
	t, err := parseTime(source, format, loc, base)
	if err != nil {
		err = fmt.Errorf("failed to parse %q with %q: %w", strings.Clone(source), format, err)
	}
	return t, err
}

func parseTime(source, format string, loc, base *time.Location) (time.Time, error) {
	year, month, day, hour, minute, second, nanosecond := 1900, 1, 0, 0, 0, 0, 0
	var j, week, weekday, yday, ampm, colons, sign int
	century, weekstart := -1, time.Weekday(-1)
	var hasISOYear, hasUnix, hasZoneName, hasZoneOffset bool
	var pending string
	var err error
	for i, l := 0, len(source); i < len(format); i++ {
		if b := format[i]; b == '%' {
			if i++; i == len(format) {
				return time.Time{}, errors.New(`stray "%"`)
			}
			b = format[i]
		L:
			switch b {
			case 'G':
				hasISOYear = true
				fallthrough
			case 'Y':
				sign, j = parseSign(source, j, l)
				if year, j, err = parseInt(source, j, 4, 0, 9999, b); err != nil {
					return time.Time{}, err
				}
				year *= sign
			case 'g':
				hasISOYear = true
				fallthrough
			case 'y':
				if year, j, err = parseInt(source, j, 2, 0, 99, b); err != nil {
					return time.Time{}, err
				}
				if year < 69 {
					year += 2000
				} else {
					year += 1900
				}
			case 'C':
				sign, j = parseSign(source, j, l)
				if sign < 0 {
					return time.Time{}, errors.New(`negative century is not supported for "%C"`)
				}
				if century, j, err = parseInt(source, j, 2, 0, 99, 'C'); err != nil {
					return time.Time{}, err
				}
			case 'm':
				if month, j, err = parseInt(source, j, 2, 1, 12, 'm'); err != nil {
					return time.Time{}, err
				}
			case 'B':
				if month, j, err = parseAny(source, j, longMonthNames, 'B'); err != nil {
					return time.Time{}, err
				}
			case 'b', 'h':
				if month, j, err = parseAny(source, j, shortMonthNames, b); err != nil {
					return time.Time{}, err
				}
			case 'A':
				if weekday, j, err = parseAny(source, j, longWeekNames, 'A'); err != nil {
					return time.Time{}, err
				}
			case 'a':
				if weekday, j, err = parseAny(source, j, shortWeekNames, 'a'); err != nil {
					return time.Time{}, err
				}
			case 'w':
				if weekday, j, err = parseInt(source, j, 1, 0, 6, 'w'); err != nil {
					return time.Time{}, err
				}
				weekday++
			case 'u':
				if weekday, j, err = parseInt(source, j, 1, 1, 7, 'u'); err != nil {
					return time.Time{}, err
				}
				weekday = weekday%7 + 1
			case 'V':
				if week, j, err = parseInt(source, j, 2, 1, 53, b); err != nil {
					return time.Time{}, err
				}
				weekstart = time.Thursday
				weekday = or(weekday, 2)
			case 'U':
				if week, j, err = parseInt(source, j, 2, 0, 53, b); err != nil {
					return time.Time{}, err
				}
				weekstart = time.Sunday
				weekday = or(weekday, 1)
			case 'W':
				if week, j, err = parseInt(source, j, 2, 0, 53, b); err != nil {
					return time.Time{}, err
				}
				weekstart = time.Monday
				weekday = or(weekday, 2)
			case 'e':
				if j < l && source[j] == ' ' {
					j++
				}
				fallthrough
			case 'd':
				if day, j, err = parseInt(source, j, 2, 1, 31, b); err != nil {
					return time.Time{}, err
				}
			case 'j':
				if yday, j, err = parseInt(source, j, 3, 1, 366, 'j'); err != nil {
					return time.Time{}, err
				}
			case 'k':
				if j < l && source[j] == ' ' {
					j++
				}
				fallthrough
			case 'H':
				if hour, j, err = parseInt(source, j, 2, 0, 23, b); err != nil {
					return time.Time{}, err
				}
			case 'l':
				if j < l && source[j] == ' ' {
					j++
				}
				fallthrough
			case 'I':
				if hour, j, err = parseInt(source, j, 2, 1, 12, b); err != nil {
					return time.Time{}, err
				}
				if hour == 12 {
					hour = 0
				}
			case 'P', 'p':
				if ampm, j, err = parseAny(source, j, []string{"AM", "PM"}, b); err != nil {
					return time.Time{}, err
				}
			case 'M':
				if minute, j, err = parseInt(source, j, 2, 0, 59, 'M'); err != nil {
					return time.Time{}, err
				}
			case 'S':
				if second, j, err = parseInt(source, j, 2, 0, 60, 'S'); err != nil {
					return time.Time{}, err
				}
			case 's':
				sign, j = parseSign(source, j, l)
				// 2147483647-12-31T23:59:59Z, the last second of the maximum year.
				maxUnix := int64(67767976233532799)
				if sign < 0 {
					// -2147483648-01-01T00:00:00Z, the first second of the
					// minimum year, negated since the sign is applied below.
					maxUnix = 67768100567971200
				}
				var unix int64
				if unix, j, err = parseInt64(source, j, maxUnix, 's'); err != nil {
					return time.Time{}, err
				}
				t := time.Unix(int64(sign)*unix, 0).In(time.UTC)
				var mon time.Month
				year, mon, day = t.Date()
				hour, minute, second = t.Clock()
				month, hasUnix = int(mon), true
			case 'f':
				microsecond, i := 0, j
				if microsecond, j, err = parseInt(source, j, 6, 0, 999999, 'f'); err != nil {
					return time.Time{}, err
				}
				for i = j - i; i < 6; i++ {
					microsecond *= 10
				}
				nanosecond = microsecond * 1000
			case 'Z':
				if j >= l {
					return time.Time{}, parseFormatError('Z')
				}
				i := j
				if source[j] == '+' || source[j] == '-' {
					for j++; j < l; j++ {
						if source[j]-'0' >= 10 {
							break
						}
					}
				} else if j+4 <= l && (source[j:j+4] == "ChST" || source[j:j+4] == "MeST") {
					j += 4
				} else {
					for ; j < l; j++ {
						if source[j]-'A' > 'Z'-'A' {
							break
						}
					}
				}
				name := strings.Clone(source[i:j])
				t, err := time.ParseInLocation("MST", name, base)
				if err != nil {
					return time.Time{}, parseFormatError('Z')
				}
				if hasZoneOffset {
					_, offset := locationZone(loc)
					loc = time.FixedZone(name, offset)
				} else {
					loc = t.Location()
				}
				hasZoneName = true
			case 'z':
				if j >= l {
					return time.Time{}, parseZFormatError(colons)
				}
				sign = 1
				switch source[j] {
				case '-':
					sign = -1
					fallthrough
				case '+':
					hour, minute, second, i := 0, 0, 0, j+1
					if hour, j, _ = parseInt(source, i, 2, 0, 23, 'z'); j != i+2 {
						return time.Time{}, parseZFormatError(colons)
					}
					if j >= l || source[j] != ':' {
						if colons > 0 && colons < 3 {
							return time.Time{}, expectedColonForZFormatError(colons)
						}
					} else if j++; colons == 0 {
						colons = 4
					}
					i = j
					if minute, j, _ = parseInt(source, i, 2, 0, 59, 'z'); j != i+2 {
						if colons > 0 && colons != 3 {
							return time.Time{}, parseZFormatError(colons & 3)
						}
						j = i
					} else if colons > 1 {
						if j >= l || source[j] != ':' {
							if colons < 3 {
								return time.Time{}, expectedColonForZFormatError(colons)
							}
						} else {
							i = j + 1
							if second, j, _ = parseInt(source, i, 2, 0, 59, 'z'); j != i+2 {
								if colons < 3 {
									return time.Time{}, parseZFormatError(colons)
								}
								j = i - 1
							}
						}
					}
					var name string
					if hasZoneName {
						name, _ = locationZone(loc)
					}
					loc, colons = time.FixedZone(name, sign*((hour*60+minute)*60+second)), 0
					hasZoneOffset = true
				case 'Z':
					loc, colons, j = time.UTC, 0, j+1
				default:
					return time.Time{}, parseZFormatError(colons)
				}
			case ':':
				if pending != "" {
					if j >= l || source[j] != b {
						return time.Time{}, expectedFormatError(b)
					}
					j++
				} else {
					for colons = 1; colons <= 3; colons++ {
						if i++; i == len(format) {
							break
						} else if b = format[i]; b == 'z' {
							goto L
						} else if b != ':' || colons == 3 {
							break
						}
					}
					return time.Time{}, expectedZAfterColonError(colons)
				}
			case 't', 'n':
				i := j
			K:
				for ; j < l; j++ {
					switch source[j] {
					case ' ', '\t', '\n', '\v', '\f', '\r':
					default:
						break K
					}
				}
				if i == j {
					return time.Time{}, fmt.Errorf(`expected a space for "%%%c"`, b)
				}
			case '%':
				if j >= l || source[j] != b {
					return time.Time{}, expectedFormatError(b)
				}
				j++
			case 'c':
				pending = "a b e H:M:S Y"
			case '+':
				pending = "a b e H:M:S Z Y"
			case 'v':
				pending = "e-b-Y"
			case 'r':
				pending = "I:M:S p"
			case 'F':
				pending = "Y-m-d"
			case 'D', 'x':
				pending = "m/d/y"
			case 'T', 'X':
				pending = "H:M:S"
			case 'R':
				pending = "H:M"
			default:
				if pending == "" {
					r, _ := utf8.DecodeRuneInString(format[i:])
					return time.Time{}, unexpectedFormatError(r)
				}
				if j >= l || source[j] != b {
					return time.Time{}, expectedFormatError(b)
				}
				j++
			}
			if pending != "" {
				b, pending = pending[0], pending[1:]
				goto L
			}
		} else if j >= l || source[j] != b {
			r, _ := utf8.DecodeRuneInString(format[i:])
			return time.Time{}, expectedFormatError(r)
		} else {
			j++
		}
	}
	if j < len(source) {
		return time.Time{}, fmt.Errorf("unparsed string %q", strings.Clone(source[j:]))
	}
	if ampm > 0 {
		hour = hour%12 + (ampm-1)*12
	}
	if century >= 0 {
		year = century*100 + year%100
	}
	switch {
	default:
		day = 1
		fallthrough
	case day > 0:
		if hasISOYear {
			return time.Time{}, errors.New(`use "%Y" to parse non-ISO year`)
		}
		if hasUnix {
			return time.Date(year, time.Month(month), day, hour, minute, second, nanosecond, time.UTC).In(loc), nil
		}
		return time.Date(year, time.Month(month), day, hour, minute, second, nanosecond, loc), nil
	case yday > 0:
		if hasISOYear {
			return time.Time{}, errors.New(`use "%Y" to parse non-ISO year for "%j"`)
		}
		return time.Date(year, time.January, yday, hour, minute, second, nanosecond, loc), nil
	case hasISOYear:
		if weekstart < time.Sunday {
			weekstart, week, weekday = time.Thursday, 1, or(weekday, 2)
		}
		fallthrough
	case weekstart >= time.Sunday:
		if (weekstart == time.Thursday) != hasISOYear {
			if hasISOYear {
				return time.Time{}, errors.New(`use "%Y" to parse non-ISO year for "%U" or "%W"`)
			}
			return time.Time{}, errors.New(`use "%G" to parse ISO year for "%V"`)
		}
		if weekstart > time.Sunday && weekday == 1 {
			week++
		}
		t := time.Date(year, time.January, -int(weekstart), 0, 0, 0, 0, time.UTC)
		return time.Date(year, time.January,
			week*7-int(weekstart)-int(t.Weekday())+weekday-1,
			hour, minute, second, nanosecond, loc), nil
	}
}

func locationZone(loc *time.Location) (string, int) {
	return time.Date(2000, time.January, 1, 0, 0, 0, 0, loc).Zone()
}

type parseFormatError byte

func (err parseFormatError) Error() string {
	return fmt.Sprintf(`cannot parse "%%%c"`, byte(err))
}

type expectedFormatError rune

func (err expectedFormatError) Error() string {
	return fmt.Sprintf("expected %q", rune(err))
}

type unexpectedFormatError rune

func (err unexpectedFormatError) Error() string {
	return fmt.Sprintf(`unexpected format "%%%c"`, rune(err))
}

type parseZFormatError int

func (err parseZFormatError) Error() string {
	return `cannot parse "%` + `:::z"`[3-err:]
}

type expectedColonForZFormatError int

func (err expectedColonForZFormatError) Error() string {
	return `expected ':' for "%` + `:::z"`[3-err:]
}

type expectedZAfterColonError int

func (err expectedZAfterColonError) Error() string {
	return `expected 'z' after "%` + `:::"`[3-err:]
}

func parseSign(source string, index, l int) (int, int) {
	if index < l && source[index] == '-' {
		return -1, index + 1
	}
	return 1, index
}

// parseInt parses an integer from source. This is intentionally not a
// wrapper of parseInt64 to avoid the overhead of int64 arithmetic and
// function call indirection in the hot path (~20% slower in benchmarks).
func parseInt(source string, index, size, minimum, maximum int, format byte) (int, int, error) {
	value, i := 0, index
	for l := min(index+size, len(source)); i < l; i++ {
		if b := source[i] - '0'; b < 10 {
			value = value*10 + int(b)
		} else {
			break
		}
	}
	if i == index || value < minimum || maximum < value {
		return 0, 0, parseFormatError(format)
	}
	return value, i, nil
}

// parseInt64 parses an integer from source. The maximum value is limited so
// that the year of the parsed time fits in int on 32-bit architectures, and
// the accumulation never overflows int64. The number of digits needs no limit
// because the maximum value bounds it.
func parseInt64(source string, index int, maximum int64, format byte) (int64, int, error) {
	value, i := int64(0), index
	for ; i < len(source); i++ {
		if b := source[i] - '0'; b < 10 {
			value = value*10 + int64(b)
			if value > maximum {
				return 0, 0, parseFormatError(format)
			}
		} else {
			break
		}
	}
	if i == index {
		return 0, 0, parseFormatError(format)
	}
	return value, i, nil
}

func parseAny(source string, index int, candidates []string, format byte) (int, int, error) {
L:
	for i, xs := range candidates {
		j := index
		for k := 0; k < len(xs); k, j = k+1, j+1 {
			if j >= len(source) {
				continue L
			}
			if x, y := xs[k], source[j]; x != y && x|0x20 != y|0x20 {
				continue L
			}
		}
		return i + 1, j, nil
	}
	return 0, 0, parseFormatError(format)
}
