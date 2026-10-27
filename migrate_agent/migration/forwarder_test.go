package migration

import (
	"context"
	"net"
	"os"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/utmstack/MigrationTool/config"
	pb "github.com/utmstack/MigrationTool/grpc/pb"
)

// fakeCollectorServer implements the CollectorService surface the bridge calls.
type fakeCollectorServer struct {
	pb.UnimplementedCollectorServiceServer
	rows          []*pb.Collector
	accepted      string
	lastConfig    *pb.CollectorConfig
	lastState     *pb.IntegrationStateResponse
	stateSequence []*pb.IntegrationStateResponse
}

func (f *fakeCollectorServer) ListCollector(ctx context.Context, in *pb.ListRequest) (*pb.ListCollectorResponse, error) {
	return &pb.ListCollectorResponse{Rows: f.rows, Total: int32(len(f.rows))}, nil
}

func (f *fakeCollectorServer) SetCollectorConfig(ctx context.Context, in *pb.CollectorConfig) (*pb.ConfigKnowledge, error) {
	f.lastConfig = in
	return &pb.ConfigKnowledge{Accepted: f.accepted}, nil
}

func (f *fakeCollectorServer) GetCollectorIntegrationState(ctx context.Context, in *pb.IntegrationStateRequest) (*pb.IntegrationStateResponse, error) {
	if len(f.stateSequence) > 0 {
		next := f.stateSequence[0]
		f.stateSequence = f.stateSequence[1:]
		return next, nil
	}
	return f.lastState, nil
}

// dialForwarder spins up the collector service over bufconn and returns a
// panelClient bound to it.
func dialForwarder(t *testing.T, c *fakeCollectorServer) *panelClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	pb.RegisterCollectorServiceServer(srv, c)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &panelClient{
		conn:        conn,
		collectors:  pb.NewCollectorServiceClient(conn),
		internalKey: "the-internal-key",
	}
}

func TestLocalForwarderCollectorIDMatchByHostname(t *testing.T) {
	host, _ := os.Hostname()
	c := &fakeCollectorServer{rows: []*pb.Collector{
		{Id: 5, Module: pb.CollectorModule_FORWARDER, Hostname: host},
		{Id: 6, Module: pb.CollectorModule_UTMSTACK, Hostname: host}, // different module, ignored
	}}
	client := dialForwarder(t, c)

	id, err := client.LocalForwarderCollectorID(context.Background())
	if err != nil {
		t.Fatalf("LocalForwarderCollectorID: %v", err)
	}
	if id != 5 {
		t.Errorf("id = %d, want 5", id)
	}
}

func TestLocalForwarderCollectorIDAbsent(t *testing.T) {
	c := &fakeCollectorServer{rows: []*pb.Collector{
		{Id: 5, Module: pb.CollectorModule_FORWARDER, Hostname: "other-host"},
	}}
	client := dialForwarder(t, c)

	if _, err := client.LocalForwarderCollectorID(context.Background()); err == nil {
		t.Error("expected error when forwarder not registered, got nil")
	}
}

func TestSetIntegrationAccepts(t *testing.T) {
	c := &fakeCollectorServer{accepted: "true"}
	client := dialForwarder(t, c)

	err := client.SetIntegration(context.Background(), 9, "ciscoasa",
		map[string]string{"enabled": "true", "proto": "tcp", "port": "10514", "tls": "false"})
	if err != nil {
		t.Fatalf("SetIntegration: %v", err)
	}
	cfg := c.lastConfig
	if cfg == nil {
		t.Fatal("no config received")
	}
	if cfg.CollectorId != "9" {
		t.Errorf("CollectorId = %q, want 9", cfg.CollectorId)
	}
	if len(cfg.Groups) != 1 || cfg.Groups[0].GroupName != "ciscoasa" {
		t.Errorf("groups = %+v, want one ciscoasa group", cfg.Groups)
	}
	if len(cfg.Groups[0].Configurations) != 4 {
		t.Errorf("configs = %d, want 4", len(cfg.Groups[0].Configurations))
	}
}

func TestSetIntegrationRejected(t *testing.T) {
	c := &fakeCollectorServer{accepted: "false"}
	client := dialForwarder(t, c)

	if err := client.SetIntegration(context.Background(), 9, "ciscoasa",
		map[string]string{"enabled": "true"}); err == nil {
		t.Error("expected error when rejected, got nil")
	}
}

func TestIntegrationState(t *testing.T) {
	c := &fakeCollectorServer{lastState: &pb.IntegrationStateResponse{
		Configured: true, ConfigStatus: "accepted",
	}}
	client := dialForwarder(t, c)

	configured, status, lastErr, err := client.IntegrationState(context.Background(), 9, "ciscoasa")
	if err != nil {
		t.Fatalf("IntegrationState: %v", err)
	}
	if !configured || status != "accepted" || lastErr != "" {
		t.Errorf("got configured=%v status=%q lastErr=%q, want true accepted ''", configured, status, lastErr)
	}
}

func TestWaitForIntegrationAppliedPendingThenAcked(t *testing.T) {
	c := &fakeCollectorServer{stateSequence: []*pb.IntegrationStateResponse{
		{Configured: true, ConfigStatus: "Pending"},
		{Configured: true, ConfigStatus: "Sent"},
		{Configured: true, ConfigStatus: "Acked"},
	}}
	client := dialForwarder(t, c)
	m := &Migrator{state: &StateFile{}, creds: &config.Credentials{}}

	if err := m.waitForIntegrationApplied(context.Background(), client, 9, "ciscoasa"); err != nil {
		t.Fatalf("expected ack after pending/sent, got %v", err)
	}
}

func TestWaitForIntegrationAppliedFailed(t *testing.T) {
	c := &fakeCollectorServer{lastState: &pb.IntegrationStateResponse{
		Configured: true, ConfigStatus: "Failed", LastError: "TLS certificates not found",
	}}
	client := dialForwarder(t, c)
	m := &Migrator{state: &StateFile{}, creds: &config.Credentials{}}

	err := m.waitForIntegrationApplied(context.Background(), client, 9, "ciscoasa")
	if err == nil {
		t.Fatal("expected error for Failed state, got nil")
	}
	if !strings.Contains(err.Error(), "TLS certificates not found") {
		t.Errorf("error %q should carry the last_error", err)
	}
}

func TestWaitForIntegrationAppliedNotConfigured(t *testing.T) {
	c := &fakeCollectorServer{lastState: &pb.IntegrationStateResponse{Configured: false}}
	client := dialForwarder(t, c)
	m := &Migrator{state: &StateFile{}, creds: &config.Credentials{}}

	if err := m.waitForIntegrationApplied(context.Background(), client, 9, "ciscoasa"); err == nil {
		t.Fatal("expected error when row never appeared, got nil")
	}
}
