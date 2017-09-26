package catalog

import (
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
)

// TEMP - unnecessary once Subscription type implemented directly
type mockSubscription struct {
	appType        string
	currentVersion semver.Version
	namespace      string
}

func (sub *mockSubscription) AppType() string {
	return sub.appType
}
func (sub *mockSubscription) CurrentVersion() semver.Version {
	return sub.currentVersion
}
func (sub *mockSubscription) Namespace() semver.Version {
	return sub.namespace
}
func newMockSub(apptype string, ver string) *mockSubscription {
	return &mockSubscription{apptype, semver.New(ver)}
}

// memCatalog is a rough mock catalog that holds apps and declarations in a map
type memCatalog struct {
	versions     map[string]semver.Versions
	declarations map[string]map[string]InstallDeclaration
}

func latest(verlist semver.Versions) (semver.Version, ok) {
	var latest semver.Version
	if verlist.Len() < 1 {
		return latest, false
	}
	semver.Sort(verlist)
	latest = verlist[verlist.Len()-1]
	return latest, true
}

func (cat *memCatalog) FetchLatestVersion(apptype string) (*semver.Version, error) {
	versions, ok := cat.versions[apptype]
	if !ok {
		return nil, fmt.Errorf("unknown apptype: %s", apptype)
	}
}

func (cat *memCatalog) FetchInstallDeclarationForAppVersion(apptype string, ver *semver.Version) (*InstallDeclaration, error) {
	versions, ok := cat.versions[apptype]
	if !ok {
		return nil, fmt.Errorf("unknown apptype: %s", apptype)
	}
	decl, ok := versions[ver.String()]
	if !ok {
		return nil, fmt.Errorf("unknown version %s for app: %s", ver.String(), apptype)
	}
	return decl, nil
}

func TestCatalog(t *testing.T) {
	return
}
