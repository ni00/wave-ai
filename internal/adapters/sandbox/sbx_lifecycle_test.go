package sandbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	common "github.com/docker/sandboxes-api/gen/go/docker/sbx/common/v1"
	files "github.com/docker/sandboxes-api/gen/go/docker/sbx/files/v1"
	filesconnect "github.com/docker/sandboxes-api/gen/go/docker/sbx/files/v1/sbxfilesv1connect"
	v1 "github.com/docker/sandboxes-api/gen/go/docker/sbx/v1"
	rpc "github.com/docker/sandboxes-api/gen/go/docker/sbx/v1/sbxv1connect"
	"gorm.io/gorm"
)

func TestSbxRetainsResourcesAndConfirmsStop(t *testing.T) {
	db, prefix := capacityDB(t)
	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()
	var mu sync.Mutex
	status := common.SandboxStatus_SANDBOX_STATUS_STOPPED
	creates, starts := 0, 0
	stopFails := false
	var requested Resources
	mux.Handle(rpc.CapabilityServiceGetCapabilitiesProcedure, connect.NewUnaryHandler(rpc.CapabilityServiceGetCapabilitiesProcedure, func(context.Context, *connect.Request[v1.GetCapabilitiesRequest]) (*connect.Response[v1.Capabilities], error) {
		return connect.NewResponse(&v1.Capabilities{}), nil
	}))
	mux.Handle(rpc.SandboxServiceCreateSandboxProcedure, connect.NewUnaryHandler(rpc.SandboxServiceCreateSandboxProcedure, func(_ context.Context, r *connect.Request[v1.CreateSandboxRequest]) (*connect.Response[v1.Operation], error) {
		mu.Lock()
		defer mu.Unlock()
		creates++
		requested = Resources{r.Msg.Resources.GetCpus(), r.Msg.Resources.GetMemoryMib()}
		if r.Msg.Image != "original" || r.Msg.Agent != "shell" {
			t.Errorf("unexpected create spec: %v", r.Msg)
		}
		status = common.SandboxStatus_SANDBOX_STATUS_RUNNING
		return connect.NewResponse(&v1.Operation{Id: "create"}), nil
	}))
	mux.Handle(rpc.SandboxServiceGetSandboxProcedure, connect.NewUnaryHandler(rpc.SandboxServiceGetSandboxProcedure, func(context.Context, *connect.Request[v1.GetSandboxRequest]) (*connect.Response[v1.Sandbox], error) {
		mu.Lock()
		defer mu.Unlock()
		return connect.NewResponse(&v1.Sandbox{Core: &common.SandboxCore{Id: "retained-id", Status: status, Endpoint: &common.SandboxEndpoint{Uri: server.URL}}}), nil
	}))
	mux.Handle(rpc.SandboxServiceStartSandboxProcedure, connect.NewUnaryHandler(rpc.SandboxServiceStartSandboxProcedure, func(context.Context, *connect.Request[v1.StartSandboxRequest]) (*connect.Response[v1.Operation], error) {
		mu.Lock()
		defer mu.Unlock()
		starts++
		status = common.SandboxStatus_SANDBOX_STATUS_RUNNING
		return connect.NewResponse(&v1.Operation{Id: "start"}), nil
	}))
	mux.Handle(rpc.SandboxServiceStopSandboxProcedure, connect.NewUnaryHandler(rpc.SandboxServiceStopSandboxProcedure, func(context.Context, *connect.Request[v1.StopSandboxRequest]) (*connect.Response[v1.Operation], error) {
		mu.Lock()
		defer mu.Unlock()
		if stopFails {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("uncertain stop"))
		}
		status = common.SandboxStatus_SANDBOX_STATUS_STOPPED
		return connect.NewResponse(&v1.Operation{Id: "stop"}), nil
	}))
	mux.Handle(rpc.OperationServiceWaitOperationProcedure, connect.NewUnaryHandler(rpc.OperationServiceWaitOperationProcedure, func(context.Context, *connect.Request[v1.WaitOperationRequest]) (*connect.Response[v1.Operation], error) {
		return connect.NewResponse(&v1.Operation{Done: true}), nil
	}))
	mux.Handle(filesconnect.FileServiceMkdirProcedure, connect.NewUnaryHandler(filesconnect.FileServiceMkdirProcedure, func(context.Context, *connect.Request[files.MkdirRequest]) (*connect.Response[files.MkdirResponse], error) {
		return connect.NewResponse(&files.MkdirResponse{}), nil
	}))
	opts := SbxOptions{Store: db, BaseURL: server.URL, Image: "original", CPUs: 1, MemoryMiB: 1024, MaxRunning: 1, MemoryBudgetMiB: 4096}
	box := NewSbx(opts)
	// A profile reservation must reach the actual CreateSandbox request.
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix, Resources{2, 2048}) }); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if err := box.Ensure(context.Background(), prefix); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	mu.Lock()
	gotCreates, gotResources := creates, requested
	mu.Unlock()
	if gotCreates != 1 || gotResources != (Resources{2, 2048}) {
		t.Fatalf("creates=%d resources=%+v", gotCreates, gotResources)
	}
	mu.Lock()
	stopFails = true
	mu.Unlock()
	if err := box.Stop(context.Background(), prefix); err == nil {
		t.Fatal("uncertain stop accepted")
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return box.Reserve(tx, prefix+"other", Resources{1, 1024}) }); !errors.Is(err, ErrCapacity) {
		t.Fatalf("uncertain stop freed capacity: %v", err)
	}
	mu.Lock()
	stopFails = false
	mu.Unlock()
	if err := box.Stop(context.Background(), prefix); err != nil {
		t.Fatal(err)
	}
	// Restart a provider with different defaults; the retained VM is started,
	// never replaced or silently accounted at the new smaller size.
	opts.Image = "changed"
	box = NewSbx(opts)
	if err := box.Ensure(context.Background(), prefix); err != nil {
		t.Fatal(err)
	}
	var record Record
	db.Where("session_id = ?", prefix).Take(&record)
	if record.State != "running" || record.MemoryMiB != 2048 || record.Image != "original" {
		t.Fatalf("restart state/spec: %+v", record)
	}
	mu.Lock()
	gotCreates, gotStarts := creates, starts
	mu.Unlock()
	if gotCreates != 1 || gotStarts != 1 {
		t.Fatalf("create=%d start=%d", gotCreates, gotStarts)
	}
}
