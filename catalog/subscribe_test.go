package catalog

import (
	"fmt"
	"testing"

	"github.com/coreos/go-semver/semver"
)

type mockSubscription struct {
	appType        string
	currentVersion semver.Version
}

func (sub *mockSubscription) AppType() string {
	return sub.appType
}
func (sub *mockSubscription) CurrentVersion() semver.Version {
	return sub.currentVersion
}
func newMockSub(apptype string, ver string) *mockSubscription {
	return &mockSubscription{apptype, semver.New(ver)}
}

type memCatalog struct {
	versions     map[string]semver.Versions
	declarations map[string]map[semver.Version]InstallDeclaration
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

func TestCatalog(t *testing.T) {
	return
}
