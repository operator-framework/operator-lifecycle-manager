package action

import (
	"fmt"
	"io"

	"github.com/h2non/filetype"

	"github.com/operator-framework/operator-registry/alpha/declcfg"
)

type Init struct {
	Package           string
	DefaultChannel    string
	DescriptionReader io.Reader
	IconReader        io.Reader
}

func (i Init) Run() (*declcfg.Package, error) {
	pkg := &declcfg.Package{
		// TODO(joelanford): Use a constant for "olm.package"
		Schema:         "olm.package",
		Name:           i.Package,
		DefaultChannel: i.DefaultChannel,
	}
	if i.DescriptionReader != nil {
		descriptionData, err := io.ReadAll(i.DescriptionReader)
		if err != nil {
			return nil, fmt.Errorf("read description: %v", err)
		}
		pkg.Description = string(descriptionData)
	}

	if i.IconReader != nil {
		iconData, err := io.ReadAll(i.IconReader)
		if err != nil {
			return nil, fmt.Errorf("read icon: %v", err)
		}
		iconType, err := filetype.Match(iconData)
		if err != nil {
			return nil, fmt.Errorf("detect icon mediatype: %v", err)
		}
		if iconType.MIME.Type != "image" {
			return nil, fmt.Errorf("detected invalid type %q: not an image", iconType.MIME.Value)
		}
		pkg.Icon = &declcfg.Icon{
			Data:      iconData,
			MediaType: iconType.MIME.Value,
		}
	}
	return pkg, nil
}
