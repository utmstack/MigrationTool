package migration

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/utmstack/MigrationTool/grpc/pb"
)

const bufSize = 1024 * 1024

// fakeAgentServer implements the AgentService surface the bridge calls.
type fakeAgentServer struct {
	pb.UnimplementedAgentServiceServer
	rows      []*pb.Agent
	sawKey    string
	sawMethod string
}

func (f *fakeAgentServer) ListAgents(ctx context.Context, in *pb.ListRequest) (*pb.ListAgentsResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("internal-key"); len(v) > 0 {
		f.sawKey = v[0]
	}
	if v := md.Get("served-by"); len(v) > 0 {
		f.sawMethod = v[0]
	}
	return &pb.ListAgentsResponse{Rows: f.rows}, nil
}

// fakePanelServer implements the PanelService surface the bridge calls.
type fakePanelServer struct {
	pb.UnimplementedPanelServiceServer
	key    string
	sawKey string
}

func (f *fakePanelServer) GetConnectionKey(ctx context.Context, in *pb.ConnectionKeyRequest) (*pb.ConnectionKeyResponse, error) {
	if md, _ := metadata.FromIncomingContext(ctx); md != nil {
		if v := md.Get("internal-key"); len(v) > 0 {
			f.sawKey = v[0]
		}
	}
	return &pb.ConnectionKeyResponse{ConnectionKey: f.key}, nil
}

// dialBufconn spins up both services over an in-memory listener and returns a
// panelClient bound to them.
func dialBufconn(t *testing.T, agent *fakeAgentServer, panel *fakePanelServer) *panelClient {
	t.Helper()
	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	pb.RegisterAgentServiceServer(srv, agent)
	pb.RegisterPanelServiceServer(srv, panel)
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
		agents:      pb.NewAgentServiceClient(conn),
		panel:       pb.NewPanelServiceClient(conn),
		collectors:  pb.NewCollectorServiceClient(conn),
		internalKey: "the-internal-key",
	}
}

func TestListAgentIDs(t *testing.T) {
	agent := &fakeAgentServer{rows: []*pb.Agent{{Id: 7}, {Id: 42}}}
	client := dialBufconn(t, agent, &fakePanelServer{})

	ids, err := client.ListAgentIDs(context.Background())
	if err != nil {
		t.Fatalf("ListAgentIDs: %v", err)
	}
	if len(ids) != 2 || ids[0] != 7 || ids[1] != 42 {
		t.Errorf("ids = %v, want [7 42]", ids)
	}
	if agent.sawKey != "the-internal-key" {
		t.Errorf("internal-key metadata = %q, want the-internal-key", agent.sawKey)
	}
}

func TestGetConnectionKey(t *testing.T) {
	panel := &fakePanelServer{key: "conn-key-123"}
	client := dialBufconn(t, &fakeAgentServer{}, panel)

	got, err := client.GetConnectionKey(context.Background())
	if err != nil {
		t.Fatalf("GetConnectionKey: %v", err)
	}
	if got != "conn-key-123" {
		t.Errorf("key = %q, want conn-key-123", got)
	}
	if panel.sawKey != "the-internal-key" {
		t.Errorf("internal-key metadata = %q, want the-internal-key", panel.sawKey)
	}
}

func TestGetConnectionKeyEmpty(t *testing.T) {
	client := dialBufconn(t, &fakeAgentServer{}, &fakePanelServer{key: ""})

	if _, err := client.GetConnectionKey(context.Background()); err == nil {
		t.Error("expected error for empty connection key, got nil")
	}
}

func TestNewPanelRejectsEmpty(t *testing.T) {
	if _, err := NewPanel("", "key", false); err == nil {
		t.Error("expected error for empty server")
	}
	if _, err := NewPanel("host", "", false); err == nil {
		t.Error("expected error for empty internal key")
	}
}

func TestEnabledListenersTCPWinsAndTLS(t *testing.T) {
	cnf := &V11CollectorConfig{Integrations: map[string]V11Integration{
		"ciscoasa": {
			TCPPort: Port{Enabled: true, Value: "10514", TLSEnabled: false},
			UDPPort: Port{Enabled: true, Value: "10515"},
		},
		"panorama": {
			TCPPort: Port{Enabled: true, Value: "10516", TLSEnabled: true},
		},
	}}

	got := EnabledListeners(cnf)
	if len(got) != 2 {
		t.Fatalf("got %d listeners, want 2: %+v", len(got), got)
	}
	// Sorted by data type: ciscoasa first, panorama second.
	if got[0].DataType != "ciscoasa" || got[0].Proto != "tcp" || got[0].Port != "10514" || got[0].TLS {
		t.Errorf("ciscoasa = %+v, want tcp 10514 no-tls (tcp wins over udp)", got[0])
	}
	if got[1].DataType != "panorama" || got[1].Proto != "tls" || got[1].Port != "10516" || !got[1].TLS {
		t.Errorf("panorama = %+v, want tls 10516 tls", got[1])
	}
}

func TestEnabledListenersUDPOnly(t *testing.T) {
	cnf := &V11CollectorConfig{Integrations: map[string]V11Integration{
		"netflow": {UDPPort: Port{Enabled: true, Value: "10517"}},
	}}

	got := EnabledListeners(cnf)
	if len(got) != 1 || got[0].DataType != "netflow" || got[0].Proto != "udp" || got[0].Port != "10517" || got[0].TLS {
		t.Errorf("got %+v, want netflow udp 10517 no-tls", got)
	}
}
