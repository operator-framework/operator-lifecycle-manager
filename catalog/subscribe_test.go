package catalog

import (
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
)

// TEMP - unnecessary once Subscription type implemented directly
type mockSubscription struct {
	appType        string
	currentVersion *semver.Version
	namespace      string
}

func (sub *mockSubscription) AppType() string {
	return sub.appType
}
func (sub *mockSubscription) CurrentVersion() *semver.Version {
	return sub.currentVersion
}
func (sub *mockSubscription) Namespace() string {
	return sub.namespace
}
func newMockSub(apptype, ver, ns string) *mockSubscription {
	return &mockSubscription{apptype, semver.New(ver), ns}
}

// memCatalog is a rough mock catalog that holds apps and declarations in a map
type memCatalog struct {
	versions     map[string]semver.Versions
	declarations map[string]map[string]InstallDeclaration
}

func latest(verlist semver.Versions) (*semver.Version, bool) {
	if verlist.Len() < 1 {
		return nil, false
	}
	semver.Sort(verlist)
	return verlist[verlist.Len()-1], true
}

func (cat *memCatalog) FetchLatestVersion(apptype string) (*semver.Version, error) {
	versions, ok := cat.versions[apptype]
	if !ok {
		return nil, fmt.Errorf("unknown apptype: %s", apptype)
	}
	ver, ok := latest(versions)
	if !ok {
		return nil, fmt.Errorf("cannot find valid version for apptype %s", apptype)
	}
	return ver, nil
}

func (cat *memCatalog) FetchInstallDeclarationForAppVersion(apptype string, ver *semver.Version) (*InstallDeclaration, error) {
	appversions, ok := cat.declarations[apptype]
	if !ok {
		return nil, fmt.Errorf("unknown apptype: %s", apptype)
	}
	decl, ok := appversions[ver.String()]
	if !ok {
		return nil, fmt.Errorf("unknown version %s for app: %s", ver.String(), apptype)
	}
	return &decl, nil
}

func (cat *memCatalog) ResolveDependencies(decl *InstallDeclaration) error {
	// you don't get no dependencies!
	return nil
}

func TestCatalog(t *testing.T) {
	return
}
