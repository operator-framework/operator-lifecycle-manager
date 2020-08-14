package sql

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	keywords = map[string]int{
		"ACTION":        ACTION,
		"AND":           AND,
		"ASC":           ASC,
		"AUTOINCREMENT": AUTOINCREMENT,
		"CASCADE":       CASCADE,
		"CHECK":         CHECK,
		"COLLATE":       COLLATE,
		"CONFLICT":      CONFLICT,
		"CONSTRAINT":    CONSTRAINT,
		"CREATE":        CREATE,
		"DEFAULT":       DEFAULT,
		"DEFERRABLE":    DEFERRABLE,
		"DEFERRED":      DEFERRED,
		"DELETE":        DELETE,
		"DESC":          DESC,
		"FOREIGN":       FOREIGN,
		"FROM":          FROM,
		"GLOB":          GLOB,
		"INDEX":         INDEX,
		"IN":            IN,
		"INITIALLY":     INITIALLY,
		"IS":            IS,
		"KEY":           KEY,
		"LIKE":          LIKE,
		"MATCH":         MATCH,
		"NO":            NO,
		"NOT":           NOT,
		"NULL":          NULL,
		"ON":            ON,
		"OR":            OR,
		"PRIMARY":       PRIMARY,
		"REFERENCES":    REFERENCES,
		"REGEXP":        REGEXP,
		"REPLACE":       REPLACE,
		"RESTRICT":      RESTRICT,
		"ROWID":         ROWID,
		"SELECT":        SELECT,
		"SET":           SET,
		"TABLE":         TABLE,
		"UNIQUE":        UNIQUE,
		"UPDATE":        UPDATE,
		"WHERE":         WHERE,
		"WITHOUT":       WITHOUT,
	}
	operators = map[string]struct{}{
		"||": struct{}{},
		">=": struct{}{},
		"<=": struct{}{},
		"==": struct{}{},
		"!=": struct{}{},
		"<>": struct{}{},
		">>": struct{}{},
		"<<": struct{}{},
	}
)

type token struct {
	typ int
	s   string
	n   int64
	f   float64
}

func stoken(typ int, s string) token {
	return token{
		typ: typ,
		s:   s,
	}
}

func ntoken(n int64) token {
	return token{
		typ: tSignedNumber,
		n:   n,
	}
}

func ftoken(f float64) token {
	return token{
		typ: tFloat,
		f:   f,
	}
}

func optoken(s string) token {
	return token{
		typ: tOperator,
		s:   s,
	}
}

func tokenize(s string) ([]token, error) {
	var res []token
	for i := 0; ; {
		if i >= len(s) {
			return res, nil
		}
		c, l := utf8.DecodeRuneInString(s[i:])

		switch {
		case unicode.IsSpace(c):
			// ignore
		case unicode.IsLetter(c) || c == '_':
			bt, bl := readBareword(s[i:])
			tnr := tBare
			if n, ok := keywords[strings.ToUpper(bt)]; ok {
				tnr = n
			}
			res = append(res, stoken(tnr, bt))
			i += bl - 1
		case unicode.IsDigit(c) || c == '.':
			tok, l := readNumericLiteral(s[i:])
			if l < 0 {
				return res, errors.New("unsupported number")

			}
			res = append(res, tok)
			i += l - 1
		default:
			switch c {
			case '>', '<', '|', '*', '/', '%', '&', '=', '!':
				op := readOp(s[i:])
				res = append(res, stoken(tOperator, op))
				i += len(op) - 1
			case '(', ')', ',', '+', '-', '~':
				// + and - might be binary or unary. let the lexer figure that out
				res = append(res, stoken(int(c), string(c)))
			case '\'':
				bt, bl := readQuoted('\'', s[i+1:], true)
				if bl == -1 {
					return res, errors.New("no terminating ' found")
				}
				res = append(res, stoken(tLiteral, bt))
				i += bl
			case '"', '`', '[':
				close := c
				allowEscape := true
				if close == '[' {
					close = ']'
					allowEscape = false
				}
				bt, bl := readQuoted(close, s[i+1:], allowEscape)
				if bl == -1 {
					return res, fmt.Errorf("no terminating %q found", close)
				}
				res = append(res, stoken(tIdentifier, bt))
				i += bl
			default:
				return nil, fmt.Errorf("unexpected char at pos:%d: %q", i, c)
			}
		}
		i += l
	}
}

func readBareword(s string) (string, int) {
	for i, r := range s {
		switch {
		case unicode.IsLetter(r):
		case i > 0 && unicode.IsDigit(r):
		case r == '_':
		default:
			return s[:i], i
		}
	}
	return s, len(s)
}

func readOp(s string) string {
	if len(s) == 1 {
		return s
	}
	if _, ok := operators[s[:2]]; ok {
		return s[:2]
	}
	return s[:1]
}

func readNumericLiteral(s string) (token, int) {
	float := false
	hex := false
	allowSigns := false
loop:
	for i, r := range s {
		switch {
		case unicode.IsDigit(r):
		case (r == '-' || r == '+') && allowSigns:
			allowSigns = false
		case r == 'x' || r == 'X':
			hex = true
		case r == '.':
			float = true
		case r == 'e' || r == 'E':
			float = true
			allowSigns = true // only after an 'e' we accept a sign
		default:
			s = s[:i]
			break loop
		}
	}
	if hex && (strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X")) {
		n, err := strconv.ParseUint(s[2:], 16, 64)
		if err != nil {
			return token{}, -1
		}
		// 64-bit two's-complement
		return ntoken(int64(n)), len(s)
	}
	if float {
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return token{}, -1
		}
		return ftoken(f), len(s)
	}
	n, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return token{}, -1
	}
	return ntoken(n), len(s)
}

// parse a quoted string until `close`. Opening char is already gone.
// >  A single quote within the string can be encoded by putting two single
// > quotes in a row - as in Pascal. C-style escapes using the backslash
// > character are not supported because they are not standard SQL.
func readQuoted(close rune, s string, allowEscape bool) (string, int) {
	for i, r := range s {
		switch r {
		case close:
			if allowEscape && len(s) > i+1 && rune(s[i+1]) == close {
				ss, si := readQuoted(close, s[i+2:], allowEscape)
				return s[:i+1] + ss, i + si + 2
			}
			return s[:i], i + 1
		default:
		}
	}
	return "", -1
}
