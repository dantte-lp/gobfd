package toolprofile

// GoReleaserModule and GoReleaserSource locate the managed publisher source contract.
const (
	GoReleaserModule       = "github.com/goreleaser/goreleaser/v2"
	GoReleaserSource       = "tools/goreleaser/source.json"
	GoReleaserConsumer     = "github.com/dantte-lp/gobfd/tools/goreleaser"
	GoReleaserQualifiedMod = "tools/goreleaser/qualified.mod"
	GoReleaserQualifiedSum = "tools/goreleaser/qualified.sum"
	RefmtModule            = "github.com/polydawn/refmt"
)
