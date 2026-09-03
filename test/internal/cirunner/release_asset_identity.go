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
	fields := []string{"apiUrl", "digest", "id", "name", "size", "state"}
	if err := rejectJSONFieldAliases(asset, fields); err != nil {
		return releaseRemoteAssetIdentity{}, fmt.Errorf("release draft asset %d: %w", index, err)
	}
	for _, field := range fields {
		if _, exists := asset[field]; !exists {
			return releaseRemoteAssetIdentity{}, fmt.Errorf(
				"release draft asset %d lacks %s: %w", index, field, errInvalidConfig,
			)
		}
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
	if err := decodeJSONDocument(asset["size"], &size, "release draft asset size"); err != nil {
		return releaseRemoteAssetIdentity{}, err
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
		receipt.Assets = append(receipt.Assets, releaseAssetIdentityRecord{
			NodeID: asset.NodeID, DatabaseID: asset.DatabaseID, Name: asset.Name,
			Size: asset.Size, Digest: asset.Digest, State: asset.State,
		})
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
	if err := validateStrictJSONDocument(data, "release asset identity receipt"); err != nil {
		return nil, err
	}
	root, err := decodeRequiredJSONObject(
		data, "release asset identity receipt", []string{"schema_version", "tag", "assets"},
	)
	if err != nil {
		return nil, err
	}
	if len(root) != 3 {
		return nil, fmt.Errorf("release asset identity receipt has unexpected fields: %w", errInvalidConfig)
	}
	var schemaVersion *int
	if err := decodeJSONDocument(root["schema_version"], &schemaVersion, "release asset identity receipt schema"); err != nil {
		return nil, err
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
	if len(rawRecords) != len(expectedAssets) || len(remote) != len(expectedAssets) {
		return nil, fmt.Errorf("release asset identity receipt set is incomplete: %w", errInvalidConfig)
	}
	records := make([]releaseAssetIdentityRecord, 0, len(rawRecords))
	nodeIDs := make(map[string]struct{}, len(rawRecords))
	databaseIDs := make(map[uint64]struct{}, len(rawRecords))
	fields := []string{"node_id", "database_id", "name", "size", "digest", "state"}
	for index, raw := range rawRecords {
		if raw == nil {
			return nil, fmt.Errorf("release asset identity receipt record %d is not an object: %w", index, errInvalidConfig)
		}
		if err := rejectJSONFieldAliases(raw, fields); err != nil {
			return nil, fmt.Errorf("release asset identity receipt record %d: %w", index, err)
		}
		if len(raw) != len(fields) {
			return nil, fmt.Errorf("release asset identity receipt record %d has unexpected fields: %w", index, errInvalidConfig)
		}
		for _, field := range fields {
			if _, exists := raw[field]; !exists {
				return nil, fmt.Errorf(
					"release asset identity receipt record %d lacks %s: %w", index, field, errInvalidConfig,
				)
			}
		}
		nodeID, err := decodeRequiredJSONString(raw["node_id"], "release asset identity receipt node ID")
		if err != nil || !canonicalReleaseAssetNodeID(nodeID) {
			return nil, fmt.Errorf("release asset identity receipt node ID is not canonical: %w", errors.Join(err, errInvalidConfig))
		}
		var databaseID *uint64
		if err := decodeJSONDocument(raw["database_id"], &databaseID, "release asset identity receipt REST ID"); err != nil {
			return nil, err
		}
		if databaseID == nil || *databaseID == 0 {
			return nil, fmt.Errorf("release asset identity receipt REST ID is not positive: %w", errInvalidConfig)
		}
		name, err := decodeRequiredJSONString(raw["name"], "release asset identity receipt name")
		if err != nil {
			return nil, err
		}
		var size *int64
		if err := decodeJSONDocument(raw["size"], &size, "release asset identity receipt size"); err != nil {
			return nil, err
		}
		if size == nil || *size <= 0 || *size > releaseArtifactLimit {
			return nil, fmt.Errorf("release asset identity receipt size is outside bounds: %w", errInvalidConfig)
		}
		digest, err := decodeRequiredJSONString(raw["digest"], "release asset identity receipt digest")
		if err != nil || !canonicalOCIDigest(digest) {
			return nil, fmt.Errorf("release asset identity receipt digest is not canonical: %w", errors.Join(err, errInvalidConfig))
		}
		state, err := decodeRequiredJSONString(raw["state"], "release asset identity receipt state")
		if err != nil || state != "uploaded" {
			return nil, fmt.Errorf("release asset identity receipt state is not uploaded: %w", errors.Join(err, errInvalidConfig))
		}
		if _, exists := nodeIDs[nodeID]; exists {
			return nil, fmt.Errorf("release asset identity receipt duplicates node ID: %w", errInvalidConfig)
		}
		if _, exists := databaseIDs[*databaseID]; exists {
			return nil, fmt.Errorf("release asset identity receipt duplicates REST ID: %w", errInvalidConfig)
		}
		nodeIDs[nodeID] = struct{}{}
		databaseIDs[*databaseID] = struct{}{}
		record := releaseAssetIdentityRecord{
			NodeID: nodeID, DatabaseID: *databaseID, Name: name, Size: *size, Digest: digest, State: state,
		}
		if name != expectedAssets[index] || !releaseAssetIdentityMatchesRemote(record, remote[index]) {
			return nil, fmt.Errorf("release asset identity receipt record %d differs from exact remote asset: %w", index, errInvalidConfig)
		}
		records = append(records, record)
	}
	return records, nil
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
	random := [16]byte{}
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("generate temporary release asset identity receipt name: %w", err)
	}
	temporaryName := "." + releaseAssetIdentityReceiptName + "-" + hex.EncodeToString(random[:])
	temporary, err := root.OpenFile(temporaryName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create temporary release asset identity receipt: %w", err)
	}
	ownedTemporaryInfo, err := temporary.Stat()
	if err != nil || !ownedTemporaryInfo.Mode().IsRegular() {
		return nil, errors.Join(
			fmt.Errorf(
				"inspect owned temporary release asset identity receipt: %w",
				errors.Join(err, errInvalidConfig),
			),
			wrapOptional("close temporary release asset identity receipt", temporary.Close()),
		)
	}
	defer func() {
		returnErr = errors.Join(
			returnErr,
			removeOwnedRootedArtifact(
				root, temporaryName, ownedTemporaryInfo, "temporary release asset identity receipt",
			),
		)
	}()
	if err := temporary.Chmod(benchmarkArtifactMode); err != nil {
		return nil, errors.Join(
			fmt.Errorf("set temporary release asset identity receipt mode: %w", err),
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
		return nil, errors.Join(
			wrapOptional("write temporary release asset identity receipt", writeErr),
			wrapOptional("close temporary release asset identity receipt", closeErr),
		)
	}
	temporaryInfo, err := root.Lstat(temporaryName)
	if err != nil || !temporaryInfo.Mode().IsRegular() ||
		!os.SameFile(temporaryInfo, ownedTemporaryInfo) ||
		temporaryInfo.Mode().Perm() != benchmarkArtifactMode || temporaryInfo.Size() != int64(len(data)) {
		return nil, fmt.Errorf(
			"temporary release asset identity receipt violates mode or size contract: %w",
			errors.Join(err, errInvalidConfig),
		)
	}
	if err := root.Link(temporaryName, releaseAssetIdentityReceiptName); err != nil {
		return nil, fmt.Errorf("publish release asset identity receipt without replacement: %w", err)
	}
	currentTemporary, temporaryErr := root.Lstat(temporaryName)
	currentPublished, publishedErr := root.Lstat(releaseAssetIdentityReceiptName)
	if publishedErr != nil {
		return nil, fmt.Errorf(
			"inspect published release asset identity receipt ownership: %w",
			errors.Join(publishedErr, errInvalidConfig),
		)
	}
	if os.SameFile(currentPublished, ownedTemporaryInfo) {
		published = currentPublished
		if temporaryErr != nil {
			return published, fmt.Errorf(
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
	if !os.SameFile(currentTemporary, ownedTemporaryInfo) {
		return published, fmt.Errorf(
			"temporary release asset identity receipt ownership changed after publish: %w",
			errInvalidConfig,
		)
	}
	return published, nil
}
