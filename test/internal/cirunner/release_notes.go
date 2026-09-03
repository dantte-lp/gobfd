package cirunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const releaseNotesLimit = 8 << 20

// ReleaseNotesOptions supplies release-list and changelog inputs.
type ReleaseNotesOptions struct {
	Root       string
	RefName    string
	Repository string
	Output     io.Writer
	Runner     SpecRunner
}

type listedRelease struct {
	Draft      *bool           `json:"draft"`
	Prerelease *bool           `json:"prerelease"`
	TagName    json.RawMessage `json:"tag_name"`
}

type canonicalReleaseVersion struct {
	Tag   string
	Major string
	Minor string
	Patch string
}

// ReleaseNotes selects the previous published release and renders the exact changelog range.
func ReleaseNotes(ctx context.Context, options ReleaseNotesOptions) (returnErr error) {
	root, err := validateAbsoluteExistingDirectory(options.Root, "repository root")
	if err != nil {
		return err
	}
	current, canonical := parseCanonicalReleaseTag(options.RefName)
	if !canonical {
		return fmt.Errorf("release tag is not canonical stable SemVer: %q: %w", options.RefName, errInvalidConfig)
	}
	owner, repository, err := parseGitHubRepository(options.Repository)
	if err != nil {
		return err
	}
	if options.Runner == nil {
		return fmt.Errorf("release notes command runner is required: %w", errInvalidConfig)
	}
	output := options.Output
	if output == nil {
		output = io.Discard
	}
	repositoryRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root for release notes: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, wrapOptional("close repository root for release notes", repositoryRoot.Close()))
	}()
	if err := validateRootedRegularTarget(repositoryRoot, "release-notes.md", "release notes"); err != nil {
		return err
	}
	if err := writeRootedArtifact(repositoryRoot, "release-notes.md", nil, "release notes", releaseNotesLimit); err != nil {
		return err
	}

	response, err := runReleasePreflightCommand(ctx, options.Runner, CommandSpec{
		Name: "gh",
		Args: []string{
			"api", "--paginate", "repos/" + owner + "/" + repository + "/releases?per_page=100", "--slurp",
		},
		Dir: root,
	}, "list published releases for release notes")
	if err != nil {
		return err
	}
	previousTag, err := selectPreviousRelease(response, current.Tag)
	if err != nil {
		return err
	}
	previous, canonical := parseCanonicalReleaseTag(previousTag)
	if !canonical {
		return fmt.Errorf("selected previous release tag is not canonical: %q: %w", previousTag, errInvalidConfig)
	}
	changelog, err := readRootedRegularFile(repositoryRoot, "CHANGELOG.md", "release changelog", releaseNotesLimit)
	if err != nil {
		return err
	}
	rangeText, err := extractReleaseChangelogRange(string(changelog), current, previous)
	if err != nil {
		return err
	}
	notes := renderReleaseNotes(owner, repository, current.Tag, previous.Tag, rangeText)
	if len(notes) == 0 || len(notes) > releaseNotesLimit {
		return fmt.Errorf("release notes exceed the bounded non-empty contract: %w", errInvalidConfig)
	}
	if err := writeRootedArtifact(repositoryRoot, "release-notes.md", []byte(notes), "release notes", releaseNotesLimit); err != nil {
		return err
	}
	console := "--- Release notes for " + strings.TrimPrefix(current.Tag, "v") + " ---\n" + notes
	if written, err := io.WriteString(output, console); err != nil {
		return fmt.Errorf("write release notes to workflow log: %w", err)
	} else if written != len(console) {
		return fmt.Errorf("write release notes to workflow log: %w", io.ErrShortWrite)
	}
	return nil
}

func selectPreviousRelease(data []byte, currentTag string) (string, error) {
	current, canonical := parseCanonicalReleaseTag(currentTag)
	if !canonical {
		return "", fmt.Errorf("current release tag is not canonical: %q: %w", currentTag, errInvalidConfig)
	}
	pages := [][]*listedRelease{}
	if err := decodeJSONDocument(data, &pages, "paginated published releases"); err != nil {
		return "", err
	}
	if len(pages) == 0 {
		return "", fmt.Errorf("paginated published releases contain no pages: %w", errInvalidConfig)
	}
	versions := make([]canonicalReleaseVersion, 0)
	for pageIndex, page := range pages {
		if page == nil {
			return "", fmt.Errorf("published releases page %d is null: %w", pageIndex, errInvalidConfig)
		}
		for itemIndex, release := range page {
			if release == nil || release.Draft == nil || release.Prerelease == nil || len(release.TagName) == 0 {
				return "", fmt.Errorf("published releases page %d item %d lacks required fields: %w", pageIndex, itemIndex, errInvalidConfig)
			}
			var tag *string
			if err := json.Unmarshal(release.TagName, &tag); err != nil || tag == nil {
				return "", fmt.Errorf(
					"decode published releases page %d item %d tag_name: %w",
					pageIndex, itemIndex, errors.Join(err, errInvalidConfig),
				)
			}
			if *release.Draft || *release.Prerelease || *tag == current.Tag {
				continue
			}
			version, canonical := parseCanonicalReleaseTag(*tag)
			if canonical {
				versions = append(versions, version)
			}
		}
	}
	var bestOverall *canonicalReleaseVersion
	var bestLine *canonicalReleaseVersion
	for index := range versions {
		candidate := &versions[index]
		if bestOverall == nil || compareReleaseVersions(*candidate, *bestOverall) > 0 {
			bestOverall = candidate
		}
		if candidate.Major == current.Major && candidate.Minor == current.Minor &&
			(bestLine == nil || compareReleaseVersions(*candidate, *bestLine) > 0) {
			bestLine = candidate
		}
	}
	if bestLine != nil {
		return bestLine.Tag, nil
	}
	if bestOverall != nil {
		return bestOverall.Tag, nil
	}
	return "", fmt.Errorf("no previous canonical published release exists: %w", errInvalidConfig)
}

func parseCanonicalReleaseTag(tag string) (canonicalReleaseVersion, bool) {
	if len(tag) > 128 || hasControl(tag) {
		return canonicalReleaseVersion{}, false
	}
	matches := regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).FindStringSubmatch(tag)
	if len(matches) != 4 {
		return canonicalReleaseVersion{}, false
	}
	return canonicalReleaseVersion{Tag: tag, Major: matches[1], Minor: matches[2], Patch: matches[3]}, true
}

func compareReleaseVersions(left, right canonicalReleaseVersion) int {
	for _, pair := range [][2]string{{left.Major, right.Major}, {left.Minor, right.Minor}, {left.Patch, right.Patch}} {
		if len(pair[0]) < len(pair[1]) {
			return -1
		}
		if len(pair[0]) > len(pair[1]) {
			return 1
		}
		if comparison := strings.Compare(pair[0], pair[1]); comparison != 0 {
			return comparison
		}
	}
	return 0
}

func extractReleaseChangelogRange(
	changelog string,
	current canonicalReleaseVersion,
	previous canonicalReleaseVersion,
) (string, error) {
	start := -1
	stop := -1
	offset := 0
	for _, line := range strings.SplitAfter(changelog, "\n") {
		if start < 0 {
			if validDatedReleaseHeading(line, strings.TrimPrefix(current.Tag, "v")) {
				start = offset
			}
		} else if validDatedReleaseHeading(line, strings.TrimPrefix(previous.Tag, "v")) {
			stop = offset
			break
		}
		offset += len(line)
	}
	if start < 0 || stop < 0 || stop <= start {
		return "", fmt.Errorf(
			"CHANGELOG.md does not contain a complete %s..%s release range: %w",
			previous.Tag, current.Tag, errInvalidConfig,
		)
	}
	rangeText := changelog[start:stop]
	hasCategory := false
	hasEntry := false
	for _, line := range strings.Split(rangeText, "\n") {
		hasCategory = hasCategory || strings.HasPrefix(line, "### ")
		hasEntry = hasEntry || strings.HasPrefix(line, "- ")
	}
	if !hasCategory || !hasEntry {
		return "", fmt.Errorf("release changelog range lacks categorized entries: %w", errInvalidConfig)
	}
	return rangeText, nil
}

func validDatedReleaseHeading(line, version string) bool {
	line = strings.TrimSuffix(line, "\n")
	prefix := "## [" + version + "] - "
	if !strings.HasPrefix(line, prefix) {
		return false
	}
	date := strings.TrimPrefix(line, prefix)
	if len(date) != len("2006-01-02") {
		return false
	}
	_, err := time.Parse("2006-01-02", date)
	return err == nil
}

func renderReleaseNotes(owner, repository, currentTag, previousTag, changelogRange string) string {
	repositoryURL := "https://github.com/" + owner + "/" + repository
	return "## GoBFD " + currentTag + "\n\n" + changelogRange + "\n" +
		"**Full changelog:** [CHANGELOG.md at " + currentTag + "](" +
		repositoryURL + "/blob/" + currentTag + "/CHANGELOG.md)\n\n" +
		"**Changes since " + previousTag + ":** [compare " + previousTag + "..." + currentTag + "](" +
		repositoryURL + "/compare/" + previousTag + "..." + currentTag + ")\n"
}

func validateRootedRegularTarget(root *os.Root, name, purpose string) error {
	info, err := root.Lstat(name)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s %s has mode %s: %w", purpose, name, info.Mode(), errInvalidConfig)
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect %s %s: %w", purpose, name, err)
	}
	return nil
}

func decodeJSONDocument(data []byte, destination any, purpose string) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s JSON: %w", purpose, err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode %s JSON trailing data: %w", purpose, errInvalidConfig)
	}
	return nil
}
