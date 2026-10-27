//go:build interop_testcontainers

package interop_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/dantte-lp/gobfd/internal/apiclient"
	bfdv1 "github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1"
	"github.com/dantte-lp/gobfd/pkg/bfdpb/bfd/v1/bfdv1connect"
)

// TestSameKeyAPIProcess runs only as an explicit child inside the owned daemon container.
func TestSameKeyAPIProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Second)
	defer cancel()
	receipt, err := runSameKeyAPI(ctx)
	raw, marshalErr := json.Marshal(receipt)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	t.Logf("SAME_KEY_API=%s", raw)
	if err != nil {
		t.Fatal(err)
	}
}

func sameKeyFRRRequest() *bfdv1.AddSessionRequest {
	return &bfdv1.AddSessionRequest{
		PeerAddress: frrIP, LocalAddress: gobfdIP, Type: bfdv1.SessionType_SESSION_TYPE_SINGLE_HOP,
		DesiredMinTxInterval:  durationpb.New(300 * time.Millisecond),
		RequiredMinRxInterval: durationpb.New(300 * time.Millisecond), DetectMultiplier: 3,
		AuthType:  bfdv1.AuthenticationType_AUTHENTICATION_TYPE_SIMPLE_PASSWORD,
		AuthKeyId: 7, AuthSecret: []byte("gobfd-interop"), PaddedPduSize: 128,
	}
}

func runSameKeyAPI(ctx context.Context) (sameKeyAPIReceipt, error) {
	var receipt sameKeyAPIReceipt
	raw, err := readSameKeyBoundedFile("/tmp/same-key-api-input.json")
	if err != nil {
		return receipt, err
	}
	if err = json.Unmarshal(raw, &receipt); err != nil {
		return receipt, fmt.Errorf("decode same-key input: %w", err)
	}
	if receipt.OldLocal == 0 || receipt.OldRemote == 0 || receipt.OldPort < 49152 || !receipt.OldUp {
		return receipt, fmt.Errorf("missing initial same-key identity: %w", errTimerEvidence)
	}
	endpoint, err := apiclient.ParseEndpoint("unix:///run/gobfd/gobfd.sock")
	if err != nil {
		return receipt, err
	}
	httpClient := endpoint.HTTPClient()
	defer httpClient.CloseIdleConnections()
	client := bfdv1connect.NewBfdServiceClient(connect.NewClient(connecthttp.NewTransport(httpClient, endpoint.BaseURL)))
	prior, err := sameKeyAPIUp(ctx, client, receipt.OldLocal)
	if err != nil || prior.GetRemoteDiscriminator() != receipt.OldRemote {
		return receipt, fmt.Errorf("initial same-key API epoch changed: %w", errors.Join(err, errTimerEvidence))
	}
	table, fds, err := readSameKeySockets()
	if err != nil {
		return receipt, err
	}
	receipt.OldSocketInode, err = sameKeySenderInode(table, fds, receipt.OldPort)
	if err != nil {
		return receipt, err
	}
	deleteCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	done := make(chan error, 1)
	go func() {
		_, deleteErr := client.DeleteSession(deleteCtx, &bfdv1.DeleteSessionRequest{LocalDiscriminator: receipt.OldLocal})
		done <- deleteErr
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			<-done
		}
	}()
	_, err = pollUntil(ctx, 25*time.Millisecond, func(pollCtx context.Context) (bool, bool, error) {
		_, getErr := client.GetSession(pollCtx, &bfdv1.GetSessionRequest{
			Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{LocalDiscriminator: receipt.OldLocal},
		})
		absent := connect.CodeOf(getErr) == connect.CodeNotFound
		if getErr != nil && !absent {
			return false, false, getErr
		}
		return absent, absent, nil
	})
	if err != nil {
		return receipt, fmt.Errorf("observe retiring same-key owner: %w", err)
	}
	receipt.OldDetached = true
	_, fds, err = readSameKeySockets()
	if err != nil {
		return receipt, err
	}
	oldSocket := fmt.Sprintf("socket:[%d]", receipt.OldSocketInode)
	receipt.OldSocketBeforeConflict = slices.Contains(fds, oldSocket)
	if !receipt.OldSocketBeforeConflict {
		return receipt, fmt.Errorf("old socket closed before concurrent Add: %w", errTimerEvidence)
	}
	_, conflictErr := client.AddSession(ctx, sameKeyFRRRequest())
	receipt.DuplicateCode = connect.CodeOf(conflictErr)
	_, fds, err = readSameKeySockets()
	if err != nil {
		return receipt, err
	}
	receipt.OldSocketAfterConflict = slices.Contains(fds, oldSocket)
	if receipt.DuplicateCode != connect.CodeAlreadyExists || !receipt.OldSocketAfterConflict {
		return receipt, fmt.Errorf("concurrent Add lacked retiring socket/AlreadyExists evidence: %w", errTimerEvidence)
	}
	deleteErr := <-done
	joined = true
	if deleteErr != nil {
		return receipt, fmt.Errorf("delete old same-key owner: %w", deleteErr)
	}
	receipt.DeleteComplete = true
	_, fds, err = readSameKeySockets()
	if err != nil {
		return receipt, err
	}
	receipt.OldSocketAfterDelete = slices.Contains(fds, oldSocket)
	if receipt.OldSocketAfterDelete {
		return receipt, fmt.Errorf("old sender inode survives successful Delete: %w", errTimerEvidence)
	}
	receipt.ClosedBeforeRetry = true
	next, err := client.AddSession(ctx, sameKeyFRRRequest())
	if err != nil {
		return receipt, fmt.Errorf("retry after joined same-key Delete: %w", err)
	}
	receipt.NewLocal = next.GetSession().GetLocalDiscriminator()
	if receipt.NewLocal == 0 || receipt.NewLocal == receipt.OldLocal {
		return receipt, fmt.Errorf("distinct new epoch not observed: %w", errTimerEvidence)
	}
	current, err := sameKeyAPIUp(ctx, client, receipt.NewLocal)
	if err != nil {
		return receipt, err
	}
	receipt.NewRemote, receipt.NewUp = current.GetRemoteDiscriminator(), true
	return receipt, nil
}

func sameKeyAPIUp(ctx context.Context, client bfdv1connect.BfdServiceClient, discr uint32) (*bfdv1.BfdSession, error) {
	return pollUntil(ctx, 25*time.Millisecond, func(pollCtx context.Context) (*bfdv1.BfdSession, bool, error) {
		response, err := client.GetSession(pollCtx, &bfdv1.GetSessionRequest{
			Identifier: &bfdv1.GetSessionRequest_LocalDiscriminator{LocalDiscriminator: discr},
		})
		if err != nil {
			return nil, false, err
		}
		session := response.GetSession()
		return session, session.GetLocalState() == bfdv1.SessionState_SESSION_STATE_UP &&
			session.GetRemoteState() == bfdv1.SessionState_SESSION_STATE_UP && session.GetRemoteDiscriminator() != 0, nil
	})
}

func readSameKeyBoundedFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open same-key evidence: %w", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return nil, fmt.Errorf("read same-key evidence: %w", err)
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("same-key evidence exceeds 64 KiB: %w", errTimerEvidence)
	}
	return raw, nil
}

func readSameKeySockets() (string, []string, error) {
	raw, err := readSameKeyBoundedFile("/proc/1/net/udp")
	if err != nil {
		return "", nil, err
	}
	directory, err := os.Open("/proc/1/fd")
	if err != nil {
		return "", nil, fmt.Errorf("open daemon descriptors: %w", err)
	}
	entries, readErr := directory.ReadDir(257)
	if errors.Is(readErr, io.EOF) {
		readErr = nil
	}
	if err = errors.Join(readErr, directory.Close()); err != nil {
		return "", nil, fmt.Errorf("read daemon descriptors: %w", err)
	}
	if len(entries) > 256 {
		return "", nil, fmt.Errorf("daemon descriptor observation exceeds 256: %w", errTimerEvidence)
	}
	targets := make([]string, 0, len(entries))
	for _, entry := range entries {
		target, linkErr := os.Readlink("/proc/1/fd/" + entry.Name())
		if errors.Is(linkErr, os.ErrNotExist) {
			continue
		}
		if linkErr != nil {
			return "", nil, fmt.Errorf("read daemon descriptor target: %w", linkErr)
		}
		targets = append(targets, target)
	}
	return string(raw), targets, nil
}
