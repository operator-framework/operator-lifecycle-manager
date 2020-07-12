package containertools

type BuildOptions struct {
	format     string
	tags       []string
	dockerfile string
	context    string
}

func (o *BuildOptions) SetFormatDocker() {
	o.format = "docker"
}

func (o *BuildOptions) SetFormatOCI() {
	o.format = "oci"
}

func (o *BuildOptions) AddTag(tag string) {
	o.tags = append(o.tags, tag)
}

func (o *BuildOptions) SetDockerfile(dockerfile string) {
	o.dockerfile = dockerfile
}

func (o *BuildOptions) SetContext(context string) {
	o.context = context
}

func DefaultBuildOptions() BuildOptions {
	var o BuildOptions
	o.SetFormatDocker()
	o.SetContext(".")
	return o
}
