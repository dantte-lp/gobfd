package cirunner

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	releasePreflightResponseLimit = 64 << 20
	releaseReceiptLimit           = 256
	releasePreflightGraphQLQuery  = `
query($owner: String!, $name: String!, $tag: String!) {
  repository(owner: $owner, name: $name) {
    release(tagName: $tag) {
      id
      isDraft
      tagName
    }
  }
}
`
)

// ReleasePreflightOptions supplies immutable release identity inputs.
type ReleasePreflightOptions struct {
	Root       string
	RunnerTemp string
	RefName    string
	SHA        string
	Repository string
	Runner     SpecRunner
}

type releaseGitObject struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

type releaseGitRef struct {
	Ref    string           `json:"ref"`
	Object releaseGitObject `json:"object"`
}

type releaseGitTag struct {
	SHA    string           `json:"sha"`
	Tag    string           `json:"tag"`
	Object releaseGitObject `json:"object"`
}

type releaseGraphQLResponse struct {
	Errors json.RawMessage `json:"errors"`
	Data   *struct {
		Repository *struct {
			Release json.RawMessage `json:"release"`
		} `json:"repository"`
	} `json:"data"`
}

type releasePackageVersion struct {
	Metadata *releasePackageMetadata `json:"metadata"`
}

type releasePackageMetadata struct {
	Container *releasePackageContainer `json:"container"`
}

type releasePackageContainer struct {
	Tags json.RawMessage `json:"tags"`
}

// ReleasePreflight refuses mutable or colliding release identities before publication starts.
func ReleasePreflight(ctx context.Context, options ReleasePreflightOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	runnerTemp, err := validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return err
	}
	version, releaseBranch, err := parseStableReleaseVersion(options.RefName)
	if err != nil {
		return err
	}
	expectedCommit, err := validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return err
	}
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release preflight command runner is required: %w", errInvalidConfig)
	}
	receiptRoot, receipts, err := prepareReleaseReceipts(runnerTemp)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close RUNNER_TEMP receipt root", receiptRoot.Close()))
	}()

	headOutput, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: root,
	}, "resolve checked-out release commit")
	if err != nil {
		return err
	}
	head, err := parseCommandSHA(headOutput, "git rev-parse HEAD")
	if err != nil {
		return err
	}
	if expectedCommit != head {
		return fmt.Errorf("checked-out commit does not equal the workflow tag commit: %w", errInvalidConfig)
	}

	tagRef := releaseGitRef{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/ref/tags/" + options.RefName}, Dir: root,
	}, "read annotated release tag ref", &tagRef); err != nil {
		return err
	}
	tagObjectSHA, err := validateFullCommitSHA(tagRef.Object.SHA, "release tag object SHA")
	if err != nil {
		return err
	}
	if tagRef.Ref != "refs/tags/"+options.RefName || tagRef.Object.Type != "tag" {
		return fmt.Errorf("release tag must be an exact annotated tag ref: %s: %w", options.RefName, errInvalidConfig)
	}

	tagObject := releaseGitTag{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/tags/" + tagObjectSHA}, Dir: root,
	}, "read annotated release tag object", &tagObject); err != nil {
		return err
	}
	tagSHA, err := validateFullCommitSHA(tagObject.SHA, "annotated tag object SHA")
	if err != nil {
		return err
	}
	tagTargetSHA, err := validateFullCommitSHA(tagObject.Object.SHA, "annotated tag target SHA")
	if err != nil {
		return err
	}
	if tagSHA != tagObjectSHA || tagObject.Tag != options.RefName ||
		tagObject.Object.Type != "commit" || tagTargetSHA != expectedCommit {
		return fmt.Errorf("annotated tag does not target the checked-out release commit: %w", errInvalidConfig)
	}

	branchRef := releaseGitRef{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/ref/heads/" + releaseBranch}, Dir: root,
	}, "read release branch ref", &branchRef); err != nil {
		return err
	}
	branchSHA, err := validateFullCommitSHA(branchRef.Object.SHA, "release branch head SHA")
	if err != nil {
		return err
	}
	if branchRef.Ref != "refs/heads/"+releaseBranch || branchRef.Object.Type != "commit" || branchSHA != expectedCommit {
		return fmt.Errorf("%s does not equal the exact %s head: %w", options.RefName, releaseBranch, errInvalidConfig)
	}
	releaseResponse := releaseGraphQLResponse{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"api", "graphql", "-f", "query=" + releasePreflightGraphQLQuery,
			"-F", "owner=" + owner, "-F", "name=" + repository, "-F", "tag=" + options.RefName,
		},
		Dir: root,
	}, "query existing release", &releaseResponse); err != nil {
		return err
	}
	if err := validateAbsentRelease(releaseResponse, options.RefName); err != nil {
		return err
	}

	packagePages := [][]*releasePackageVersion{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"api", "--paginate", "/users/" + owner + "/packages/container/" + repository + "/versions?per_page=100", "--slurp",
		},
		Dir: root,
	}, "list versioned OCI tags", &packagePages); err != nil {
		return err
	}
	if len(packagePages) == 0 {
		return fmt.Errorf("paginated OCI versions response has no pages: %w", errInvalidConfig)
	}
	existingTags := make(map[string]struct{})
	for pageIndex, page := range packagePages {
		if page == nil {
			return fmt.Errorf("paginated OCI versions page %d is null: %w", pageIndex, errInvalidConfig)
		}
		for itemIndex, packageVersion := range page {
			if packageVersion == nil || packageVersion.Metadata == nil || packageVersion.Metadata.Container == nil {
				return fmt.Errorf("OCI versions page %d item %d lacks container metadata: %w", pageIndex, itemIndex, errInvalidConfig)
			}
			tagsJSON := bytes.TrimSpace(packageVersion.Metadata.Container.Tags)
			if len(tagsJSON) == 0 || bytes.Equal(tagsJSON, []byte("null")) {
				return fmt.Errorf("OCI versions page %d item %d lacks an explicit tags array: %w", pageIndex, itemIndex, errInvalidConfig)
			}
			tagValues := []json.RawMessage{}
			if err := json.Unmarshal(tagsJSON, &tagValues); err != nil {
				return fmt.Errorf("decode OCI versions page %d item %d tags: %w", pageIndex, itemIndex, err)
			}
			for tagIndex, tagJSON := range tagValues {
				var tag *string
				if err := json.Unmarshal(tagJSON, &tag); err != nil || tag == nil {
					return fmt.Errorf(
						"decode OCI versions page %d item %d tag %d as string: %w",
						pageIndex, itemIndex, tagIndex, errors.Join(err, errInvalidConfig),
					)
				}
				existingTags[*tag] = struct{}{}
			}
		}
	}
	for _, tag := range []string{version, version + "-debian-trixie", version + "-oraclelinux10"} {
		if _, exists := existingTags[tag]; exists {
			return fmt.Errorf("versioned OCI tag already exists: %s: %w", tag, errInvalidConfig)
		}
	}
	for index, value := range []string{expectedCommit, releaseBranch, tagObjectSHA} {
		if err := writeRootedReceipt(receiptRoot, receipts[index], []byte(value+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func parseStableReleaseVersion(refName string) (string, string, error) {
	version, canonical := parseCanonicalReleaseTag(refName)
	if !canonical {
		return "", "", fmt.Errorf("release tag is not canonical stable SemVer: %q: %w", refName, errInvalidConfig)
	}
	return strings.TrimPrefix(refName, "v"), "release/v" + version.Major + "." + version.Minor, nil
}

func parseGitHubRepository(value string) (string, string, error) {
	owner, repository, found := strings.Cut(value, "/")
	if !found || strings.Contains(repository, "/") || !validGitHubOwner(owner) || !validGitHubRepositoryName(repository) {
		return "", "", fmt.Errorf("invalid GITHUB_REPOSITORY: %q: %w", value, errInvalidConfig)
	}
	return owner, repository, nil
}

func validGitHubOwner(value string) bool {
	if value == "" || len(value) > 39 || value[0] == '-' || value[len(value)-1] == '-' || strings.Contains(value, "--") {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func validGitHubRepositoryName(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 100 || hasControl(value) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("-._", character) {
			continue
		}
		return false
	}
	return true
}

func validateFullCommitSHA(value, purpose string) (string, error) {
	if len(value) != 40 {
		return "", fmt.Errorf("%s must be exactly 40 lowercase hexadecimal characters: %w", purpose, ErrInvalidSHA)
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			if character < 'a' || character > 'f' {
				return "", fmt.Errorf("%s must be exactly 40 lowercase hexadecimal characters: %w", purpose, ErrInvalidSHA)
			}
		}
	}
	return value, nil
}

func prepareReleaseReceipts(runnerTemp string) (*os.Root, []string, error) {
	names := []string{
		"expected-release-commit.txt",
		"expected-release-branch.txt",
		"expected-release-tag-object.txt",
	}
	root, err := os.OpenRoot(runnerTemp)
	if err != nil {
		return nil, nil, fmt.Errorf("open RUNNER_TEMP receipt root: %w", err)
	}
	for _, name := range names {
		info, statErr := root.Lstat(name)
		if statErr == nil && !info.Mode().IsRegular() {
			validationErr := fmt.Errorf("release identity receipt %s has mode %s: %w", name, info.Mode(), errInvalidConfig)
			return nil, nil, errors.Join(validationErr, wrapOptional("close RUNNER_TEMP receipt root", root.Close()))
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			validationErr := fmt.Errorf("inspect release identity receipt %s: %w", name, statErr)
			return nil, nil, errors.Join(validationErr, wrapOptional("close RUNNER_TEMP receipt root", root.Close()))
		}
	}
	for _, name := range names {
		if err := writeRootedReceipt(root, name, nil); err != nil {
			return nil, nil, errors.Join(err, wrapOptional("close RUNNER_TEMP receipt root", root.Close()))
		}
	}
	return root, names, nil
}

func writeRootedReceipt(root *os.Root, name string, data []byte) (returnErr error) {
	return writeRootedArtifact(root, name, data, "release identity receipt", releaseReceiptLimit)
}

func writeRootedArtifact(root *os.Root, name string, data []byte, purpose string, limit int) (returnErr error) {
	if root == nil || len(data) > limit {
		return fmt.Errorf("%s %s exceeds its bounded contract: %w", purpose, name, errInvalidConfig)
	}
	random := [16]byte{}
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate temporary %s name: %w", purpose, err)
	}
	temporaryName := "." + name + "-" + hex.EncodeToString(random[:])
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create temporary %s %s: %w", purpose, name, err)
	}
	defer func() {
		if err := root.Remove(temporaryName); err != nil && !errors.Is(err, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary %s %s: %w", purpose, name, err))
		}
	}()
	if err := temporary.Chmod(benchmarkArtifactMode); err != nil {
		return errors.Join(
			fmt.Errorf("set temporary %s %s mode: %w", purpose, name, err),
			wrapOptional("close temporary "+purpose+" "+name, temporary.Close()),
		)
	}
	written, writeErr := temporary.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr != nil || closeErr != nil {
		return errors.Join(
			wrapOptional("write temporary "+purpose+" "+name, writeErr),
			wrapOptional("close temporary "+purpose+" "+name, closeErr),
		)
	}
	if err := root.Rename(temporaryName, name); err != nil {
		return fmt.Errorf("publish %s %s: %w", purpose, name, err)
	}
	info, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect published %s %s: %w", purpose, name, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != benchmarkArtifactMode || info.Size() != int64(len(data)) {
		return fmt.Errorf("published %s %s violates mode or size contract: %w", purpose, name, errInvalidConfig)
	}
	return nil
}

func parseCommandSHA(output []byte, purpose string) (string, error) {
	value := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(value, "\n") || strings.Contains(value, "\r") {
		return "", fmt.Errorf("%s returned multiple lines: %w", purpose, ErrInvalidSHA)
	}
	return validateFullCommitSHA(value, purpose)
}

func runReleasePreflightJSON(ctx context.Context, runner SpecRunner, spec CommandSpec, purpose string, destination any) error {
	output, err := runReleasePreflightCommand(ctx, runner, spec, purpose)
	if err != nil {
		return err
	}
	return decodeJSONDocument(output, destination, purpose)
}

func runReleasePreflightCommand(ctx context.Context, runner SpecRunner, spec CommandSpec, purpose string) ([]byte, error) {
	output := &boundedPreflightOutput{limit: releasePreflightResponseLimit}
	spec.Stdout = output
	if err := runner.RunCommand(ctx, spec); err != nil {
		return nil, fmt.Errorf("%s: %w", purpose, err)
	}
	if output.Len() == 0 {
		return nil, fmt.Errorf("%s returned empty output: %w", purpose, errInvalidConfig)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

func validateAbsentRelease(response releaseGraphQLResponse, refName string) error {
	errorsJSON := bytes.TrimSpace(response.Errors)
	if len(errorsJSON) > 0 && !bytes.Equal(errorsJSON, []byte("null")) {
		items := []json.RawMessage{}
		if err := json.Unmarshal(errorsJSON, &items); err != nil {
			return fmt.Errorf("decode GraphQL errors: %w", err)
		}
		if len(items) != 0 {
			return fmt.Errorf("GitHub release query returned errors: %w", errInvalidConfig)
		}
	}
	if response.Data == nil || response.Data.Repository == nil || len(response.Data.Repository.Release) == 0 {
		return fmt.Errorf("GitHub release query lacks repository release state: %w", errInvalidConfig)
	}
	if !bytes.Equal(bytes.TrimSpace(response.Data.Repository.Release), []byte("null")) {
		return fmt.Errorf("release or draft already exists: %s: %w", refName, errInvalidConfig)
	}
	return nil
}

type boundedPreflightOutput struct {
	bytes.Buffer
	limit int
}

func (output *boundedPreflightOutput) Write(data []byte) (int, error) {
	if len(data) > output.limit-output.Len() {
		return 0, fmt.Errorf("release preflight response exceeds %d bytes: %w", output.limit, errInvalidConfig)
	}
	return output.Buffer.Write(data)
}
