# Changelog
## [v0.1.9](https://github.com/itchyny/timefmt-go/compare/v0.1.8..v0.1.9) (2026-10-01)
* fix parsing `%p` to reinterpret the hour on a 12-hour clock
* fix parsing `%s` to validate the range of Unix time
* fix parsing `%s` in a location or with `%z` to keep the absolute time
* fix parsing `%G` and `%g` without `%V` to start at the first week of the ISO year
* fix parsing `%G` and `%g` to return error with non-ISO date directives
* fix parsing week date directives (`%V`, `%U`, `%W`) with leap second
* fix parsing time zone name of `%Z` following `%z`
* fix parsing `%Z` for numeric and mixed case time zone abbreviations (`+07`, `ChST`)
* fix parsing `%Z` not to retain the source string in the time zone name
* fix `%^Z` and `%#Z` mangling digits and symbols in time zone name
* fix `%#Z` panicking on a single-character time zone name
* fix error messages of non-ASCII format directives and characters
* improve performance of parsing by avoiding `defer` (10-28% less time in benchmarks)
* improve performance of parsing a byte slice converted to a string (no allocation up to 32 bytes)
* improve performance of formatting by emitting digits at once (2-6% less time in benchmarks)

## [v0.1.8](https://github.com/itchyny/timefmt-go/compare/v0.1.7..v0.1.8) (2026-04-01)
* fix parsing negative year and Unix time (`%Y`, `%G`, `%s`)
* fix formatting negative year, century, Unix time (`%Y`, `%G`, `%C`, `%y`, `%g`, `%s`)
* fix `%g` parsing to use the same two-digit year threshold 69 as `%y`
* fix `%s` formatting and parsing on 32-bit platforms
* support parsing time zone offset with `%:::z`
* improve performance of parsing/formatting compound directives

## [v0.1.7](https://github.com/itchyny/timefmt-go/compare/v0.1.6..v0.1.7) (2025-10-01)
* refactor code using built-in `min` and `max` functions

## [v0.1.6](https://github.com/itchyny/timefmt-go/compare/v0.1.5..v0.1.6) (2024-06-01)
* support parsing week directives (`%A`, `%a`, `%w`, `%u`, `%V`, `%U`, `%W`)
* validate range of values on parsing directives
* fix formatting `%l` to show `12` at midnight

## [v0.1.5](https://github.com/itchyny/timefmt-go/compare/v0.1.4..v0.1.5) (2022-12-01)
* support parsing time zone offset with name using both `%z` and `%Z`

## [v0.1.4](https://github.com/itchyny/timefmt-go/compare/v0.1.3..v0.1.4) (2022-09-01)
* improve documents
* drop support for Go 1.16

## [v0.1.3](https://github.com/itchyny/timefmt-go/compare/v0.1.2..v0.1.3) (2021-04-14)
* implement `ParseInLocation` for configuring the default location

## [v0.1.2](https://github.com/itchyny/timefmt-go/compare/v0.1.1..v0.1.2) (2021-02-22)
* implement parsing/formatting time zone offset with colons (`%:z`, `%::z`, `%:::z`)
* recognize `Z` as UTC on parsing time zone offset (`%z`)
* fix padding on formatting time zone offset (`%z`)

## [v0.1.1](https://github.com/itchyny/timefmt-go/compare/v0.1.0..v0.1.1) (2020-09-01)
* fix overflow check in 32-bit architecture

## [v0.1.0](https://github.com/itchyny/timefmt-go/compare/2c02364..v0.1.0) (2020-08-16)
* initial implementation
