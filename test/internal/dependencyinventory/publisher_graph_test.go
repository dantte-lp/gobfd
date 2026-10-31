package dependencyinventory

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/dantte-lp/gobfd/test/internal/cirunner"
)

const publisherGraphFixture = `{"Path":"github.com/dantte-lp/gobfd/tools/goreleaser","Main":true}
{"Path":"github.com/goreleaser/goreleaser/v2","Version":"v2.19.0","Replace":{"Path":"../source"}}
{"Path":"github.com/polydawn/refmt","Version":"v0.90.0","Replace":{"Path":"../refmt-local"}}
{"Path":"example.invalid/dependency","Version":"v1.0.0","Indirect":true,
"Replace":{"Path":"example.invalid/dependency","Version":"v1.1.0"}}`

func TestPublisherGraphUsesAuthenticatedAncestryAndEffectiveRemoteVersions(t *testing.T) {
	t.Parallel()
	modules, err := parseSelectedModuleGraph([]byte(publisherGraphFixture), goreleaserGraphID)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 3 || modules[0].Version != "v2.19.0" || modules[1].Version != "v0.90.0" ||
		modules[2].Version != "v1.1.0" || !modules[2].Indirect {
		t.Fatalf("publisher graph = %#v", modules)
	}
}

func TestPublisherGraphRejectsUnknownOrIncompleteSources(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"empty":           "",
		"wrong consumer":  strings.Replace(publisherGraphFixture, "gobfd/tools/goreleaser", "gobfd/tools/other", 1),
		"absolute source": strings.Replace(publisherGraphFixture, "../source", "/tmp/source", 1),
		"cleaned alias":   strings.Replace(publisherGraphFixture, "../source", "../other/../source", 1),
		"foreign source":  strings.Replace(publisherGraphFixture, "../refmt-local", "../foreign", 1),
		"remote instead of local": strings.Replace(publisherGraphFixture, `"Path":"../source"`,
			`"Path":"github.com/goreleaser/goreleaser/v2","Version":"v2.19.0"`, 1),
		"duplicate field": strings.Replace(publisherGraphFixture, `"Version":"v2.19.0"`,
			`"Version":"v2.18.0","Version":"v2.19.0"`, 1),
		"duplicate module": publisherGraphFixture + "\n" + `{"Path":"example.invalid/dependency","Version":"v1.1.0"}`,
		"go error": strings.Replace(publisherGraphFixture, `"Version":"v2.19.0"`,
			`"Version":"v2.19.0","Error":{"Err":"cannot load module"}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseSelectedModuleGraph([]byte(data), goreleaserGraphID); err == nil {
				t.Fatal("unqualified publisher graph accepted")
			}
		})
	}
	if _, err := parseSelectedModuleGraph([]byte(publisherGraphFixture), "tools"); err == nil {
		t.Fatal("publisher local replacement accepted in another graph")
	}
}

type graphWorkspaceFixture struct {
	verifications int
	failAfter     int
	failure       error
}

func (*graphWorkspaceFixture) Directory() (string, error)     { return "/owned/stage", nil }
func (*graphWorkspaceFixture) Environment() ([]string, error) { return []string{"GOWORK=off"}, nil }
func (workspace *graphWorkspaceFixture) Verify() error {
	workspace.verifications++
	if workspace.failAfter != 0 && workspace.verifications >= workspace.failAfter {
		return workspace.failure
	}
	return nil
}

type graphRunnerFunc func(context.Context, cirunner.CommandSpec) error

func (runner graphRunnerFunc) RunCommand(ctx context.Context, spec cirunner.CommandSpec) error {
	return runner(ctx, spec)
}

func TestPublisherCollectionVerifiesAfterChildFailure(t *testing.T) {
	t.Parallel()
	commandErr := errors.New("go list failed")
	identityErr := errors.New("source identity changed")
	workspace := &graphWorkspaceFixture{failAfter: 2, failure: identityErr}
	_, err := collectPublisherModules(context.Background(), workspace,
		graphRunnerFunc(func(_ context.Context, spec cirunner.CommandSpec) error {
			if spec.Dir != "/owned/stage" || strings.Join(spec.Args, " ") != "list -mod=readonly -m -json all" ||
				strings.Join(spec.Env, " ") != "GOWORK=off" || spec.WaitDelay == 0 {
				t.Fatalf("graph command = %#v", spec)
			}
			return commandErr
		}))
	if !errors.Is(err, commandErr) || !errors.Is(err, identityErr) || workspace.verifications != 2 {
		t.Fatalf("error = %v, verifications = %d", err, workspace.verifications)
	}
}

func TestPublisherCollectionUsesActualChildOutput(t *testing.T) {
	t.Parallel()
	workspace := &graphWorkspaceFixture{}
	graph, err := CollectGoReleaserGraph(context.Background(), workspace,
		graphRunnerFunc(func(_ context.Context, spec cirunner.CommandSpec) error {
			_, err := io.WriteString(spec.Stdout, publisherGraphFixture)
			return err
		}))
	if err != nil || graph.ID != goreleaserGraphID || len(graph.Modules) != 3 || workspace.verifications != 2 {
		t.Fatalf("graph = %#v, error = %v, verifications = %d", graph, err, workspace.verifications)
	}
}

func TestPublisherGraphOutputRejectsOverflowWithoutPartialWrite(t *testing.T) {
	t.Parallel()
	var output boundedGraphOutput
	if n, err := output.Write(make([]byte, maxJSONFileBytes+1)); err == nil || n != 0 || output.Len() != 0 {
		t.Fatalf("overflow write = %d, %v; buffered = %d", n, err, output.Len())
	}
}

func TestValidateRequiresPublisherGraph(t *testing.T) {
	t.Parallel()
	inv := validInventory()
	inv.ModuleGraphs = inv.ModuleGraphs[:4]
	err := Validate(inv, "")
	if err == nil || !strings.Contains(err.Error(), `required module graph "goreleaser" is missing`) {
		t.Fatalf("Validate() = %v, want missing publisher graph", err)
	}
}

func TestPublisherLocalSourcePinDoesNotClaimUpstreamArchive(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFixture(t, root, "tools/goreleaser/source.json", `{"module":"github.com/goreleaser/goreleaser/v2"}`)
	graph, ok := moduleGraphFiles(goreleaserGraphID)
	if !ok {
		t.Fatal("publisher graph missing")
	}
	sources, pin, err := publisherSourcePin(root, graph, "github.com/goreleaser/goreleaser/v2")
	if err != nil || pin.Kind != "prepared-source-profile-sha256" || len(pin.Value) != 64 ||
		len(sources) != 2 || sources[1].Match != "../source" {
		t.Fatalf("source pin = %#v, sources = %#v, error = %v", pin, sources, err)
	}
	writeFixture(t, root, "tools/goreleaser/source.json",
		`{"module":"github.com/goreleaser/goreleaser/v2","changed":true}`)
	_, changed, err := publisherSourcePin(root, graph, "github.com/goreleaser/goreleaser/v2")
	if err != nil || changed == pin {
		t.Fatalf("changed source profile pin = %#v, error = %v", changed, err)
	}
}
