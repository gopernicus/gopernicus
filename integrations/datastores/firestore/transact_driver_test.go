package firestore

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"

	gcfs "cloud.google.com/go/firestore"
	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/gopernicus/gopernicus/sdk"
)

type transactionServer struct {
	firestorepb.UnimplementedFirestoreServer
	begins     atomic.Int32
	beginError error
}

func (s *transactionServer) BeginTransaction(context.Context, *firestorepb.BeginTransactionRequest) (*firestorepb.BeginTransactionResponse, error) {
	if s.begins.Add(1) > 1 && s.beginError != nil {
		return nil, s.beginError
	}
	return &firestorepb.BeginTransactionResponse{Transaction: []byte("test-transaction")}, nil
}

func (*transactionServer) Rollback(context.Context, *firestorepb.RollbackRequest) (*emptypb.Empty, error) {
	return &emptypb.Empty{}, nil
}

func (*transactionServer) Commit(context.Context, *firestorepb.CommitRequest) (*firestorepb.CommitResponse, error) {
	return &firestorepb.CommitResponse{}, nil
}

func transactionDriverDB(t *testing.T, server *transactionServer) *DB {
	t.Helper()
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	firestorepb.RegisterFirestoreServer(grpcServer, server)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_ = grpcServer.Serve(listener)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		_ = listener.Close()
		<-stopped
	})
	conn, err := grpc.NewClient("passthrough:///fixture",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	client, err := gcfs.NewClient(context.Background(), "fixture", option.WithGRPCConn(conn))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return &DB{client: client, maxAttempts: 2}
}

func TestTransactReturnsTerminalDriverFailure(t *testing.T) {
	for _, scenario := range []string{"next begin denied", "retry canceled", "contention exhausted", "retry succeeds"} {
		t.Run(scenario, func(t *testing.T) {
			server := &transactionServer{}
			if scenario == "next begin denied" {
				server.beginError = status.Error(codes.PermissionDenied, "next begin denied")
			}
			db := transactionDriverDB(t, server)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			err := db.Transact(ctx, func(context.Context) error {
				calls++
				if scenario == "retry canceled" {
					cancel()
				}
				if scenario == "retry succeeds" && calls == 2 {
					return nil
				}
				return MapError(status.Error(codes.Aborted, "read contention"))
			})
			switch scenario {
			case "next begin denied":
				if calls != 1 || server.begins.Load() != 2 || !errors.Is(err, sdk.ErrForbidden) || status.Code(err) != codes.PermissionDenied {
					t.Fatalf("calls=%d begins=%d err=%v; want terminal begin refusal", calls, server.begins.Load(), err)
				}
			case "retry canceled":
				if calls != 1 || (status.Code(err) != codes.Canceled && !errors.Is(err, context.Canceled)) || errors.Is(err, sdk.ErrConflict) {
					t.Fatalf("calls=%d err=%v; want cancellation", calls, err)
				}
			case "contention exhausted":
				if calls != 2 || !errors.Is(err, sdk.ErrConflict) || status.Code(err) != codes.Aborted {
					t.Fatalf("calls=%d err=%v; want exhausted contention", calls, err)
				}
			case "retry succeeds":
				if calls != 2 || err != nil {
					t.Fatalf("calls=%d err=%v; want committed second attempt", calls, err)
				}
			}
		})
	}
}

type sliceFailure []int

func (sliceFailure) Error() string { return "non-comparable domain failure" }

func TestTransactPreservesArbitraryCallbackErrors(t *testing.T) {
	db := transactionDriverDB(t, &transactionServer{})
	plain := errors.New("domain refusal")
	if got := db.Transact(context.Background(), func(context.Context) error { return plain }); got != plain {
		t.Fatalf("callback identity changed: %v", got)
	}
	original := sliceFailure{1, 2}
	got := db.Transact(context.Background(), func(context.Context) error { return original })
	returned, ok := got.(sliceFailure)
	if !ok || len(returned) != len(original) || &returned[0] != &original[0] {
		t.Fatalf("non-comparable callback identity changed: %#v", got)
	}
}
