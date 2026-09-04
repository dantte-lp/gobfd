package cirunner

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	releaseAssetIdentityReceiptName  = "verified-release-asset-identities.json"
	releaseAssetIdentityReceiptLimit = 64 << 10
)

type releaseRemoteAssetIdentity struct {
	NodeID     string
	DatabaseID uint64
	Name       string
	Size       int64
	Digest     string
	State      string
}

type releaseAssetIdentityReceipt struct {
	SchemaVersion int                          `json:"schema_version"`
	Tag           string                       `json:"tag"`
	Assets        []releaseAssetIdentityRecord `json:"assets"`
}

type releaseAssetIdentityRecord struct {
	NodeID     string `json:"node_id"`
	DatabaseID uint64 `json:"database_id"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Digest     string `json:"digest"`
	State      string `json:"state"`
}

func validateRemoteReleaseAsset(
	asset map[string]json.RawMessage,
	index int,
	repository string,
) (releaseRemoteAssetIdentity, error) {
	if err := validateRemoteReleaseAssetFields(asset, index); err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	nodeID, err := decodeRequiredJSONString(asset["id"], "release draft asset node ID")
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	if !canonicalReleaseAssetNodeID(nodeID) {
		return releaseRemoteAssetIdentity{}, fmt.Errorf("release draft asset node ID is not canonical: %w", errInvalidConfig)
	}
	apiURL, err := decodeRequiredJSONString(asset["apiUrl"], "release draft asset API URL")
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	databaseID, err := parseReleaseAssetDatabaseID(apiURL, repository)
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	name, err := decodeRequiredJSONString(asset["name"], "release draft asset name")
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	var size *int64
	if decodeErr := decodeJSONDocument(asset["size"], &size, "release draft asset size"); decodeErr != nil {
		return releaseRemoteAssetIdentity{}, decodeErr
	}
	if size == nil || *size <= 0 || *size > releaseArtifactLimit {
		return releaseRemoteAssetIdentity{}, fmt.Errorf("release draft asset size is outside bounds: %w", errInvalidConfig)
	}
	digest, err := decodeRequiredJSONString(asset["digest"], "release draft asset digest")
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	if !canonicalOCIDigest(digest) {
		return releaseRemoteAssetIdentity{}, fmt.Errorf("release draft asset digest is not canonical SHA-256: %w", errInvalidConfig)
	}
	state, err := decodeRequiredJSONString(asset["state"], "release draft asset state")
	if err != nil {
		return releaseRemoteAssetIdentity{}, err
	}
	if state != "uploaded" {
		return releaseRemoteAssetIdentity{}, fmt.Errorf("release draft asset state is not uploaded: %w", errInvalidConfig)
	}
	return releaseRemoteAssetIdentity{
		NodeID: nodeID, DatabaseID: databaseID, Name: name, Size: *size, Digest: digest, State: state,
	}, nil
}

func validateRemoteReleaseAssetFields(asset map[string]json.RawMessage, index int) error {
	fields := []string{"apiUrl", "digest", "id", "name", "size", "state"}
	if err := rejectJSONFieldAliases(asset, fields); err != nil {
		return fmt.Errorf("release draft asset %d: %w", index, err)
	}
	for _, field := range fields {
		if _, exists := asset[field]; !exists {
			return fmt.Errorf("release draft asset %d lacks %s: %w", index, field, errInvalidConfig)
		}
	}
	return nil
}

func canonicalReleaseAssetNodeID(value string) bool {
	if value == "" || len(value) > 256 || hasControl(value) || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	return true
}

func parseReleaseAssetDatabaseID(apiURL, repository string) (uint64, error) {
	prefix := "https://api.github.com/repos/" + repository + "/releases/assets/"
	idText, found := strings.CutPrefix(apiURL, prefix)
	if !found || idText == "" || (len(idText) > 1 && idText[0] == '0') {
		return 0, fmt.Errorf("release draft asset API URL is not canonical: %w", errInvalidConfig)
	}
	id, err := strconv.ParseUint(idText, 10, 64)
	if err != nil || id == 0 {
		return 0, fmt.Errorf("release draft asset API URL lacks a positive REST ID: %w", errors.Join(err, errInvalidConfig))
	}
	return id, nil
}

func renderReleaseAssetIdentityReceipt(
	refName string,
	remote []releaseRemoteAssetIdentity,
	local map[string]releaseAssetSnapshot,
) ([]byte, error) {
	receipt := releaseAssetIdentityReceipt{SchemaVersion: 1, Tag: refName}
	receipt.Assets = make([]releaseAssetIdentityRecord, 0, len(remote))
	for _, asset := range remote {
		snapshot, exists := local[asset.Name]
		if !exists {
			return nil, fmt.Errorf("release asset %s lacks a validated local snapshot: %w", asset.Name, errInvalidConfig)
		}
		if asset.Size != snapshot.Size {
			return nil, fmt.Errorf("release asset %s size differs from validated local snapshot: %w", asset.Name, errInvalidConfig)
		}
		if asset.Digest != snapshot.Digest {
			return nil, fmt.Errorf("release asset %s digest differs from validated local snapshot: %w", asset.Name, errInvalidConfig)
		}
		receipt.Assets = append(receipt.Assets, releaseAssetIdentityRecord(asset))
	}
	if len(receipt.Assets) != len(local) {
		return nil, fmt.Errorf("release asset identity receipt set is incomplete: %w", errInvalidConfig)
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode release asset identity receipt: %w", err)
	}
	data = append(data, '\n')
	if len(data) > releaseAssetIdentityReceiptLimit {
		return nil, fmt.Errorf("release asset identity receipt exceeds %d bytes: %w", releaseAssetIdentityReceiptLimit, errInvalidConfig)
	}
	return data, nil
}

func parseReleaseAssetIdentityReceipt(
	data []byte,
	refName string,
	expectedAssets []string,
	remote []releaseRemoteAssetIdentity,
) ([]releaseAssetIdentityRecord, error) {
	rawRecords, err := parseReleaseAssetIdentityReceiptRecords(data, refName)
	if err != nil {
		return nil, err
	}
	if len(rawRecords) != len(expectedAssets) || len(remote) != len(expectedAssets) {
		return nil, fmt.Errorf("release asset identity receipt set is incomplete: %w", errInvalidConfig)
	}
	records := make([]releaseAssetIdentityRecord, 0, len(rawRecords))
	nodeIDs := make(map[string]struct{}, len(rawRecords))
	databaseIDs := make(map[uint64]struct{}, len(rawRecords))
	for index, raw := range rawRecords {
		record, err := parseReleaseAssetIdentityRecord(raw, index)
		if err != nil {
			return nil, err
		}
		if _, exists := nodeIDs[record.NodeID]; exists {
			return nil, fmt.Errorf("release asset identity receipt duplicates node ID: %w", errInvalidConfig)
		}
		if _, exists := databaseIDs[record.DatabaseID]; exists {
			return nil, fmt.Errorf("release asset identity receipt duplicates REST ID: %w", errInvalidConfig)
		}
		nodeIDs[record.NodeID] = struct{}{}
		databaseIDs[record.DatabaseID] = struct{}{}
		if record.Name != expectedAssets[index] || !releaseAssetIdentityMatchesRemote(record, remote[index]) {
			return nil, fmt.Errorf(
				"release asset identity receipt record %d differs from exact remote asset: %w",
				index, errInvalidConfig,
			)
		}
		records = append(records, record)
	}
	return records, nil
}

func parseReleaseAssetIdentityReceiptRecords(data []byte, refName string) ([]map[string]json.RawMessage, error) {
	if err := validateStrictJSONDocument(data, "release asset identity receipt"); err != nil {
		return nil, err
	}
	requiredFields := []string{"schema_version", "tag", "assets"}
	root, err := decodeRequiredJSONObject(
		data, "release asset identity receipt", requiredFields,
	)
	if err != nil {
		return nil, err
	}
	if len(root) != len(requiredFields) {
		return nil, fmt.Errorf("release asset identity receipt has unexpected fields: %w", errInvalidConfig)
	}
	var schemaVersion *int
	if decodeErr := decodeJSONDocument(
		root["schema_version"], &schemaVersion, "release asset identity receipt schema",
	); decodeErr != nil {
		return nil, decodeErr
	}
	if schemaVersion == nil || *schemaVersion != 1 {
		return nil, fmt.Errorf("release asset identity receipt schema is not version 1: %w", errInvalidConfig)
	}
	tag, err := decodeRequiredJSONString(root["tag"], "release asset identity receipt tag")
	if err != nil {
		return nil, err
	}
	if tag != refName {
		return nil, fmt.Errorf("release asset identity receipt tag differs from release tag: %w", errInvalidConfig)
	}
	rawRecords := []map[string]json.RawMessage{}
	if err := decodeJSONDocument(root["assets"], &rawRecords, "release asset identity receipt assets"); err != nil {
		return nil, err
	}
	return rawRecords, nil
}

func parseReleaseAssetIdentityRecord(
	raw map[string]json.RawMessage,
	index int,
) (releaseAssetIdentityRecord, error) {
	if err := validateReleaseAssetIdentityRecordFields(raw, index); err != nil {
		return releaseAssetIdentityRecord{}, err
	}
	nodeID, err := decodeRequiredJSONString(raw["node_id"], "release asset identity receipt node ID")
	if err != nil || !canonicalReleaseAssetNodeID(nodeID) {
		return releaseAssetIdentityRecord{}, fmt.Errorf(
			"release asset identity receipt node ID is not canonical: %w",
			errors.Join(err, errInvalidConfig),
		)
	}
	var databaseID *uint64
	if decodeErr := decodeJSONDocument(
		raw["database_id"], &databaseID, "release asset identity receipt REST ID",
	); decodeErr != nil {
		return releaseAssetIdentityRecord{}, decodeErr
	}
	if databaseID == nil || *databaseID == 0 {
		return releaseAssetIdentityRecord{}, fmt.Errorf(
			"release asset identity receipt REST ID is not positive: %w", errInvalidConfig,
		)
	}
	name, err := decodeRequiredJSONString(raw["name"], "release asset identity receipt name")
	if err != nil {
		return releaseAssetIdentityRecord{}, err
	}
	var size *int64
	if decodeErr := decodeJSONDocument(raw["size"], &size, "release asset identity receipt size"); decodeErr != nil {
		return releaseAssetIdentityRecord{}, decodeErr
	}
	if size == nil || *size <= 0 || *size > releaseArtifactLimit {
		return releaseAssetIdentityRecord{}, fmt.Errorf(
			"release asset identity receipt size is outside bounds: %w", errInvalidConfig,
		)
	}
	digest, err := decodeRequiredJSONString(raw["digest"], "release asset identity receipt digest")
	if err != nil || !canonicalOCIDigest(digest) {
		return releaseAssetIdentityRecord{}, fmt.Errorf(
			"release asset identity receipt digest is not canonical: %w",
			errors.Join(err, errInvalidConfig),
		)
	}
	state, err := decodeReceiptAssetState(raw["state"])
	if err != nil {
		return releaseAssetIdentityRecord{}, err
	}
	return releaseAssetIdentityRecord{
		NodeID: nodeID, DatabaseID: *databaseID, Name: name, Size: *size, Digest: digest, State: state,
	}, nil
}

func validateReleaseAssetIdentityRecordFields(raw map[string]json.RawMessage, index int) error {
	fields := []string{"node_id", "database_id", "name", "size", "digest", "state"}
	if raw == nil {
		return fmt.Errorf("release asset identity receipt record %d is not an object: %w", index, errInvalidConfig)
	}
	if err := rejectJSONFieldAliases(raw, fields); err != nil {
		return fmt.Errorf("release asset identity receipt record %d: %w", index, err)
	}
	if len(raw) != len(fields) {
		return fmt.Errorf("release asset identity receipt record %d has unexpected fields: %w", index, errInvalidConfig)
	}
	for _, field := range fields {
		if _, exists := raw[field]; !exists {
			return fmt.Errorf(
				"release asset identity receipt record %d lacks %s: %w", index, field, errInvalidConfig,
			)
		}
	}
	return nil
}

func decodeReceiptAssetState(data []byte) (string, error) {
	state, err := decodeRequiredJSONString(data, "release asset identity receipt state")
	if err != nil || state != "uploaded" {
		return "", fmt.Errorf(
			"release asset identity receipt state is not uploaded: %w",
			errors.Join(err, errInvalidConfig),
		)
	}
	return state, nil
}

func releaseAssetIdentityMatchesRemote(record releaseAssetIdentityRecord, remote releaseRemoteAssetIdentity) bool {
	return record.NodeID == remote.NodeID && record.DatabaseID == remote.DatabaseID && record.Name == remote.Name &&
		record.Size == remote.Size && record.Digest == remote.Digest && record.State == remote.State
}

func publishReleaseAssetIdentityReceipt(
	root *os.Root,
	data []byte,
) (published os.FileInfo, returnErr error) {
	if root == nil || len(data) > releaseAssetIdentityReceiptLimit {
		return nil, fmt.Errorf("release asset identity receipt exceeds its bounded contract: %w", errInvalidConfig)
	}
	temporary, temporaryName, ownedTemporaryInfo, err := createReleaseAssetIdentityTemporary(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			removeOwnedRootedArtifact(
				root, temporaryName, ownedTemporaryInfo, "temporary release asset identity receipt",
			),
		)
	}()
	if err := writeReleaseAssetIdentityTemporary(temporary, data); err != nil {
		return nil, err
	}
	if err := validateReleaseAssetIdentityTemporary(
		root, temporaryName, ownedTemporaryInfo, int64(len(data)),
	); err != nil {
		return nil, err
	}
	if err := root.Link(temporaryName, releaseAssetIdentityReceiptName); err != nil {
		return nil, fmt.Errorf("publish release asset identity receipt without replacement: %w", err)
	}
	return inspectPublishedReleaseAssetIdentity(root, temporaryName, ownedTemporaryInfo)
}

func createReleaseAssetIdentityTemporary(root *os.Root) (*os.File, string, os.FileInfo, error) {
	random := [16]byte{}
	if _, err := rand.Read(random[:]); err != nil {
		return nil, "", nil, fmt.Errorf("generate temporary release asset identity receipt name: %w", err)
	}
	temporaryName := "." + releaseAssetIdentityReceiptName + "-" + hex.EncodeToString(random[:])
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", nil, fmt.Errorf("create temporary release asset identity receipt: %w", err)
	}
	ownedTemporaryInfo, err := temporary.Stat()
	if err != nil || !ownedTemporaryInfo.Mode().IsRegular() {
		return nil, "", nil, errors.Join(
			fmt.Errorf(
				"inspect owned temporary release asset identity receipt: %w",
				errors.Join(err, errInvalidConfig),
			),
			wrapOptional("close temporary release asset identity receipt", temporary.Close()),
		)
	}
	return temporary, temporaryName, ownedTemporaryInfo, nil
}

func writeReleaseAssetIdentityTemporary(temporary *os.File, data []byte) error {
	if chmodErr := temporary.Chmod(benchmarkArtifactMode); chmodErr != nil {
		return errors.Join(
			fmt.Errorf("set temporary release asset identity receipt mode: %w", chmodErr),
			wrapOptional("close temporary release asset identity receipt", temporary.Close()),
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
			wrapOptional("write temporary release asset identity receipt", writeErr),
			wrapOptional("close temporary release asset identity receipt", closeErr),
		)
	}
	return nil
}

func validateReleaseAssetIdentityTemporary(
	root *os.Root,
	name string,
	expected os.FileInfo,
	size int64,
) error {
	temporaryInfo, err := root.Lstat(name)
	if err != nil || !temporaryInfo.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, expected) ||
		temporaryInfo.Mode().Perm() != benchmarkArtifactMode || temporaryInfo.Size() != size {
		return fmt.Errorf(
			"temporary release asset identity receipt violates mode or size contract: %w",
			errors.Join(err, errInvalidConfig),
		)
	}
	return nil
}

func inspectPublishedReleaseAssetIdentity(
	root *os.Root,
	temporaryName string,
	expected os.FileInfo,
) (os.FileInfo, error) {
	currentTemporary, temporaryErr := root.Lstat(temporaryName)
	currentPublished, publishedErr := root.Lstat(releaseAssetIdentityReceiptName)
	if publishedErr != nil {
		return nil, fmt.Errorf(
			"inspect published release asset identity receipt ownership: %w",
			errors.Join(publishedErr, errInvalidConfig),
		)
	}
	if os.SameFile(currentPublished, expected) {
		if temporaryErr != nil {
			return currentPublished, fmt.Errorf(
				"inspect temporary release asset identity receipt after publish: %w",
				temporaryErr,
			)
		}
	} else {
		return nil, fmt.Errorf(
			"published release asset identity receipt ownership changed: %w",
			errInvalidConfig,
		)
	}
	if !os.SameFile(currentTemporary, expected) {
		return currentPublished, fmt.Errorf(
			"temporary release asset identity receipt ownership changed after publish: %w",
			errInvalidConfig,
		)
	}
	return currentPublished, nil
}
