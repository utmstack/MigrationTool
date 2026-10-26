package migration

import (
	"context"
	"crypto/tls"
	"net"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	pb "github.com/utmstack/MigrationTool/grpc/pb"
)

// Panel is the agent-manager surface the bridge talks to over 443.
type Panel interface {
	ListAgentIDs(ctx context.Context) ([]uint32, error)
	GetConnectionKey(ctx context.Context) (string, error)
	Close() error
}

type panelClient struct {
	conn        *grpc.ClientConn
	agents      pb.AgentServiceClient
	panel       pb.PanelServiceClient
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
	if insecureTLS {
		creds = grpc.WithTransportCredentials(insecure.NewCredentials())
	} else {
		creds = grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{}))
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

func (c *panelClient) Close() error {
	return c.conn.Close()
}
