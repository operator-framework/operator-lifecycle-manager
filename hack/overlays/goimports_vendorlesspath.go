package imports

import "strings"

// VendorlessPath returns ipath with any vendor prefixes trimmed.
func VendorlessPath(ipath string) string {
	if i := strings.LastIndex(ipath, "/vendor/"); i >= 0 {
		return ipath[i+len("/vendor/"):]
	}
	if strings.HasPrefix(ipath, "vendor/") {
		return ipath[len("vendor/"):]
	}
	return ipath
}
