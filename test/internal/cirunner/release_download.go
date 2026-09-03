package cirunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
)

const releaseAssetDownloadDirectory = "verified-release-assets"

func downloadExactReleaseAssets(
	ctx context.Context,
	runner SpecRunner,
	commandRoot *os.Root,
	runnerTempRoot *os.Root,
	commandDirectory string,
	runnerTempPath string,
	repository string,
	refName string,
	environment []string,
	expectedAssets []string,
	validateContents func(*os.Root) error,
	commitValidatedContents func() error,
) (returnErr error) {
	if validateContents == nil {
		return fmt.Errorf("release asset content validator is required: %w", errInvalidConfig)
	}
	if commitValidatedContents == nil {
		return fmt.Errorf("release asset commit callback is required: %w", errInvalidConfig)
	}
	if err := validateRootPathIdentity(
		runnerTempRoot, runnerTempPath, "RUNNER_TEMP before release asset download",
	); err != nil {
		return err
	}
	if _, err := runnerTempRoot.Lstat(releaseAssetDownloadDirectory); err == nil {
		return fmt.Errorf("release asset download directory collision: %w", errInvalidConfig)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect release asset download directory: %w", err)
	}
	if err := runnerTempRoot.Mkdir(releaseAssetDownloadDirectory, 0o700); err != nil {
		return fmt.Errorf("create release asset download directory: %w", err)
	}
	createdInfo, err := runnerTempRoot.Lstat(releaseAssetDownloadDirectory)
	if err != nil || !createdInfo.IsDir() {
		return errors.Join(
			fmt.Errorf("inspect created release asset download directory: %w", errors.Join(err, errInvalidConfig)),
			removeOwnedReleaseDownloadRoot(runnerTempRoot, createdInfo),
		)
	}
	downloadRoot, err := runnerTempRoot.OpenRoot(releaseAssetDownloadDirectory)
	if err != nil {
		return errors.Join(
			fmt.Errorf("open release asset download root: %w", err),
			removeOwnedReleaseDownloadRoot(runnerTempRoot, createdInfo),
		)
	}
	openedInfo, err := downloadRoot.Stat(".")
	if err != nil || !os.SameFile(openedInfo, createdInfo) {
		return errors.Join(
			fmt.Errorf("release asset download root identity changed: %w", errors.Join(err, errInvalidConfig)),
			wrapOptional("close unowned release asset download root", downloadRoot.Close()),
			removeOwnedReleaseDownloadRoot(runnerTempRoot, createdInfo),
		)
	}
	keep := false
	defer func() {
		if !keep {
			returnErr = errors.Join(returnErr, clearOwnedReleaseDownloadRoot(downloadRoot))
		}
		returnErr = errors.Join(returnErr, wrapOptional("close release asset download root", downloadRoot.Close()))
		if !keep {
			returnErr = errors.Join(returnErr, removeOwnedReleaseDownloadRoot(runnerTempRoot, createdInfo))
		}
	}()

	downloadPath := filepath.Join(runnerTempPath, releaseAssetDownloadDirectory)
	if err := runner.RunCommand(ctx, CommandSpec{
		Name: "gh",
		Args: []string{
			"release", "download", refName, "--repo", repository, "--dir", downloadPath,
		},
		Dir: commandDirectory, Env: environment,
	}); err != nil {
		return fmt.Errorf("download exact release assets: %w", err)
	}
	if err := validateOwnedReleaseDownloadRoot(
		runnerTempRoot, downloadRoot, createdInfo, expectedAssets,
	); err != nil {
		return err
	}
	if err := validateContents(downloadRoot); err != nil {
		return err
	}
	if err := validateOwnedReleaseDownloadRoot(
		runnerTempRoot, downloadRoot, createdInfo, expectedAssets,
	); err != nil {
		return err
	}
	if err := validateRootPathIdentity(
		commandRoot, commandDirectory, "release verifier root after release asset download",
	); err != nil {
		return err
	}
	if err := validateRootPathIdentity(
		runnerTempRoot, runnerTempPath, "RUNNER_TEMP after release asset download",
	); err != nil {
		return err
	}
	if err := commitValidatedContents(); err != nil {
		return err
	}
	keep = true
	return nil
}

func validateOwnedReleaseDownloadRoot(
	parentRoot *os.Root,
	downloadRoot *os.Root,
	expectedRootInfo os.FileInfo,
	expectedAssets []string,
) error {
	if err := validateReleaseDownloadRootIdentity(parentRoot, downloadRoot, expectedRootInfo); err != nil {
		return err
	}
	directory, err := downloadRoot.Open(".")
	if err != nil {
		return fmt.Errorf("open release asset download directory for inventory: %w", err)
	}
	entries, readErr := directory.ReadDir(len(expectedAssets) + 1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return errors.Join(
			fmt.Errorf("read release asset download directory: %w", readErr),
			wrapOptional("close release asset download directory", closeErr),
		)
	}
	if closeErr != nil {
		return fmt.Errorf("close release asset download directory: %w", closeErr)
	}
	actualAssets := make([]string, 0, len(entries))
	for _, entry := range entries {
		actualAssets = append(actualAssets, entry.Name())
	}
	sort.Strings(actualAssets)
	if !slices.Equal(actualAssets, expectedAssets) {
		return fmt.Errorf("downloaded release asset set differs from the exact manifest: %w", errInvalidConfig)
	}
	for _, name := range expectedAssets {
		info, err := downloadRoot.Lstat(name)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > releaseArtifactLimit {
			return fmt.Errorf(
				"downloaded release asset %s must be a bounded nonempty regular file: %w",
				name, errors.Join(err, errInvalidConfig),
			)
		}
	}
	return validateReleaseDownloadRootIdentity(parentRoot, downloadRoot, expectedRootInfo)
}

func validateReleaseDownloadRootIdentity(
	parentRoot *os.Root,
	downloadRoot *os.Root,
	expectedRootInfo os.FileInfo,
) error {
	currentRootInfo, err := parentRoot.Lstat(releaseAssetDownloadDirectory)
	if err != nil || expectedRootInfo == nil || !currentRootInfo.IsDir() || !os.SameFile(currentRootInfo, expectedRootInfo) {
		return fmt.Errorf("release asset download directory ownership changed: %w", errors.Join(err, errInvalidConfig))
	}
	openedRootInfo, err := downloadRoot.Stat(".")
	if err != nil || !os.SameFile(openedRootInfo, expectedRootInfo) {
		return fmt.Errorf("opened release asset download root ownership changed: %w", errors.Join(err, errInvalidConfig))
	}
	return nil
}

func clearOwnedReleaseDownloadRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return fmt.Errorf("open partial release asset download directory for cleanup: %w", err)
	}
	var result error
	for {
		entries, readErr := directory.ReadDir(32)
		for _, entry := range entries {
			if err := root.RemoveAll(entry.Name()); err != nil {
				result = errors.Join(result, fmt.Errorf("remove partial release asset %s: %w", entry.Name(), err))
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			result = errors.Join(result, fmt.Errorf("read partial release asset download directory: %w", readErr))
			break
		}
	}
	return errors.Join(result, wrapOptional("close partial release asset download directory", directory.Close()))
}

func removeOwnedReleaseDownloadRoot(parentRoot *os.Root, expected os.FileInfo) error {
	current, err := parentRoot.Lstat(releaseAssetDownloadDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect release asset download directory before cleanup: %w", err)
	}
	if expected == nil || !current.IsDir() || !os.SameFile(current, expected) {
		return fmt.Errorf("release asset download directory ownership changed before cleanup: %w", errInvalidConfig)
	}
	if err := parentRoot.Remove(releaseAssetDownloadDirectory); err != nil {
		return fmt.Errorf("remove release asset download directory: %w", err)
	}
	return nil
}
