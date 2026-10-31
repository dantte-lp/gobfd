// Package toolprofile defines shared source-build contracts for managed development tools.
package toolprofile

// Frozen Syft source and build inputs shared by CI and release callers.
const (
	SyftModule       = "github.com/anchore/syft"
	SyftCommand      = SyftModule + "/cmd/syft"
	SyftManifest     = "tools/syft/go.mod"
	SyftBuildTags    = "containers_image_openpgp,exclude_graphdriver_btrfs"
	SyftSourceCommit = "f15c3343e29dfb20cf6d82c820e7090b49808c96"
	SyftSourceSum    = "h1:IeBZJrF/g75ba3k0ITQ8v4NCVGiGsWU3Wa1P/eSAFa0="
)
