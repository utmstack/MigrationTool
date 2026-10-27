package migration

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"

	pb "github.com/utmstack/MigrationTool/grpc/pb"
)

// Panel is the agent-manager surface the bridge talks to over 443.
type Panel interface {
	ListAgentIDs(ctx context.Context) ([]uint32, error)
	GetConnectionKey(ctx context.Context) (string, error)
	LocalForwarderCollectorID(ctx context.Context) (uint32, error)
	SetIntegration(ctx context.Context, collectorID uint32, dataType string, kv map[string]string) error
	IntegrationState(ctx context.Context, collectorID uint32, dataType string) (configured bool, status string, lastError string, err error)
	Close() error
}

type panelClient struct {
	conn        *grpc.ClientConn
	agents      pb.AgentServiceClient
	panel       pb.PanelServiceClient
	collectors  pb.CollectorServiceClient
	internalKey string
}

// NewPanel dials the V12 panel. internalKey authenticates against
// InternalKeyRoutes (ListAgents, GetConnectionKey).
func NewPanel(server, internalKey string, insecureTLS bool) (Panel, error) {
	if server == "" {
		return nil, fmt.Errorf("panel server is empty")
	}
	if internalKey == "" {
		return nil, fmt.Errorf("internal key is empty")
	}

	var creds grpc.DialOption
	// Both modes need h2 in ALPN: the panel serves gRPC behind nginx over 443.
	// insecure.NewCredentials() omits NextProtos, so the TLS handshake would
	// negotiate http/1.1 and gRPC would fail reading the server preface.
	if insecureTLS {
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"h2"},
		}))
	} else {
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			NextProtos: []string{"h2"},
		}))
	}

	target := server
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}

	conn, err := grpc.NewClient(target, creds)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", server, err)
	}

	return &panelClient{
		conn:        conn,
		agents:      pb.NewAgentServiceClient(conn),
		panel:       pb.NewPanelServiceClient(conn),
		collectors:  pb.NewCollectorServiceClient(conn),
		internalKey: internalKey,
	}, nil
}

// rpcContext binds the internal-key metadata and a per-call timeout. The
// cancel runs after the RPC completes, not when this helper returns.
func (c *panelClient) rpcContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	return metadata.AppendToOutgoingContext(ctx, "internal-key", c.internalKey), cancel
}

func (c *panelClient) ListAgentIDs(ctx context.Context) ([]uint32, error) {
	rctx, cancel := c.rpcContext(ctx)
	defer cancel()

	resp, err := c.agents.ListAgents(rctx, &pb.ListRequest{PageSize: 1000})
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0, len(resp.GetRows()))
	for _, a := range resp.GetRows() {
		ids = append(ids, a.GetId())
	}
	return ids, nil
}

func (c *panelClient) GetConnectionKey(ctx context.Context) (string, error) {
	rctx, cancel := c.rpcContext(ctx)
	defer cancel()

	resp, err := c.panel.GetConnectionKey(rctx, &pb.ConnectionKeyRequest{})
	if err != nil {
		return "", err
	}
	if resp.GetConnectionKey() == "" {
		return "", fmt.Errorf("empty connection key from panel")
	}
	return resp.GetConnectionKey(), nil
}

// localForwarderID matches this host's forwarder row by module, then by
// hostname or local IPv4 (the forwarder registers both).
func (c *panelClient) localForwarderID(ctx context.Context) (uint32, error) {
	hostname, _ := os.Hostname()
	ip := localIPv4()

	resp, err := c.listCollectors(ctx)
	if err != nil {
		return 0, err
	}
	for _, col := range resp.GetRows() {
		if col.GetModule() != pb.CollectorModule_FORWARDER {
			continue
		}
		if col.GetHostname() != "" && hostname != "" && col.GetHostname() == hostname {
			return uint32(col.GetId()), nil
		}
		if ip != "" && col.GetIp() == ip {
			return uint32(col.GetId()), nil
		}
	}
	return 0, fmt.Errorf("forwarder for this host not registered yet")
}

func (c *panelClient) listCollectors(ctx context.Context) (*pb.ListCollectorResponse, error) {
	rctx, cancel := c.rpcContext(ctx)
	defer cancel()

	return c.collectors.ListCollector(rctx, &pb.ListRequest{PageSize: 1000})
}

func (c *panelClient) LocalForwarderCollectorID(ctx context.Context) (uint32, error) {
	return c.localForwarderID(ctx)
}

func (c *panelClient) SetIntegration(ctx context.Context, collectorID uint32, dataType string, kv map[string]string) error {
	rctx, cancel := c.rpcContext(ctx)
	defer cancel()

	reqID := uuid.NewString()
	confs := make([]*pb.CollectorGroupConfigurations, 0, len(kv))
	for k, v := range kv {
		confs = append(confs, &pb.CollectorGroupConfigurations{ConfKey: k, ConfValue: v})
	}

	resp, err := c.collectors.SetCollectorConfig(rctx, &pb.CollectorConfig{
		CollectorId: strconv.FormatUint(uint64(collectorID), 10),
		RequestId:   reqID,
		Groups: []*pb.CollectorConfigGroup{{
			GroupName:      dataType,
			RequestId:      reqID,
			Configurations: confs,
		}},
	})
	if err != nil {
		return err
	}
	if resp.GetAccepted() == "false" {
		return fmt.Errorf("integration %s rejected: %s", dataType, resp.GetErrorMessage())
	}
	return nil
}

func (c *panelClient) IntegrationState(ctx context.Context, collectorID uint32, dataType string) (bool, string, string, error) {
	rctx, cancel := c.rpcContext(ctx)
	defer cancel()

	resp, err := c.collectors.GetCollectorIntegrationState(rctx, &pb.IntegrationStateRequest{
		CollectorId: int32(collectorID),
		DataType:    dataType,
	})
	if err != nil {
		return false, "", "", err
	}
	return resp.GetConfigured(), resp.GetConfigStatus(), resp.GetLastError(), nil
}

func (c *panelClient) Close() error {
	return c.conn.Close()
}

// localIPv4 mirrors the forwarder's registration address (first non-loopback
// IPv4) so the bridge can match its own collector row.
func localIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if v4 := ipNet.IP.To4(); v4 != nil {
			return v4.String()
		}
	}
	return ""
}
