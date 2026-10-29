package cirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ReleasePullRequestOptions binds release notes to a merged release pull request.
type ReleasePullRequestOptions struct {
	Root        string
	RefName     string
	SHA         string
	Repository  string
	Environment []string
	Runner      SpecRunner
}

type releasePullRequest struct {
	Number         int    `json:"number"`
	State          string `json:"state"`
	MergedAt       string `json:"merged_at"`
	MergeCommitSHA string `json:"merge_commit_sha"`
	HTMLURL        string `json:"html_url"`
	Base           struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
}

type releasePRInputs struct {
	root, version, branch, repository string
}

// VerifyReleasePullRequest validates the manifest, translations and exact merged PR
// before appending its canonical URL. It performs no remote mutations.
func VerifyReleasePullRequest(ctx context.Context, options ReleasePullRequestOptions) (returnErr error) {
	inputs, err := prepareReleasePRInputs(options)
	if err != nil {
		return err
	}
	files, err := os.OpenRoot(inputs.root)
	if err != nil {
		return fmt.Errorf("open release PR root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close release PR root", files.Close()))
	}()
	if documentErr := validateReleasePRDocuments(files, inputs.version); documentErr != nil {
		return documentErr
	}
	pullRequest, err := queryMergedReleasePR(ctx, options, inputs)
	if err != nil {
		return err
	}
	if identityErr := validateRootPathIdentity(files, inputs.root, "release PR root after queries"); identityErr != nil {
		return identityErr
	}
	return appendReleasePRNotes(files, pullRequest)
}

func prepareReleasePRInputs(options ReleasePullRequestOptions) (releasePRInputs, error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "release PR root")
	if err != nil {
		return releasePRInputs{}, err
	}
	version, branch, err := parseReleaseVersion(options.RefName)
	if err != nil {
		return releasePRInputs{}, err
	}
	if _, shaErr := validateFullCommitSHA(options.SHA, "release PR commit"); shaErr != nil {
		return releasePRInputs{}, shaErr
	}
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return releasePRInputs{}, err
	}
	if options.Runner == nil {
		return releasePRInputs{}, fmt.Errorf("release PR command runner is required: %w", errInvalidConfig)
	}
	return releasePRInputs{root: root, version: version, branch: branch, repository: owner + "/" + repository}, nil
}

func queryMergedReleasePR(
	ctx context.Context, options ReleasePullRequestOptions, inputs releasePRInputs,
) (releasePullRequest, error) {
	response, err := runReleasePRQuery(ctx, options, inputs.root,
		"repos/"+inputs.repository+"/commits/"+options.SHA+"/pulls?per_page=100")
	if err != nil {
		return releasePullRequest{}, err
	}
	pullRequest, err := selectMergedReleasePR(response, options.SHA, inputs.repository, inputs.branch)
	if err != nil {
		return releasePullRequest{}, err
	}
	response, err = runReleasePRQuery(ctx, options, inputs.root,
		"repos/"+inputs.repository+"/pulls/"+strconv.Itoa(pullRequest.Number)+"/files?per_page=100")
	if err != nil {
		return releasePullRequest{}, err
	}
	if fileErr := validateReleasePRChangedFiles(response); fileErr != nil {
		return releasePullRequest{}, fileErr
	}
	return pullRequest, nil
}

func appendReleasePRNotes(files *os.Root, pullRequest releasePullRequest) error {
	notes, err := readRootedRegularFile(files, "release-notes.md", "release PR notes", releaseNotesLimit)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		return fmt.Errorf("release PR notes are empty: %w", errInvalidConfig)
	}
	footer := fmt.Sprintf("\n\nRelease pull request: [#%d](%s).\n", pullRequest.Number, pullRequest.HTMLURL)
	if strings.HasSuffix(string(notes), footer) {
		return nil
	}
	if strings.Contains(string(notes), "\nRelease pull request:") {
		return fmt.Errorf("release notes already bind a different pull request: %w", errInvalidConfig)
	}
	return writeRootedArtifact(files, "release-notes.md", append(notes, footer...), "release PR notes", releaseNotesLimit)
}

func runReleasePRQuery(
	ctx context.Context, options ReleasePullRequestOptions, root, endpoint string,
) ([]byte, error) {
	data, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "gh", Args: []string{
			"api", "--paginate", endpoint, "--slurp", "--header", "X-GitHub-Api-Version:2022-11-28",
		},
		Dir: root, Env: options.Environment,
	}, "query release pull request")
	if err != nil {
		return nil, err
	}
	if err := validateStrictJSONDocument(data, "release pull request query"); err != nil {
		return nil, err
	}
	return data, nil
}

func validateReleasePRDocuments(root *os.Root, version string) error {
	manifest, err := readRootedRegularFile(root, ".release-please-manifest.json", "release manifest", releaseNotesLimit)
	if err != nil {
		return err
	}
	if structureErr := validateStrictJSONDocument(manifest, "release manifest"); structureErr != nil {
		return structureErr
	}
	fields, err := decodeRequiredJSONObject(manifest, "release manifest", []string{"."})
	if err != nil {
		return err
	}
	actual, err := decodeRequiredJSONString(fields["."], "release manifest version")
	if err != nil {
		return err
	}
	if actual != version {
		return fmt.Errorf("release manifest does not match tag version: %w", errInvalidConfig)
	}
	date := ""
	for _, name := range []string{"CHANGELOG.md", "CHANGELOG.ru.md"} {
		data, err := readRootedRegularFile(root, name, "release changelog", releaseNotesLimit)
		if err != nil {
			return err
		}
		found := false
		for line := range strings.SplitSeq(string(data), "\n") {
			if validDatedReleaseHeading(line, version) {
				actualDate := strings.TrimPrefix(line, "## ["+version+"] - ")
				if date != "" && actualDate != date {
					return fmt.Errorf("release changelog dates differ between languages: %w", errInvalidConfig)
				}
				date = actualDate
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s lacks a dated release version: %w", name, errInvalidConfig)
		}
	}
	return nil
}

func selectMergedReleasePR(data []byte, sha, repository, branch string) (releasePullRequest, error) {
	var pages [][]releasePullRequest
	if err := decodeJSONDocument(data, &pages, "associated release pull requests"); err != nil {
		return releasePullRequest{}, err
	}
	selected := releasePullRequest{}
	for _, page := range pages {
		for _, candidate := range page {
			if candidate.State != "closed" || candidate.MergeCommitSHA != sha || candidate.Base.Ref != branch ||
				candidate.Base.Repo.FullName != repository || candidate.MergedAt == "" ||
				!strings.HasPrefix(candidate.Head.Ref, "release-please--branches--") {
				continue
			}
			if _, err := time.Parse(time.RFC3339, candidate.MergedAt); err != nil {
				return releasePullRequest{}, fmt.Errorf("release PR merge timestamp: %w", err)
			}
			expectedURL := "https://github.com/" + repository + "/pull/" + strconv.Itoa(candidate.Number)
			if candidate.Number <= 0 || candidate.HTMLURL != expectedURL {
				return releasePullRequest{}, fmt.Errorf("release PR identity is not canonical: %w", errInvalidConfig)
			}
			if selected.Number != 0 {
				return releasePullRequest{}, fmt.Errorf("multiple merged release PRs match the tag: %w", errInvalidConfig)
			}
			selected = candidate
		}
	}
	if selected.Number == 0 {
		return releasePullRequest{}, fmt.Errorf(
			"no merged Release Please PR matches the exact tag commit and branch: %w", errInvalidConfig,
		)
	}
	return selected, nil
}

func validateReleasePRChangedFiles(data []byte) error {
	var pages [][]struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
	}
	if err := decodeJSONDocument(data, &pages, "release PR files"); err != nil {
		return err
	}
	required := map[string]bool{"CHANGELOG.md": false, "CHANGELOG.ru.md": false, ".release-please-manifest.json": false}
	for _, page := range pages {
		for _, file := range page {
			if _, exists := required[file.Filename]; exists && (file.Status == "modified" || file.Status == "added") {
				required[file.Filename] = true
			}
		}
	}
	for name, found := range required {
		if !found {
			return fmt.Errorf("release PR does not update %s: %w", name, errInvalidConfig)
		}
	}
	return nil
}
