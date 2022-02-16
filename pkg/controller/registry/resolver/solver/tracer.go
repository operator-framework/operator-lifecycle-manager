package solver

import (
	"fmt"
	"io"
)

type SearchPosition interface {
	Variables() []Variable
	Conflicts() []AppliedConstraint
}

type Tracer interface {
	Trace(p SearchPosition)
}

type DefaultTracer struct{}

func (DefaultTracer) Trace(_ SearchPosition) {
}

type LoggingTracer struct {
	Writer io.Writer
}

func (t LoggingTracer) Trace(p SearchPosition) {
	fmt.Fprintf(t.Writer, "---\nAssumptions:\n")
	for _, i := range p.Variables() {
		fmt.Fprintf(t.Writer, "- %s\n", i.Identifier())
	}
	fmt.Fprintf(t.Writer, "Conflicts:\n")
	for _, a := range p.Conflicts() {
		fmt.Fprintf(t.Writer, "- %s\n", a)
	}
}
