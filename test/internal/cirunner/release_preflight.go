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
	Root        string
	RunnerTemp  string
	RefName     string
	SHA         string
	Repository  string
	Environment []string
	Runner      SpecRunner
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

type releasePreflightInputs struct {
	root, runnerTemp                       string
	version, releaseBranch, expectedCommit string
	owner, repository                      string
}

// ReleasePreflight refuses mutable or colliding release identities before publication starts.
func ReleasePreflight(ctx context.Context, options ReleasePreflightOptions) (returnErr error) {
	inputs, err := validateReleasePreflightInputs(options)
	if err != nil {
		return err
	}
	repositoryRoot, err := os.OpenRoot(inputs.root)
	if err != nil {
		return fmt.Errorf("open repository root for release preflight: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close repository preflight root", repositoryRoot.Close()))
	}()
	if identityErr := validateRootPathIdentity(
		repositoryRoot, inputs.root, "repository root before release preflight",
	); identityErr != nil {
		return identityErr
	}
	receiptRoot, receipts, err := prepareReleaseReceipts(inputs.runnerTemp)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close RUNNER_TEMP receipt root", receiptRoot.Close()))
	}()

	tagObjectSHA, err := verifyReleaseGitIdentity(
		ctx, options.Runner, inputs.root, inputs.owner, inputs.repository,
		options.RefName, inputs.releaseBranch, inputs.expectedCommit, options.Environment,
	)
	if err != nil {
		return err
	}
	if err := verifyAbsentGitHubRelease(ctx, options, inputs); err != nil {
		return err
	}
	if err := verifyAbsentReleasePackageTags(ctx, options, inputs); err != nil {
		return err
	}
	if err := validateRootPathIdentity(
		repositoryRoot, inputs.root, "repository root after release preflight",
	); err != nil {
		return err
	}
	for index, value := range []string{inputs.expectedCommit, inputs.releaseBranch, tagObjectSHA} {
		if err := writeRootedReceipt(receiptRoot, receipts[index], []byte(value+"\n")); err != nil {
			return err
		}
	}
	return nil
}

func validateReleasePreflightInputs(options ReleasePreflightOptions) (releasePreflightInputs, error) {
	inputs := releasePreflightInputs{}
	var err error
	inputs.root, err = validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return releasePreflightInputs{}, err
	}
	inputs.runnerTemp, err = validateAbsoluteExistingDirectory(options.RunnerTemp, "RUNNER_TEMP")
	if err != nil {
		return releasePreflightInputs{}, err
	}
	inputs.version, inputs.releaseBranch, err = parseStableReleaseVersion(options.RefName)
	if err != nil {
		return releasePreflightInputs{}, err
	}
	inputs.expectedCommit, err = validateFullCommitSHA(options.SHA, "GITHUB_SHA")
	if err != nil {
		return releasePreflightInputs{}, err
	}
	inputs.owner, inputs.repository, err = parseGitHubRepository(options.Repository)
	if err != nil {
		return releasePreflightInputs{}, err
	}
	if options.Runner == nil {
		return releasePreflightInputs{}, fmt.Errorf("release preflight command runner is required: %w", errInvalidConfig)
	}
	return inputs, nil
}

func verifyAbsentGitHubRelease(
	ctx context.Context, options ReleasePreflightOptions, inputs releasePreflightInputs,
) error {
	releaseResponse := releaseGraphQLResponse{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"api", "graphql", "-f", "query=" + releasePreflightGraphQLQuery,
			"-F", "owner=" + inputs.owner, "-F", "name=" + inputs.repository, "-F", "tag=" + options.RefName,
		},
		Dir: inputs.root, Env: options.Environment,
	}, "query existing release", &releaseResponse, validateReleaseGraphQLResponseJSON); err != nil {
		return err
	}
	return validateAbsentRelease(releaseResponse, options.RefName)
}

func verifyAbsentReleasePackageTags(
	ctx context.Context, options ReleasePreflightOptions, inputs releasePreflightInputs,
) error {
	packagePages := [][]*releasePackageVersion{}
	if err := runReleasePreflightJSON(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"api", "--paginate", "/users/" + inputs.owner + "/packages/container/" +
				inputs.repository + "/versions?per_page=100", "--slurp",
		},
		Dir: inputs.root, Env: options.Environment,
	}, "list OCI versions", &packagePages, validateReleasePackagePagesJSON); err != nil {
		return err
	}
	existingTags, err := collectReleasePackageTags(packagePages)
	if err != nil {
		return err
	}
	for _, tag := range []string{
		inputs.version,
		inputs.version + "-debian-trixie",
		inputs.version + "-oraclelinux10",
	} {
		if _, exists := existingTags[tag]; exists {
			return fmt.Errorf("versioned OCI tag already exists: %s: %w", tag, errInvalidConfig)
		}
	}
	return nil
}

func collectReleasePackageTags(packagePages [][]*releasePackageVersion) (map[string]struct{}, error) {
	if len(packagePages) == 0 {
		return nil, fmt.Errorf("paginated OCI versions response has no pages: %w", errInvalidConfig)
	}
	existingTags := make(map[string]struct{})
	for pageIndex, page := range packagePages {
		if page == nil {
			return nil, fmt.Errorf("paginated OCI versions page %d is null: %w", pageIndex, errInvalidConfig)
		}
		if err := collectReleasePackagePageTags(existingTags, pageIndex, page); err != nil {
			return nil, err
		}
	}
	return existingTags, nil
}

func collectReleasePackagePageTags(
	existingTags map[string]struct{}, pageIndex int, page []*releasePackageVersion,
) error {
	for itemIndex, packageVersion := range page {
		if packageVersion == nil || packageVersion.Metadata == nil || packageVersion.Metadata.Container == nil {
			return fmt.Errorf(
				"OCI versions page %d item %d lacks container metadata: %w",
				pageIndex, itemIndex, errInvalidConfig,
			)
		}
		tagsJSON := bytes.TrimSpace(packageVersion.Metadata.Container.Tags)
		if len(tagsJSON) == 0 || bytes.Equal(tagsJSON, []byte("null")) {
			return fmt.Errorf(
				"OCI versions page %d item %d lacks an explicit tags array: %w",
				pageIndex, itemIndex, errInvalidConfig,
			)
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
	return nil
}

func verifyReleaseGitIdentity(
	ctx context.Context,
	runner SpecRunner,
	root string,
	owner string,
	repository string,
	refName string,
	releaseBranch string,
	expectedCommit string,
	environment []string,
) (string, error) {
	headOutput, err := runReleasePreflightCommand(ctx, runner, CommandSpec{
		Name: "git", Args: []string{"rev-parse", "HEAD"}, Dir: root,
		Env: withoutEnvironmentKeys(environment, "GH_TOKEN", "GITHUB_TOKEN"),
	}, "resolve checked-out release commit")
	if err != nil {
		return "", err
	}
	head, err := parseCommandSHA(headOutput, "git rev-parse HEAD")
	if err != nil {
		return "", err
	}
	if expectedCommit != head {
		return "", fmt.Errorf("checked-out commit does not equal the workflow tag commit: %w", errInvalidConfig)
	}

	tagObjectSHA, err := verifyAnnotatedReleaseTag(
		ctx, runner, root, owner, repository, refName, expectedCommit, environment,
	)
	if err != nil {
		return "", err
	}
	branchRef := releaseGitRef{}
	if branchErr := runReleasePreflightJSON(ctx, runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/ref/heads/" + releaseBranch},
		Dir: root, Env: environment,
	}, "read release branch ref", &branchRef, validateReleaseGitRefJSON); branchErr != nil {
		return "", branchErr
	}
	branchSHA, err := validateFullCommitSHA(branchRef.Object.SHA, "release branch head SHA")
	if err != nil {
		return "", err
	}
	if branchRef.Ref != "refs/heads/"+releaseBranch || branchRef.Object.Type != "commit" || branchSHA != expectedCommit {
		return "", fmt.Errorf("%s does not equal the exact %s head: %w", refName, releaseBranch, errInvalidConfig)
	}
	return tagObjectSHA, nil
}

func verifyAnnotatedReleaseTag(
	ctx context.Context,
	runner SpecRunner,
	root, owner, repository, refName, expectedCommit string,
	environment []string,
) (string, error) {
	tagRef := releaseGitRef{}
	if tagRefErr := runReleasePreflightJSON(ctx, runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/ref/tags/" + refName},
		Dir: root, Env: environment,
	}, "read annotated release tag ref", &tagRef, validateReleaseGitRefJSON); tagRefErr != nil {
		return "", tagRefErr
	}
	tagObjectSHA, err := validateFullCommitSHA(tagRef.Object.SHA, "release tag object SHA")
	if err != nil {
		return "", err
	}
	if tagRef.Ref != "refs/tags/"+refName || tagRef.Object.Type != "tag" {
		return "", fmt.Errorf("release tag must be an exact annotated tag ref: %s: %w", refName, errInvalidConfig)
	}

	tagObject := releaseGitTag{}
	if tagObjectErr := runReleasePreflightJSON(ctx, runner, CommandSpec{
		Name: "gh", Args: []string{"api", "repos/" + owner + "/" + repository + "/git/tags/" + tagObjectSHA},
		Dir: root, Env: environment,
	}, "read annotated release tag object", &tagObject, validateReleaseGitTagJSON); tagObjectErr != nil {
		return "", tagObjectErr
	}
	tagSHA, err := validateFullCommitSHA(tagObject.SHA, "annotated tag object SHA")
	if err != nil {
		return "", err
	}
	tagTargetSHA, err := validateFullCommitSHA(tagObject.Object.SHA, "annotated tag target SHA")
	if err != nil {
		return "", err
	}
	if tagSHA != tagObjectSHA || tagObject.Tag != refName ||
		tagObject.Object.Type != "commit" || tagTargetSHA != expectedCommit {
		return "", fmt.Errorf("annotated tag does not target the checked-out release commit: %w", errInvalidConfig)
	}
	return tagObjectSHA, nil
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
	const fullCommitSHACharacterCount = 40

	if len(value) != fullCommitSHACharacterCount {
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
	return writeRootedModeArtifact(root, name, data, purpose, limit, benchmarkArtifactMode)
}

type rootedModeArtifactInspector func(*os.Root, string, string, os.FileMode, int64) error

func writeRootedModeArtifact(
	root *os.Root,
	name string,
	data []byte,
	purpose string,
	limit int,
	mode os.FileMode,
) error {
	_, err := writeRootedModeArtifactState(
		root, name, data, purpose, limit, mode, inspectPublishedRootedModeArtifact,
	)
	return err
}

func writeRootedModeArtifactState(
	root *os.Root,
	name string,
	data []byte,
	purpose string,
	limit int,
	mode os.FileMode,
	inspect rootedModeArtifactInspector,
) (_ bool, returnErr error) {
	temporary, temporaryName, err := createRootedModeArtifactTemporary(root, name, data, purpose, limit, mode)
	if err != nil {
		return false, err
	}
	defer func() {
		if cleanupErr := root.Remove(temporaryName); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove temporary %s %s: %w", purpose, name, cleanupErr))
		}
	}()
	if chmodErr := temporary.Chmod(mode.Perm()); chmodErr != nil {
		return false, errors.Join(
			fmt.Errorf("set temporary %s %s mode: %w", purpose, name, chmodErr),
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
		return false, errors.Join(
			wrapOptional("write temporary "+purpose+" "+name, writeErr),
			wrapOptional("close temporary "+purpose+" "+name, closeErr),
		)
	}
	if renameErr := root.Rename(temporaryName, name); renameErr != nil {
		return false, fmt.Errorf("publish %s %s: %w", purpose, name, renameErr)
	}
	committed := true
	inspectErr := inspect(root, name, purpose, mode, int64(len(data)))
	return committed, inspectErr
}

func inspectPublishedRootedModeArtifact(
	root *os.Root, name, purpose string, mode os.FileMode, size int64,
) error {
	info, err := root.Lstat(name)
	if err != nil {
		return fmt.Errorf("inspect published %s %s: %w", purpose, name, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != mode.Perm() || info.Size() != size {
		return fmt.Errorf("published %s %s violates mode or size contract: %w", purpose, name, errInvalidConfig)
	}
	return nil
}

func createRootedModeArtifactTemporary(
	root *os.Root,
	name string,
	data []byte,
	purpose string,
	limit int,
	mode os.FileMode,
) (*os.File, string, error) {
	if root == nil || len(data) > limit {
		return nil, "", fmt.Errorf("%s %s exceeds its bounded contract: %w", purpose, name, errInvalidConfig)
	}
	if !mode.IsRegular() || mode.Perm() == 0 {
		return nil, "", fmt.Errorf("%s %s has invalid output mode %#o: %w", purpose, name, mode, errInvalidConfig)
	}
	random := [16]byte{}
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", fmt.Errorf("generate temporary %s name: %w", purpose, err)
	}
	temporaryName := "." + name + "-" + hex.EncodeToString(random[:])
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("create temporary %s %s: %w", purpose, name, err)
	}
	return temporary, temporaryName, nil
}

func parseCommandSHA(output []byte, purpose string) (string, error) {
	value := strings.TrimSuffix(string(output), "\n")
	if strings.Contains(value, "\n") || strings.Contains(value, "\r") {
		return "", fmt.Errorf("%s returned multiple lines: %w", purpose, ErrInvalidSHA)
	}
	return validateFullCommitSHA(value, purpose)
}

func runReleasePreflightJSON(
	ctx context.Context,
	runner SpecRunner,
	spec CommandSpec,
	purpose string,
	destination any,
	validateFields func([]byte) error,
) error {
	output, err := runReleasePreflightCommand(ctx, runner, spec, purpose)
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(output, purpose); err != nil {
		return fmt.Errorf("validate %s structure: %w", purpose, err)
	}
	if err := validateFields(output); err != nil {
		return fmt.Errorf("validate %s fields: %w", purpose, err)
	}
	return decodeJSONDocument(output, destination, purpose)
}

func validateReleaseGitRefJSON(data []byte) error {
	fields, err := decodeRequiredJSONObject(data, "release git ref", []string{"ref", "object"})
	if err != nil {
		return err
	}
	_, err = decodeRequiredJSONObject(fields["object"], "release git ref object", []string{"type", "sha"})
	return err
}

func validateReleaseGitTagJSON(data []byte) error {
	fields, err := decodeRequiredJSONObject(data, "release git tag", []string{"sha", "tag", "object"})
	if err != nil {
		return err
	}
	_, err = decodeRequiredJSONObject(fields["object"], "release git tag object", []string{"type", "sha"})
	return err
}

func validateReleaseGraphQLResponseJSON(data []byte) error {
	fields := map[string]json.RawMessage{}
	if err := decodeJSONDocument(data, &fields, "release GraphQL response"); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("release GraphQL response is not an object: %w", errInvalidConfig)
	}
	if err := rejectJSONFieldAliases(fields, []string{"errors", "data"}); err != nil {
		return err
	}
	dataJSON, exists := fields["data"]
	if !exists {
		return fmt.Errorf("release GraphQL response lacks data: %w", errInvalidConfig)
	}
	dataFields, err := decodeRequiredJSONObject(dataJSON, "release GraphQL data", []string{"repository"})
	if err != nil {
		return err
	}
	_, err = decodeRequiredJSONObject(
		dataFields["repository"], "release GraphQL repository", []string{"release"},
	)
	return err
}

func validateReleasePackagePagesJSON(data []byte) error {
	pages := []json.RawMessage{}
	if err := decodeJSONDocument(data, &pages, "release package pages"); err != nil {
		return err
	}
	for pageIndex, pageJSON := range pages {
		page := []json.RawMessage{}
		if err := decodeJSONDocument(pageJSON, &page, fmt.Sprintf("release package page %d", pageIndex)); err != nil {
			return err
		}
		for itemIndex, itemJSON := range page {
			item, err := decodeRequiredJSONObject(
				itemJSON, fmt.Sprintf("release package page %d item %d", pageIndex, itemIndex), []string{"metadata"},
			)
			if err != nil {
				return err
			}
			metadata, err := decodeRequiredJSONObject(
				item["metadata"], "release package metadata", []string{"container"},
			)
			if err != nil {
				return err
			}
			if _, err := decodeRequiredJSONObject(
				metadata["container"], "release package container", []string{"tags"},
			); err != nil {
				return err
			}
		}
	}
	return nil
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
	written, err := output.Buffer.Write(data)
	if err != nil {
		return written, fmt.Errorf("buffer release preflight response: %w", err)
	}
	return written, nil
}
