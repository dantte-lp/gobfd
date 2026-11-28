// Package toolprofile defines shared source-build contracts for managed development tools.
package toolprofile

// Frozen Syft source and build inputs shared by CI and release callers.
const (
	SyftModule       = "github.com/anchore/syft"
	SyftCommand      = SyftModule + "/cmd/syft"
	SyftManifest     = "tools/syft/go.mod"
	SyftBuildTags    = "containers_image_openpgp,exclude_graphdriver_btrfs"
	SyftSourceCommit = "1a72047fd46c72e0a12ee77e24b23d2316753d08"
	SyftSourceSum    = "h1:Vm+22QHzQfwy9WBlWeGOWBSByH6dt6SEjxrfOvQZsfM="
)
