package integrationrunner

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	contentv1 "github.com/containerd/containerd/api/services/content/v1"
	imagesv1 "github.com/containerd/containerd/api/services/images/v1"
	introspectionv1 "github.com/containerd/containerd/api/services/introspection/v1"
	leasesv1 "github.com/containerd/containerd/api/services/leases/v1"
	namespacesv1 "github.com/containerd/containerd/api/services/namespaces/v1"
	containerdtypes "github.com/containerd/containerd/api/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

type containerdBlobRecord struct {
	data   []byte
	size   int64
	labels map[string]string
}

type kubernetesContainerdFixture struct {
	mu                  sync.Mutex
	images              map[string]*imagesv1.Image
	blobs               map[string]*containerdBlobRecord
	activeLeases        map[string]map[string]string
	createdLeases       []string
	deletedLeases       []string
	leasedContentCalls  int
	imageUpdateCalls    int
	imageNames          []string
	existingAll         bool
	missingSpace        bool
	imageGetStatus      codes.Code
	imageCreateRace     bool
	imageCreateCode     codes.Code
	contentWriteCode    codes.Code
	corruptCommitDigest bool
	leaseDeleteCode     codes.Code
	onImageCreate       func(alias string) error
	namespaceCalls      int
	serverCalls         int
}

type kubernetesContainerdIntrospectionService struct {
	introspectionv1.UnimplementedIntrospectionServer

	fixture *kubernetesContainerdFixture
}

type kubernetesContainerdNamespacesService struct {
	namespacesv1.UnimplementedNamespacesServer

	fixture *kubernetesContainerdFixture
}

type kubernetesContainerdImagesService struct {
	imagesv1.UnimplementedImagesServer

	fixture *kubernetesContainerdFixture
}

type kubernetesContainerdLeasesService struct {
	leasesv1.UnimplementedLeasesServer

	fixture *kubernetesContainerdFixture
}

type kubernetesContainerdContentService struct {
	contentv1.UnimplementedContentServer

	fixture *kubernetesContainerdFixture
}

func newKubernetesContainerdFixture() *kubernetesContainerdFixture {
	return &kubernetesContainerdFixture{
		images:       make(map[string]*imagesv1.Image),
		blobs:        make(map[string]*containerdBlobRecord),
		activeLeases: make(map[string]map[string]string),
	}
}

func startKubernetesContainerdFixture(t *testing.T, socket string, fixture *kubernetesContainerdFixture) {
	t.Helper()
	fixture.mu.Lock()
	if fixture.images == nil {
		fixture.images = make(map[string]*imagesv1.Image)
	}
	if fixture.blobs == nil {
		fixture.blobs = make(map[string]*containerdBlobRecord)
	}
	if fixture.activeLeases == nil {
		fixture.activeLeases = make(map[string]map[string]string)
	}
	fixture.mu.Unlock()
	listenCfg := net.ListenConfig{}
	listener, err := listenCfg.Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	introspectionv1.RegisterIntrospectionServer(
		server, &kubernetesContainerdIntrospectionService{fixture: fixture},
	)
	namespacesv1.RegisterNamespacesServer(server, &kubernetesContainerdNamespacesService{fixture: fixture})
	imagesv1.RegisterImagesServer(server, &kubernetesContainerdImagesService{fixture: fixture})
	leasesv1.RegisterLeasesServer(server, &kubernetesContainerdLeasesService{fixture: fixture})
	contentv1.RegisterContentServer(server, &kubernetesContainerdContentService{fixture: fixture})
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.Serve(listener) }()
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			t.Errorf("close containerd fixture listener: %v", closeErr)
		}
		server.Stop()
		if srvErr := <-serveErr; srvErr != nil &&
			!errors.Is(srvErr, net.ErrClosed) &&
			!errors.Is(srvErr, grpc.ErrServerStopped) {
			t.Errorf("serve containerd fixture: %v", srvErr)
		}
	})
}

func (fixture *kubernetesContainerdFixture) requireNamespace(ctx context.Context) error {
	values, ok := metadata.FromIncomingContext(ctx)
	ns := values.Get("containerd-namespace")
	if !ok || len(ns) != 1 || ns[0] != kubernetesContainerdNamespace {
		return status.Error(codes.InvalidArgument, "missing containerd namespace")
	}
	return nil
}

func (fixture *kubernetesContainerdFixture) requireActiveLease(ctx context.Context) error {
	if err := fixture.requireNamespace(ctx); err != nil {
		return err
	}
	values, ok := metadata.FromIncomingContext(ctx)
	leases := values.Get("containerd-lease")
	if !ok || len(leases) != 1 || leases[0] == "" {
		return status.Error(codes.InvalidArgument, "missing containerd lease")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if _, active := fixture.activeLeases[leases[0]]; !active {
		return status.Error(codes.FailedPrecondition, "containerd lease is not active")
	}
	fixture.leasedContentCalls++
	return nil
}

func (service kubernetesContainerdIntrospectionService) Server(
	ctx context.Context,
	_ *emptypb.Empty,
) (*introspectionv1.ServerResponse, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	fixture.serverCalls++
	fixture.mu.Unlock()
	return &introspectionv1.ServerResponse{UUID: "containerd-server-uuid"}, nil
}

func (service kubernetesContainerdNamespacesService) Get(
	ctx context.Context,
	request *namespacesv1.GetNamespaceRequest,
) (*namespacesv1.GetNamespaceResponse, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	fixture.namespaceCalls++
	missing := fixture.missingSpace
	fixture.mu.Unlock()
	if missing {
		return nil, status.Error(codes.NotFound, "namespace absent")
	}
	return &namespacesv1.GetNamespaceResponse{
		Namespace: &namespacesv1.Namespace{Name: request.GetName()},
	}, nil
}

func (service kubernetesContainerdLeasesService) Create(
	ctx context.Context,
	request *leasesv1.CreateRequest,
) (*leasesv1.CreateResponse, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	id := request.GetID()
	labels := maps.Clone(request.GetLabels())
	if id == "" || labels["containerd.io/gc.expire"] == "" {
		return nil, status.Error(codes.InvalidArgument, "missing lease id or gc.expire label")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if _, exists := fixture.activeLeases[id]; exists {
		return nil, status.Error(codes.AlreadyExists, "lease already exists")
	}
	fixture.activeLeases[id] = labels
	fixture.createdLeases = append(fixture.createdLeases, id)
	return &leasesv1.CreateResponse{Lease: &leasesv1.Lease{ID: id, Labels: labels}}, nil
}

func (service kubernetesContainerdLeasesService) Delete(
	ctx context.Context,
	request *leasesv1.DeleteRequest,
) (*emptypb.Empty, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	code := fixture.leaseDeleteCode
	delete(fixture.activeLeases, request.GetID())
	fixture.deletedLeases = append(fixture.deletedLeases, request.GetID())
	fixture.mu.Unlock()
	if code != codes.OK {
		return nil, status.Error(code, "injected lease delete failure")
	}
	return &emptypb.Empty{}, nil
}

func (service kubernetesContainerdContentService) Info(
	ctx context.Context,
	request *contentv1.InfoRequest,
) (*contentv1.InfoResponse, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	blob, exists := fixture.blobs[request.GetDigest()]
	var info *contentv1.Info
	if exists {
		info = &contentv1.Info{
			Digest: request.GetDigest(),
			Size:   blob.size,
			Labels: maps.Clone(blob.labels),
		}
	}
	fixture.mu.Unlock()
	if !exists {
		return nil, status.Error(codes.NotFound, "blob absent")
	}
	return &contentv1.InfoResponse{Info: info}, nil
}

func (service kubernetesContainerdContentService) Update(
	ctx context.Context,
	request *contentv1.UpdateRequest,
) (*contentv1.UpdateResponse, error) {
	fixture := service.fixture
	if err := fixture.requireActiveLease(ctx); err != nil {
		return nil, err
	}
	digest := request.GetInfo().GetDigest()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	blob, exists := fixture.blobs[digest]
	if !exists {
		return nil, status.Error(codes.NotFound, "blob absent")
	}
	if blob.labels == nil {
		blob.labels = make(map[string]string)
	}
	for _, maskPath := range request.GetUpdateMask().GetPaths() {
		key, ok := strings.CutPrefix(maskPath, "labels.")
		if !ok || key == "" {
			return nil, status.Error(codes.InvalidArgument, "unsupported update mask path")
		}
		blob.labels[key] = request.GetInfo().GetLabels()[key]
	}
	return &contentv1.UpdateResponse{
		Info: &contentv1.Info{
			Digest: digest,
			Size:   blob.size,
			Labels: maps.Clone(blob.labels),
		},
	}, nil
}

func (service kubernetesContainerdContentService) Write(stream contentv1.Content_WriteServer) error {
	fixture := service.fixture
	if err := fixture.requireActiveLease(stream.Context()); err != nil {
		return err
	}
	var payload []byte
	ref := ""
	for {
		req, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return nil
		}
		if recvErr != nil {
			return fmt.Errorf("receive content write frame: %w", recvErr)
		}
		if stepErr := fixture.handleContentWriteStep(stream, req, &ref, &payload); stepErr != nil {
			return stepErr
		}
	}
}

func (fixture *kubernetesContainerdFixture) handleContentWriteStep(
	stream contentv1.Content_WriteServer,
	req *contentv1.WriteContentRequest,
	ref *string,
	payload *[]byte,
) error {
	if *ref == "" {
		*ref = req.GetRef()
		if *ref == "" {
			return status.Error(codes.InvalidArgument, "initial content write requires ref")
		}
	}
	fixture.mu.Lock()
	injectedCode := fixture.contentWriteCode
	_, alreadyExists := fixture.blobs[req.GetExpected()]
	corruptDigest := fixture.corruptCommitDigest
	fixture.mu.Unlock()
	if injectedCode != codes.OK {
		return status.Error(injectedCode, "injected content write failure")
	}
	if req.GetExpected() != "" && alreadyExists {
		return status.Error(codes.AlreadyExists, "blob already exists")
	}
	if req.GetOffset() != int64(len(*payload)) {
		return status.Error(codes.OutOfRange, "unexpected content write offset")
	}
	*payload = append(*payload, req.GetData()...)
	if req.GetAction() == contentv1.WriteAction_WRITE {
		return stream.Send(&contentv1.WriteContentResponse{
			Action: contentv1.WriteAction_WRITE,
			Offset: int64(len(*payload)),
		})
	}
	if req.GetAction() != contentv1.WriteAction_COMMIT {
		return status.Error(codes.InvalidArgument, "unsupported content write action")
	}
	computed := fmt.Sprintf("sha256:%x", sha256.Sum256(*payload))
	if req.GetTotal() > 0 && int64(len(*payload)) != req.GetTotal() {
		return status.Error(codes.FailedPrecondition, "content size mismatch")
	}
	if req.GetExpected() != "" && computed != req.GetExpected() {
		return status.Error(codes.FailedPrecondition, "content digest mismatch")
	}
	fixture.mu.Lock()
	fixture.blobs[computed] = &containerdBlobRecord{
		data:   append([]byte(nil), (*payload)...),
		size:   int64(len(*payload)),
		labels: maps.Clone(req.GetLabels()),
	}
	fixture.mu.Unlock()
	respDigest := computed
	if corruptDigest {
		respDigest = "sha256:" + strings.Repeat("0", 64)
	}
	return stream.Send(&contentv1.WriteContentResponse{
		Action: contentv1.WriteAction_COMMIT,
		Offset: int64(len(*payload)),
		Total:  int64(len(*payload)),
		Digest: respDigest,
	})
}

func (service kubernetesContainerdImagesService) Get(
	ctx context.Context,
	request *imagesv1.GetImageRequest,
) (*imagesv1.GetImageResponse, error) {
	fixture := service.fixture
	if err := fixture.requireNamespace(ctx); err != nil {
		return nil, err
	}
	fixture.mu.Lock()
	fixture.imageNames = append(fixture.imageNames, request.GetName())
	image, exists := fixture.images[request.GetName()]
	if fixture.existingAll {
		image, exists = &imagesv1.Image{Name: request.GetName()}, true
	}
	code := fixture.imageGetStatus
	fixture.mu.Unlock()
	if code != codes.OK {
		return nil, status.Error(code, "injected image query failure")
	}
	if !exists {
		return nil, status.Error(codes.NotFound, "image absent")
	}
	return &imagesv1.GetImageResponse{Image: image}, nil
}

func (service kubernetesContainerdImagesService) Create(
	ctx context.Context,
	request *imagesv1.CreateImageRequest,
) (*imagesv1.CreateImageResponse, error) {
	fixture := service.fixture
	if err := fixture.requireActiveLease(ctx); err != nil {
		return nil, err
	}
	incoming := request.GetImage()
	if incoming == nil || incoming.GetName() == "" || incoming.GetTarget() == nil {
		return nil, status.Error(codes.InvalidArgument, "incomplete image create request")
	}
	fixture.mu.Lock()
	hook := fixture.onImageCreate
	race := fixture.imageCreateRace
	createCode := fixture.imageCreateCode
	if race {
		fixture.images[incoming.GetName()] = &imagesv1.Image{
			Name: incoming.GetName(),
			Target: &containerdtypes.Descriptor{
				MediaType: ociManifestMediaType,
				Digest:    "sha256:" + strings.Repeat("e", 64),
				Size:      123,
			},
		}
	}
	_, exists := fixture.images[incoming.GetName()]
	if !exists && createCode == codes.OK {
		stored := &imagesv1.Image{
			Name: incoming.GetName(),
			Target: &containerdtypes.Descriptor{
				MediaType: incoming.GetTarget().GetMediaType(),
				Digest:    incoming.GetTarget().GetDigest(),
				Size:      incoming.GetTarget().GetSize(),
			},
			Labels: maps.Clone(incoming.GetLabels()),
		}
		fixture.images[incoming.GetName()] = stored
	}
	fixture.mu.Unlock()
	if hook != nil {
		if hookErr := hook(incoming.GetName()); hookErr != nil {
			return nil, hookErr
		}
	}
	if createCode != codes.OK {
		return nil, status.Error(createCode, "injected image create failure")
	}
	if exists {
		return nil, status.Error(codes.AlreadyExists, "image alias already exists")
	}
	return &imagesv1.CreateImageResponse{Image: incoming}, nil
}

func (service kubernetesContainerdImagesService) Update(
	_ context.Context,
	_ *imagesv1.UpdateImageRequest,
) (*imagesv1.UpdateImageResponse, error) {
	service.fixture.mu.Lock()
	service.fixture.imageUpdateCalls++
	service.fixture.mu.Unlock()
	return nil, status.Error(codes.PermissionDenied, "mutable Images.Update is forbidden")
}

func TestKubernetesContainerdStoreRequiresAbsentExactAliases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		images      map[string]*imagesv1.Image
		existingAll bool
		queryCode   codes.Code
		wantCode    codes.Code
		wantAbsent  bool
		wantErr     error
	}{
		{name: "not found means absent", wantAbsent: true},
		{
			name:    "existing alias rejected",
			images:  map[string]*imagesv1.Image{"localhost/gobfd:a": {Name: "localhost/gobfd:a"}},
			wantErr: errKubernetesImageAliasExists,
		},
		{
			name:    "mismatched response rejected",
			images:  map[string]*imagesv1.Image{"localhost/gobfd:a": {Name: "foreign"}},
			wantErr: errKubernetesContainerdStore,
		},
		{name: "query errors fail closed", queryCode: codes.Unavailable, wantCode: codes.Unavailable},
		{name: "duplicate aliases rejected before lookup", wantErr: errKubernetesContainerdStore},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			socket := filepath.Join(t.TempDir(), "containerd.sock")
			fixture := newKubernetesContainerdFixture()
			if test.images != nil {
				fixture.images = test.images
			}
			fixture.existingAll = test.existingAll
			fixture.imageGetStatus = test.queryCode
			startKubernetesContainerdFixture(t, socket, fixture)
			store, openErr := openKubernetesContainerdStore(t.Context(), "unix://"+socket)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer func() {
				if closeErr := store.close(); closeErr != nil {
					t.Errorf("close containerd connection: %v", closeErr)
				}
			}()
			if store.serverUUID != "containerd-server-uuid" {
				t.Fatalf("server identity=%q", store.serverUUID)
			}
			aliases := []string{"localhost/gobfd:b", "localhost/gobfd:a"}
			if test.name == "duplicate aliases rejected before lookup" {
				aliases = []string{"localhost/gobfd:a", "localhost/gobfd:a"}
			}
			checkErr := store.requireImagesAbsent(t.Context(), aliases)
			assertAbsentAliasesOutcome(t, checkErr, test.wantErr, test.wantCode, test.wantAbsent)
			assertAbsentAliasesCalls(t, fixture, test.name, test.wantErr != nil || test.wantCode != codes.OK)
		})
	}
}

func assertAbsentAliasesOutcome(
	t *testing.T,
	err, wantErr error,
	wantCode codes.Code,
	wantAbsent bool,
) {
	t.Helper()
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("expected %v, got %v", wantErr, err)
	}
	if wantCode != codes.OK && status.Code(err) != wantCode {
		t.Fatalf("error code=%s, want %s: %v", status.Code(err), wantCode, err)
	}
	if wantAbsent && err != nil {
		t.Fatalf("absent aliases rejected: %v", err)
	}
}

func assertAbsentAliasesCalls(t *testing.T, fixture *kubernetesContainerdFixture, name string, failed bool) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.serverCalls != 1 || fixture.namespaceCalls != 1 {
		t.Fatalf("identity calls: server=%d namespace=%d", fixture.serverCalls, fixture.namespaceCalls)
	}
	wantCalls := 2
	if failed {
		wantCalls = 1
	}
	if name == "duplicate aliases rejected before lookup" {
		wantCalls = 0
	}
	if len(fixture.imageNames) != wantCalls {
		t.Fatalf("image lookup order=%v", fixture.imageNames)
	}
	if wantCalls > 0 && fixture.imageNames[0] != "localhost/gobfd:a" {
		t.Fatalf("image lookup order=%v", fixture.imageNames)
	}
	if wantCalls == 2 && fixture.imageNames[1] != "localhost/gobfd:b" {
		t.Fatalf("image lookup order=%v", fixture.imageNames)
	}
}

func TestKubernetesContainerdStoreRejectsMissingNamespace(t *testing.T) {
	t.Parallel()
	socket := filepath.Join(t.TempDir(), "containerd.sock")
	fixture := newKubernetesContainerdFixture()
	fixture.missingSpace = true
	startKubernetesContainerdFixture(t, socket, fixture)
	if store, err := openKubernetesContainerdStore(t.Context(), "unix://"+socket); err == nil {
		if closeErr := store.close(); closeErr != nil {
			t.Errorf("close containerd connection: %v", closeErr)
		}
		t.Fatal("missing k8s.io namespace accepted")
	}
}

func TestKubernetesContainerdDirectImport(t *testing.T) {
	t.Parallel()
	const alias = "localhost/gobfd-k8s-direct:owned"
	archiveBytes, expectedID, fixtureErr := kubernetesArchiveTestBytes(alias, map[string]string{"owner": "test"})
	if fixtureErr != nil {
		t.Fatal(fixtureErr)
	}
	cases := []struct {
		name    string
		prepare func(*kubernetesContainerdFixture, verifiedKubernetesArchive)
		wantErr error
	}{
		{name: "healthy create-only import"},
		{
			name: "create-only race rejects alias without overwrite",
			prepare: func(f *kubernetesContainerdFixture, _ verifiedKubernetesArchive) {
				f.imageCreateRace = true
			},
			wantErr: errKubernetesImageAliasExists,
		},
		{
			name: "corrupt commit digest rejected",
			prepare: func(f *kubernetesContainerdFixture, _ verifiedKubernetesArchive) {
				f.corruptCommitDigest = true
			},
			wantErr: errKubernetesContentMismatch,
		},
		{
			name: "existing blob with matching size accepted and manifest GC rooted",
			prepare: func(f *kubernetesContainerdFixture, archive verifiedKubernetesArchive) {
				f.blobs[archive.Layers[0].Digest] = &containerdBlobRecord{
					size:   archive.Layers[0].Size,
					labels: map[string]string{},
				}
				_, manifestDigest, _, err := buildKubernetesOCIManifest(archive)
				if err == nil {
					manifestBytes, _, _, _ := buildKubernetesOCIManifest(archive)
					f.blobs[manifestDigest] = &containerdBlobRecord{
						data:   manifestBytes,
						size:   int64(len(manifestBytes)),
						labels: map[string]string{},
					}
				}
			},
		},
		{
			name: "existing blob with mismatched size rejected",
			prepare: func(f *kubernetesContainerdFixture, archive verifiedKubernetesArchive) {
				f.blobs[archive.Config.Digest] = &containerdBlobRecord{
					size:   archive.Config.Size + 9,
					labels: map[string]string{},
				}
			},
			wantErr: errKubernetesContentMismatch,
		},
		{
			name: "lease deletion failure reported",
			prepare: func(f *kubernetesContainerdFixture, _ verifiedKubernetesArchive) {
				f.leaseDeleteCode = codes.Internal
			},
			wantErr: status.Error(codes.Internal, "injected lease delete failure"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			runDirectImportSubtest(t, alias, expectedID, archiveBytes, test.prepare, test.wantErr)
		})
	}
}

func runDirectImportSubtest(
	t *testing.T,
	alias, expectedID string,
	archiveBytes []byte,
	prepare func(*kubernetesContainerdFixture, verifiedKubernetesArchive),
	wantErr error,
) {
	t.Helper()
	archiveFile := writeTempVerifiedArchiveFile(t, archiveBytes)
	verified, verifyErr := inspectKubernetesArchive(t.Context(), archiveFile, alias, expectedID)
	if verifyErr != nil {
		t.Fatal(verifyErr)
	}
	socket := filepath.Join(t.TempDir(), "containerd.sock")
	fixture := newKubernetesContainerdFixture()
	if prepare != nil {
		prepare(fixture, verified)
	}
	startKubernetesContainerdFixture(t, socket, fixture)
	store, openErr := openKubernetesContainerdStore(t.Context(), "unix://"+socket)
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() {
		if closeErr := store.close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})
	manifestDigest, importErr := store.importArchive(t.Context(), archiveFile, alias, verified)
	assertDirectImportResult(t, fixture, alias, verified, manifestDigest, importErr, wantErr)
}

func writeTempVerifiedArchiveFile(t *testing.T, archiveBytes []byte) *os.File {
	t.Helper()
	file, err := os.Create(filepath.Join(t.TempDir(), "archive.tar"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close archive fixture: %v", closeErr)
		}
	})
	if _, writeErr := file.Write(archiveBytes); writeErr != nil {
		t.Fatal(writeErr)
	}
	return file
}

func assertDirectImportResult(
	t *testing.T,
	fixture *kubernetesContainerdFixture,
	alias string,
	verified verifiedKubernetesArchive,
	manifestDigest string,
	importErr, wantErr error,
) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.imageUpdateCalls != 0 {
		t.Fatalf("Images.Update called %d times, want 0", fixture.imageUpdateCalls)
	}
	if len(fixture.createdLeases) != 1 || len(fixture.deletedLeases) != 1 ||
		fixture.createdLeases[0] != fixture.deletedLeases[0] {
		t.Fatalf("lease lifecycle mismatch: created=%v deleted=%v", fixture.createdLeases, fixture.deletedLeases)
	}
	if fixture.leasedContentCalls == 0 {
		t.Fatal("expected leased content/image RPCs")
	}
	if wantErr != nil {
		if importErr == nil {
			t.Fatalf("expected error %v, got nil", wantErr)
		}
		if status.Code(wantErr) != codes.OK {
			if status.Code(importErr) != status.Code(wantErr) {
				t.Fatalf("status=%s, want %s: %v", status.Code(importErr), status.Code(wantErr), importErr)
			}
		} else if !errors.Is(importErr, wantErr) {
			t.Fatalf("error=%v, want %v", importErr, wantErr)
		}
		if errors.Is(wantErr, errKubernetesImageAliasExists) {
			got := fixture.images[alias].GetTarget().GetDigest()
			if got != "sha256:"+strings.Repeat("e", 64) {
				t.Fatalf("create-only race overwrote existing alias digest: %s", got)
			}
		}
		return
	}
	if importErr != nil {
		t.Fatalf("unexpected import error: %v", importErr)
	}
	manifestBlob, ok := fixture.blobs[manifestDigest]
	if !ok {
		t.Fatalf("manifest blob %s missing from content store", manifestDigest)
	}
	if manifestBlob.labels["containerd.io/gc.ref.content.config"] != verified.Config.Digest ||
		manifestBlob.labels["containerd.io/gc.ref.content.0"] != verified.Layers[0].Digest {
		t.Fatalf("manifest GC root labels=%v", manifestBlob.labels)
	}
	if fixture.images[alias].GetTarget().GetDigest() != manifestDigest {
		t.Fatalf("published target digest=%s, want %s", fixture.images[alias].GetTarget().GetDigest(), manifestDigest)
	}
}
