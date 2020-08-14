package sql

import (
	"errors"
)

type lexer struct {
	tokens []token
	result interface{}
	err    error
}

func (l *lexer) Lex(lval *yySymType) int {
	if len(l.tokens) == 0 {
		return 0
	}
	tok := l.tokens[0]
	l.tokens = l.tokens[1:]

	lval.identifier = tok.s
	lval.signedNumber = tok.n
	lval.float = tok.f
	return tok.typ
}

func (l *lexer) Error(e string) {
	l.err = errors.New(e)
}
