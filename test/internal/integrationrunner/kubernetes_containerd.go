package integrationrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"

	contentv1 "github.com/containerd/containerd/api/services/content/v1"
	imagesv1 "github.com/containerd/containerd/api/services/images/v1"
	introspectionv1 "github.com/containerd/containerd/api/services/introspection/v1"
	leasesv1 "github.com/containerd/containerd/api/services/leases/v1"
	namespacesv1 "github.com/containerd/containerd/api/services/namespaces/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dantte-lp/gobfd/test/internal/interopproject"
)

const (
	kubernetesContainerdNamespace = "k8s.io"
	containerdReceiptSchema       = 1
)

var (
	errKubernetesContainerdStore  = errors.New("kubernetes containerd image store is not admitted")
	errKubernetesImageAliasExists = errors.New("kubernetes containerd image alias already exists")
	errKubernetesContentMismatch  = errors.New("kubernetes containerd content does not match expected digest or size")
)

type kubernetesContainerdStore struct {
	connection *grpc.ClientConn
	images     imagesv1.ImagesClient
	content    contentv1.ContentClient
	leases     leasesv1.LeasesClient
	serverUUID string
}

func openKubernetesContainerdStore(ctx context.Context, endpoint string) (*kubernetesContainerdStore, error) {
	if validateKubernetesUnixEndpoint(endpoint) != nil {
		return nil, errKubernetesContainerdStore
	}
	connection, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("create containerd image-store connection: %w", err)
	}
	store := &kubernetesContainerdStore{
		connection: connection,
		images:     imagesv1.NewImagesClient(connection),
		content:    contentv1.NewContentClient(connection),
		leases:     leasesv1.NewLeasesClient(connection),
	}
	if identifyErr := store.identify(ctx); identifyErr != nil {
		closeErr := connection.Close()
		if closeErr != nil {
			return nil, errors.Join(identifyErr, fmt.Errorf("close rejected containerd connection: %w", closeErr))
		}
		return nil, identifyErr
	}
	return store, nil
}

func (store *kubernetesContainerdStore) identify(ctx context.Context) error {
	rpcCtx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	namespaced := metadata.AppendToOutgoingContext(rpcCtx, "containerd-namespace", kubernetesContainerdNamespace)
	server, err := introspectionv1.NewIntrospectionClient(store.connection).Server(namespaced, &emptypb.Empty{})
	if err != nil {
		return fmt.Errorf("query containerd server identity: %w", err)
	}
	if server.GetUUID() == "" || !kubernetesOpaqueIdentity(server.GetUUID()) {
		return errKubernetesContainerdStore
	}
	namespaceReq := &namespacesv1.GetNamespaceRequest{Name: kubernetesContainerdNamespace}
	namespace, nsErr := namespacesv1.NewNamespacesClient(store.connection).Get(namespaced, namespaceReq)
	if nsErr != nil {
		return fmt.Errorf("verify containerd Kubernetes namespace: %w", nsErr)
	}
	if namespace.GetNamespace() == nil || namespace.GetNamespace().GetName() != kubernetesContainerdNamespace {
		return errKubernetesContainerdStore
	}
	store.serverUUID = server.GetUUID()
	return nil
}

func (store *kubernetesContainerdStore) requireImagesAbsent(ctx context.Context, names []string) error {
	if store == nil || store.connection == nil || len(names) == 0 {
		return errKubernetesContainerdStore
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			return errKubernetesContainerdStore
		}
		if _, ok := seen[name]; ok {
			return errKubernetesContainerdStore
		}
		seen[name] = struct{}{}
	}
	for _, name := range slices.Sorted(maps.Keys(seen)) {
		rpcContext, cancel := context.WithTimeout(ctx, httpTimeout)
		rpcContext = metadata.AppendToOutgoingContext(
			rpcContext,
			"containerd-namespace",
			kubernetesContainerdNamespace,
		)
		image, err := store.images.Get(rpcContext, &imagesv1.GetImageRequest{Name: name})
		cancel()
		if status.Code(err) == codes.NotFound {
			continue
		}
		if err != nil {
			return fmt.Errorf("query existing containerd image %q: %w", name, err)
		}
		if image.GetImage() == nil || image.GetImage().GetName() != name {
			return fmt.Errorf("containerd returned a mismatched image for %q: %w", name, errKubernetesContainerdStore)
		}
		return fmt.Errorf("containerd image alias %q: %w", name, errKubernetesImageAliasExists)
	}
	return nil
}

func (store *kubernetesContainerdStore) close() error {
	if store == nil || store.connection == nil {
		return nil
	}
	if err := store.connection.Close(); err != nil {
		return fmt.Errorf("close containerd image-store connection: %w", err)
	}
	return nil
}

func recordKubernetesContainerdIdentity(directory, endpoint, serverUUID string) error {
	if validateKubernetesUnixEndpoint(endpoint) != nil || !kubernetesOpaqueIdentity(serverUUID) {
		return errKubernetesContainerdStore
	}
	data, err := json.Marshal(struct {
		Schema     int    `json:"schema"`
		Namespace  string `json:"namespace"`
		Endpoint   string `json:"image_service_endpoint"`
		ServerUUID string `json:"server_uuid"`
	}{containerdReceiptSchema, kubernetesContainerdNamespace, endpoint, serverUUID})
	if err != nil {
		return fmt.Errorf("encode containerd image-store receipt: %w", err)
	}
	receiptPath := filepath.Join(directory, "containerd.json")
	if writeErr := interopproject.CreatePrivateReceipt(receiptPath, data); writeErr != nil {
		return fmt.Errorf("write containerd image-store receipt: %w", writeErr)
	}
	return nil
}
