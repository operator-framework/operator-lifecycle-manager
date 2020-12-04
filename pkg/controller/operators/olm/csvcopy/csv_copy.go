package csvcopy

import "strconv"

const DisableCSVCopyAnnotation = "operatorgroup.operators.coreos.com/csv-copy"

type Setting int

const (
	Disabled Setting = iota
	Enabled
	Spec
)

func (c Setting) String() string {
	return [...]string{"Disabled", "Enabled", "Spec"}[c]
}

func (c *Setting) Set(value string) Setting {
	i, err := strconv.Atoi(value)
	if err != nil {
		i = 1
	}
	*c = Setting(i)
	return *c
}

func New() *Setting {
	var c Setting
	return &c
}
