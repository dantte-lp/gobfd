package gobgp

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dantte-lp/gobfd/internal/gobgp/endpoint"
)

const (
	ownershipSchemaVersion = 1
	maxOwnershipStateBytes = 1 << 20
)

// ErrInvalidOwnershipStore indicates an invalid, corrupt, or untrusted GoBGP
// disable ownership state file.
var ErrInvalidOwnershipStore = errors.New("invalid gobgp disable ownership store")

// SpeakerIdentity binds a GoBGP disable receipt to a single provable local
// GoBGP speaker process epoch (ADR-0013).
type SpeakerIdentity struct {
	Target        string `json:"target"`
	PeerPID       int32  `json:"peer_pid"`
	PeerUID       uint32 `json:"peer_uid"`
	SocketDev     uint64 `json:"socket_dev,omitempty"`
	SocketIno     uint64 `json:"socket_ino,omitempty"`
	BootID        string `json:"boot_id,omitempty"`
	ProcStartTime uint64 `json:"proc_start_time,omitempty"`
	ASN           uint32 `json:"asn"`
	RouterID      string `json:"router_id"`
}

// Provable reports whether s carries a complete local Unix speaker-epoch proof.
// TCP endpoints lack kernel SO_PEERCRED and return false.
func (s SpeakerIdentity) Provable() bool {
	if _, ok := endpoint.LocalUnixSocketPath(s.Target); !ok {
		return false
	}
	return s.PeerPID > 0 && s.ASN > 0 && s.RouterID != ""
}

// Matches reports whether s and live prove the same uninterrupted GoBGP speaker
// process epoch. Any missing proof or mismatched attribute fails closed.
func (s SpeakerIdentity) Matches(live SpeakerIdentity) bool {
	if !s.Provable() || !live.Provable() {
		return false
	}
	if s.Target != live.Target || s.PeerPID != live.PeerPID || s.PeerUID != live.PeerUID ||
		s.ASN != live.ASN || s.RouterID != live.RouterID {
		return false
	}
	return s.matchesKernelEpoch(live)
}

func (s SpeakerIdentity) matchesKernelEpoch(live SpeakerIdentity) bool {
	if (s.SocketIno != 0 || live.SocketIno != 0) &&
		(s.SocketDev != live.SocketDev || s.SocketIno != live.SocketIno) {
		return false
	}
	if (s.BootID != "" || live.BootID != "") && s.BootID != live.BootID {
		return false
	}
	return (s.ProcStartTime == 0 && live.ProcStartTime == 0) || s.ProcStartTime == live.ProcStartTime
}

// OwnershipReceipt records one confirmed GoBGP DisablePeer action owned by GoBFD.
type OwnershipReceipt struct {
	Peer       string          `json:"peer"`
	Speaker    SpeakerIdentity `json:"speaker"`
	DisabledAt time.Time       `json:"disabled_at"`
}

type ownershipFilePayload struct {
	Version  int                         `json:"version"`
	Receipts map[string]OwnershipReceipt `json:"receipts"`
}

// OwnershipStore persists confirmed GoBGP peer disable receipts atomically.
type OwnershipStore struct {
	mu       sync.Mutex
	path     string
	dir      string
	base     string
	receipts map[string]OwnershipReceipt
}

// OpenOwnershipStore validates and loads an existing GoBGP disable ownership
// file, or prepares a new store if the file does not yet exist. Empty statePath
// disables disk persistence and returns (nil, nil, nil).
func OpenOwnershipStore(statePath string) (*OwnershipStore, map[string]OwnershipReceipt, error) {
	if statePath == "" {
		return nil, nil, nil
	}
	dir, base, err := splitOwnershipPath(statePath)
	if err != nil {
		return nil, nil, err
	}
	data, exists, err := readOwnershipFile(dir, base)
	if err != nil {
		return nil, nil, err
	}
	store := &OwnershipStore{
		path:     statePath,
		dir:      dir,
		base:     base,
		receipts: make(map[string]OwnershipReceipt),
	}
	if !exists {
		return store, nil, nil
	}
	loaded, err := decodeOwnershipPayload(data)
	if err != nil {
		return nil, nil, fmt.Errorf("parse gobgp ownership store %s: %w", statePath, err)
	}
	store.receipts = maps.Clone(loaded)
	return store, loaded, nil
}

// Save atomically persists receipt for peer.
func (s *OwnershipStore) Save(peer string, receipt OwnershipReceipt) error {
	if s == nil {
		return nil
	}
	canonical, err := canonicalReceiptPeer(peer)
	if err != nil {
		return err
	}
	receipt.Peer = canonical
	if !receipt.Speaker.Provable() || receipt.DisabledAt.IsZero() {
		return fmt.Errorf("save receipt for %s: %w", canonical, ErrInvalidOwnershipStore)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	next := maps.Clone(s.receipts)
	next[canonical] = receipt
	if err := s.writeSnapshot(next); err != nil {
		return err
	}
	s.receipts = next
	return nil
}

// Delete removes peer from the persisted receipt set.
func (s *OwnershipStore) Delete(peer string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.receipts[peer]; !exists {
		return nil
	}
	next := maps.Clone(s.receipts)
	delete(next, peer)
	if err := s.writeSnapshot(next); err != nil {
		return err
	}
	s.receipts = next
	return nil
}

func (s *OwnershipStore) writeSnapshot(receipts map[string]OwnershipReceipt) error {
	payload := ownershipFilePayload{
		Version:  ownershipSchemaVersion,
		Receipts: receipts,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode gobgp ownership store %s: %w", s.path, err)
	}
	encoded = append(encoded, '\n')
	if err := writeOwnershipFileAtomic(s.dir, s.base, encoded); err != nil {
		return fmt.Errorf("write gobgp ownership store %s: %w", s.path, err)
	}
	return nil
}

func splitOwnershipPath(statePath string) (string, string, error) {
	if strings.TrimSpace(statePath) != statePath || strings.ContainsRune(statePath, 0) ||
		!filepath.IsAbs(statePath) || strings.HasSuffix(statePath, "/") {
		return "", "", fmt.Errorf("ownership path %q: %w", statePath, ErrInvalidOwnershipStore)
	}
	cleaned := filepath.Clean(statePath)
	if cleaned == "/" || cleaned != statePath {
		return "", "", fmt.Errorf("ownership path %q: %w", statePath, ErrInvalidOwnershipStore)
	}
	return filepath.Dir(cleaned), filepath.Base(cleaned), nil
}

func canonicalReceiptPeer(raw string) (string, error) {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return "", fmt.Errorf("invalid receipt peer %q: %w: %w", raw, ErrInvalidOwnershipStore, err)
	}
	canonical := addr.Unmap().String()
	if canonical != raw {
		return "", fmt.Errorf("non-canonical receipt peer %q: %w", raw, ErrInvalidOwnershipStore)
	}
	return canonical, nil
}

func decodeOwnershipPayload(data []byte) (map[string]OwnershipReceipt, error) {
	if len(data) == 0 || len(data) > maxOwnershipStateBytes || !utf8.Valid(data) {
		return nil, fmt.Errorf("invalid byte length or UTF-8: %w", ErrInvalidOwnershipStore)
	}
	if err := rejectDuplicateJSONMembers(data); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var payload ownershipFilePayload
	if err := dec.Decode(&payload); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidOwnershipStore, err)
	}
	if err := ensureDecoderEOF(dec); err != nil {
		return nil, err
	}
	if payload.Version != ownershipSchemaVersion || payload.Receipts == nil {
		return nil, fmt.Errorf("unsupported schema version %d: %w", payload.Version, ErrInvalidOwnershipStore)
	}
	return validateDecodedReceipts(payload.Receipts)
}

func validateDecodedReceipts(raw map[string]OwnershipReceipt) (map[string]OwnershipReceipt, error) {
	validated := make(map[string]OwnershipReceipt, len(raw))
	for key, receipt := range raw {
		canonical, err := canonicalReceiptPeer(key)
		if err != nil {
			return nil, err
		}
		if receipt.Peer != canonical || !receipt.Speaker.Provable() || receipt.DisabledAt.IsZero() {
			return nil, fmt.Errorf("invalid receipt entry %q: %w", key, ErrInvalidOwnershipStore)
		}
		validated[canonical] = receipt
	}
	return validated, nil
}

func ensureDecoderEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("trailing JSON data: %w", ErrInvalidOwnershipStore)
	}
	return nil
}

type jsonObjectFrame struct {
	isObject bool
	expect   bool
	seen     map[string]struct{}
}

type jsonDupTracker struct {
	stack []jsonObjectFrame
}

func (t *jsonDupTracker) consumeValueSlot() {
	if len(t.stack) == 0 {
		return
	}
	top := &t.stack[len(t.stack)-1]
	if top.isObject && !top.expect {
		top.expect = true
	}
}

func (t *jsonDupTracker) stepDelim(delim json.Delim) error {
	switch delim {
	case '{':
		t.consumeValueSlot()
		t.stack = append(t.stack, jsonObjectFrame{
			isObject: true,
			expect:   true,
			seen:     make(map[string]struct{}),
		})
	case '[':
		t.consumeValueSlot()
		t.stack = append(t.stack, jsonObjectFrame{isObject: false})
	case '}', ']':
		if len(t.stack) == 0 {
			return fmt.Errorf("unbalanced JSON delimiter: %w", ErrInvalidOwnershipStore)
		}
		t.stack = t.stack[:len(t.stack)-1]
	}
	return nil
}

func (t *jsonDupTracker) stepString(token string) error {
	if len(t.stack) == 0 || !t.stack[len(t.stack)-1].isObject || !t.stack[len(t.stack)-1].expect {
		t.consumeValueSlot()
		return nil
	}
	top := &t.stack[len(t.stack)-1]
	if _, dup := top.seen[token]; dup {
		return fmt.Errorf("duplicate JSON object member %q: %w", token, ErrInvalidOwnershipStore)
	}
	top.seen[token] = struct{}{}
	top.expect = false
	return nil
}

// rejectDuplicateJSONMembers enforces duplicate-member rejection consistently
// across encoding/json v1-over-v2 implementations (AGENTS.md Go 1.27 contract).
func rejectDuplicateJSONMembers(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	var tracker jsonDupTracker
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidOwnershipStore, err)
		}
		switch val := tok.(type) {
		case json.Delim:
			if stepErr := tracker.stepDelim(val); stepErr != nil {
				return stepErr
			}
		case string:
			if stepErr := tracker.stepString(val); stepErr != nil {
				return stepErr
			}
		default:
			tracker.consumeValueSlot()
		}
	}
}

func randomTempOwnershipName(base string) (string, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "", fmt.Errorf("generate temporary file nonce: %w", err)
	}
	return fmt.Sprintf(".%s-%x.tmp", base, nonce[:]), nil
}

func removeIfPresent(root *os.Root, name string) error {
	err := root.Remove(name)
	if err == nil || errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove temporary ownership file %s: %w", name, err)
}
