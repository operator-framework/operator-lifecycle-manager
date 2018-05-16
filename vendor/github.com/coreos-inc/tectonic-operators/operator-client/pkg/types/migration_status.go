package types

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// MigrationStage represents where in the process of an upgrade
// the migration is ran.
type MigrationStage string

const (
	// MigrationStatusKind is the Kind of the MigrationStatus CRD.
	MigrationStatusKind = "MigrationStatus"
	// MigrationAPIGroup is the API Group for MigrationStatus CRD.
	MigrationAPIGroup = "kvo.coreos.com"
	// MigrationGroupVersion is the group version for MigrationStatus CRD.
	MigrationGroupVersion = "v1"
)

// Old constants for TPRs.
// TODO(yifan): DEPRECATED, remove after TPR->CRD migration is completed.
const (
	// MigrationTPRAPIGroup is the API Group for MigrationStatus TPR.
	MigrationTPRAPIGroup = "coreos.com"

	// MigrationTPRGroupVersion is the group version for MigrationStatus TPR.
	MigrationTPRGroupVersion = "v1"
)

const (
	// MigrationStageBefore is Migrations ran before update.
	MigrationStageBefore MigrationStage = "lastBeforeMigrationRan"
	// MigrationStageDuring is Migrations ran during update.
	MigrationStageDuring MigrationStage = "lastDuringMigrationRan"
	// MigrationStageAfter is Migrations ran after update.
	MigrationStageAfter MigrationStage = "lastAfterMigrationRan"
)

// MigrationStatus represents the 3rd Party API Object.
type MigrationStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Versions MigrationVersions `json:"versions,omitempty"`
}

// MigrationVersions represents the migrations that have already
// been run for a specific version. The format of the map is:
// version -> "before/after" -> N (where N is the last
// migration that was run successfully).
type MigrationVersions map[string]*MigrationVersion

// MigrationVersion represents a migration for a given version.
// Eeach field holds the last migration that was successfully ran.
type MigrationVersion struct {
	LastBeforeMigrationRan *int `json:"lastBeforeMigrationRan,omitempty"`
	LastDuringMigrationRan *int `json:"lastDuringMigrationRan,omitempty"`
	LastAfterMigrationRan  *int `json:"lastAfterMigrationRan,omitempty"`
}
