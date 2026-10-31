package cirunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

func verifyGoReleaserReceipt(installation *sourceInstallation, profile goreleaserProfile) error {
	data, err := readGoReleaserFile(installation.root, goreleaserProvenance)
	if err != nil {
		return err
	}
	if err := validateStrictJSONDocument(data, "publisher source provenance"); err != nil {
		return err
	}
	var receipt goreleaserReceipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return fmt.Errorf("decode publisher source provenance: %w", err)
	}
	if !reflect.DeepEqual(receipt.Profile, profile) || receipt.OriginalFiles != profile.Files ||
		receipt.OriginalBytes != profile.Bytes || receipt.PatchedFiles != profile.Files || receipt.PatchedBytes <= 0 ||
		receipt.PatchedBytes > profile.Bytes+2*goreleaserFileLimit || !validGoReleaserHash(receipt.PatchedContentSum) {
		return fmt.Errorf("publisher provenance differs from authenticated source/manifests: %w", errInvalidConfig)
	}
	if receipt.ConsumerContentSum != profile.consumerContentSum() {
		return fmt.Errorf("publisher consumer provenance differs from qualified manifest role: %w", errInvalidConfig)
	}
	if err := receipt.Refmt.validate(profile.Refmt.sourceProfile()); err != nil {
		return err
	}
	return installation.verify()
}
