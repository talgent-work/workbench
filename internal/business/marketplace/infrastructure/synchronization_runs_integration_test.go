//go:build integration

package infrastructure_test

import (
	"context"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/go-jimu/components/taskqueue"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	marketplaceprivatev1 "github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1"
	"github.com/talgent-work/workbench/gen/go/talgent/marketplace/private/v1/marketplaceprivatev1connect"
	"github.com/talgent-work/workbench/internal/business/marketplace/application"
	catalogtask "github.com/talgent-work/workbench/internal/business/marketplace/application/task"
	"github.com/talgent-work/workbench/internal/business/marketplace/infrastructure"
	transport "github.com/talgent-work/workbench/internal/business/marketplace/transport/connectrpc"
	marketplacehttp "github.com/talgent-work/workbench/internal/business/marketplace/transport/http"
	"github.com/talgent-work/workbench/internal/business/marketplace/transport/taskprocessor"
	"github.com/talgent-work/workbench/internal/testsupport/middlewaretest"
	"github.com/talgent-work/workbench/pkg/authn"
	runtimequeue "github.com/talgent-work/workbench/pkg/taskqueue"
	"go.uber.org/fx/fxtest"
)

func TestSynchronizationAPIQueueAndPublication(t *testing.T) {
	db := catalogDatabase(t)
	store, err := infrastructure.NewCatalogStore(db, t.TempDir(), "talgent", "Talgent")
	require.NoError(t, err)
	root := t.TempDir()
	writeMetadata(t, root, "plugins/valid/.claude-plugin/plugin.json", `{"name":"actual","version":"1.0.0"}`)
	writeCatalog(t, root, "source", []map[string]any{{"name": "alias", "source": "./plugins/valid"}, {"name": "missing", "source": "./plugins/missing"}})
	archiveServer := newCatalogHTTPSServer(t, marketplacehttp.NewCatalogHandler(application.NewApplication(nil, nil, store, nil, nil, application.ConnectorRepositories{}), slog.Default()))
	defer archiveServer.Close()
	certificate := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: archiveServer.Certificate().Raw}), 0600))
	synchronizer, err := infrastructure.NewCatalogSynchronizer(infrastructure.CatalogAcquisitionConfig{Workers: 2, RequestTimeoutSeconds: 5, CertificateAuthority: certificate, ArchiveBaseURL: archiveServer.URL + "/marketplace/archives", Sources: map[string]infrastructure.CatalogSource{"source": {URI: root, Owned: true}}}, store)
	require.NoError(t, err)
	runs := infrastructure.NewSynchronizationRuns(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := runtimequeue.Config{RedisAddr: middlewaretest.Redis(t), WorkerConcurrency: 2, QueueName: "marketplace-maintenance"}
	producer := fxtest.NewLifecycle(t)
	enqueuer := runtimequeue.NewEnqueuer(producer, cfg, slog.Default())
	producer.RequireStart()
	defer producer.RequireStop()
	app := application.NewCatalogSynchronization(runs, synchronizer, enqueuer, time.Minute, true, logger)
	_, handler := marketplaceprivatev1connect.NewSynchronizationServiceHandler(transport.NewSynchronizationHandler(app, "fixture-platform-token"))
	server := httptest.NewServer(handler)
	defer server.Close()
	client := marketplaceprivatev1connect.NewSynchronizationServiceClient(server.Client(), server.URL)
	request := func(force bool) *connect.Request[marketplaceprivatev1.RequestSynchronizationRequest] {
		req := connect.NewRequest(&marketplaceprivatev1.RequestSynchronizationRequest{Force: force})
		req.Header().Set("Authorization", "Bearer fixture-platform-token")
		return req
	}
	_, err = client.RequestSynchronization(t.Context(), connect.NewRequest(&marketplaceprivatev1.RequestSynchronizationRequest{}))
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	native := request(false)
	native.Header().Set(authn.HeaderNativeWorkID, "work")
	_, err = client.RequestSynchronization(t.Context(), native)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	first, err := client.RequestSynchronization(t.Context(), request(false))
	require.NoError(t, err)
	require.Equal(t, "queued", first.Msg.Run.Status)
	var wg sync.WaitGroup
	results := make(chan *marketplaceprivatev1.SynchronizationRun, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			response, err := client.RequestSynchronization(t.Context(), request(true))
			if err != nil {
				failures <- err
				return
			}
			results <- response.Msg.Run
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	for run := range results {
		require.Equal(t, first.Msg.Run.Id, run.Id)
		require.False(t, run.Force)
	}
	worker := fxtest.NewLifecycle(t)
	_, err = runtimequeue.NewWorker(worker, cfg, logger, runtimequeue.ProcessorIn{Processors: []taskqueue.Processor{taskprocessor.NewSynchronizationProcessor(app)}})
	require.NoError(t, err)
	worker.RequireStart()
	defer worker.RequireStop()
	waitResult := func(id string) *marketplaceprivatev1.SynchronizationRun {
		var run *marketplaceprivatev1.SynchronizationRun
		require.Eventually(t, func() bool {
			req := connect.NewRequest(&marketplaceprivatev1.GetSynchronizationRequest{})
			req.Header().Set("Authorization", "Bearer fixture-platform-token")
			response, err := client.GetSynchronization(t.Context(), req)
			if err != nil {
				return false
			}
			run = response.Msg.Run
			return run != nil && run.Id == id && run.Status == "partial_failure"
		}, 10*time.Second, 20*time.Millisecond)
		return run
	}
	run := waitResult(first.Msg.Run.Id)
	require.Len(t, run.Failures, 1)
	require.Equal(t, "source", run.Failures[0].Source)
	require.Equal(t, "missing", run.Failures[0].Slug)
	require.Contains(t, run.Failures[0].Reason, "no such file", "the status says why the Plugin failed")
	release, err := store.Get(t.Context(), "actual")
	require.NoError(t, err)
	require.Equal(t, "1.0.0", release.Version)
	require.NoError(t, app.Execute(t.Context(), run.Id), "duplicate delivery is a no-op after completion")
	next, err := client.RequestSynchronization(t.Context(), request(true))
	require.NoError(t, err)
	require.NotEqual(t, run.Id, next.Msg.Run.Id)
	require.True(t, next.Msg.Run.Force)
	waitResult(next.Msg.Run.Id)
	// The periodic trigger uses the same admission and execution command.
	periodic, err := catalogtask.NewSynchronization("")
	require.NoError(t, err)
	require.NoError(t, taskprocessor.NewSynchronizationProcessor(app).Process(t.Context(), periodic))
	latest, err := runs.Latest(t.Context())
	require.NoError(t, err)
	require.NotEqual(t, next.Msg.Run.Id, latest.ID)
	waitResult(latest.ID)
	// A stopped process cannot reserve the platform slot forever. No sleep or
	// production mutation is needed to exercise the expired persisted reservation.
	stale := application.SynchronizationRun{ID: uuid.Must(uuid.NewV7()).String(), Status: "queued", RequestedAt: time.Now().Add(-2 * time.Minute).UnixMilli(), Deadline: time.Now().Add(-time.Minute).UnixMilli()}
	_, err = runs.Reserve(t.Context(), stale)
	require.NoError(t, err)
	expired, err := app.Latest(t.Context())
	require.NoError(t, err)
	require.Equal(t, "timed_out", expired.Status)
	fresh, err := app.Request(t.Context(), false)
	require.NoError(t, err)
	require.NotEqual(t, stale.ID, fresh.ID)
	require.NoError(t, app.Execute(context.Background(), stale.ID), "superseded task cannot publish")
	waitResult(fresh.ID)
}
