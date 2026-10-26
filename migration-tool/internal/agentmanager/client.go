package agentmanager

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

const (
	AgentManagerPort = "9000"
	DefaultTimeout   = 30 * time.Second
	CommandTimeout   = 120 * time.Second
	MaxMessageSize   = 1024 * 1024 * 100 // 100MB
)

type Client struct {
	conn        *grpc.ClientConn
	agentClient AgentServiceClient
	panelClient PanelServiceClient
	internalKey string
}

func NewClient(host, internalKey string, skipTLSVerify bool) (*Client, error) {
	serverAddr := fmt.Sprintf("%s:%s", host, AgentManagerPort)

	tlsConfig := &tls.Config{
		InsecureSkipVerify: skipTLSVerify,
	}

	conn, err := grpc.NewClient(
		serverAddr,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxMessageSize)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to AgentManager: %w", err)
	}

	return &Client{
		conn:        conn,
		agentClient: NewAgentServiceClient(conn),
		panelClient: NewPanelServiceClient(conn),
		internalKey: internalKey,
	}, nil
}

func (c *Client) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func (c *Client) contextWithAuth(ctx context.Context) context.Context {
	md := metadata.New(map[string]string{
		"internal-key": c.internalKey,
	})
	return metadata.NewOutgoingContext(ctx, md)
}

func (c *Client) ListAgents(ctx context.Context) ([]*Agent, error) {
	ctx, cancel := context.WithTimeout(c.contextWithAuth(ctx), DefaultTimeout)
	defer cancel()

	resp, err := c.agentClient.ListAgents(ctx, &ListRequest{
		PageNumber: 1,
		PageSize:   10000,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	return resp.Rows, nil
}

func (c *Client) GetOnlineAgents(ctx context.Context) ([]*Agent, error) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	var online []*Agent
	for _, agent := range agents {
		if agent.Status == Status_ONLINE {
			online = append(online, agent)
		}
	}

	return online, nil
}

func (c *Client) GetOfflineAgents(ctx context.Context) ([]*Agent, error) {
	agents, err := c.ListAgents(ctx)
	if err != nil {
		return nil, err
	}

	var offline []*Agent
	for _, agent := range agents {
		if agent.Status == Status_OFFLINE {
			offline = append(offline, agent)
		}
	}

	return offline, nil
}

type CommandExecResult struct {
	AgentKey string
	Success  bool
	Output   string
	Error    error
	Duration time.Duration
}

func (c *Client) ExecuteCommand(ctx context.Context, agentId uint32, command, reason string) (*CommandExecResult, error) {
	ctx, cancel := context.WithTimeout(c.contextWithAuth(ctx), CommandTimeout)
	defer cancel()

	startTime := time.Now()

	stream, err := c.panelClient.ProcessCommand(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create command stream: %w", err)
	}

	// V11/V12 servers Atoi field 1 (agent_id); the local V10 proto names it agent_key.
	agentIdStr := strconv.FormatUint(uint64(agentId), 10)

	err = stream.Send(&UtmCommand{
		AgentKey:   agentIdStr,
		Command:    command,
		ExecutedBy: "migration-tool",
		CmdId:      "",
		OriginType: "migration",
		OriginId:   "upgrade-v12",
		Reason:     reason,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to send command: %w", err)
	}

	if err := stream.CloseSend(); err != nil {
		return nil, fmt.Errorf("failed to close send: %w", err)
	}

	result, err := stream.Recv()
	if err != nil {
		if err == io.EOF {
			return &CommandExecResult{
				AgentKey: agentIdStr,
				Success:  false,
				Error:    fmt.Errorf("stream closed without result"),
				Duration: time.Since(startTime),
			}, nil
		}
		return nil, fmt.Errorf("failed to receive result: %w", err)
	}

	execSuccess := true
	var execError error

	if result.Result == "" {
		execSuccess = false
		execError = fmt.Errorf("empty result received from agent")
	}

	if strings.Contains(strings.ToLower(result.Result), "error:") ||
		strings.Contains(strings.ToLower(result.Result), "failed:") ||
		strings.Contains(strings.ToLower(result.Result), "permission denied") {
		execSuccess = false
		execError = fmt.Errorf("command execution reported error: %s", result.Result)
	}

	return &CommandExecResult{
		AgentKey: result.AgentKey,
		Success:  execSuccess,
		Output:   result.Result,
		Error:    execError,
		Duration: time.Since(startTime),
	}, nil
}
