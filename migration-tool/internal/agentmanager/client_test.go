package agentmanager

import (
	"context"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// fakePanelServer records the UtmCommand it receives and replies with a
// pre-configured CommandResult.
type fakePanelServer struct {
	UnimplementedPanelServiceServer
	received *UtmCommand
	reply    *CommandResult
}

func (f *fakePanelServer) ProcessCommand(stream grpc.BidiStreamingServer[UtmCommand, CommandResult]) error {
	cmd := &UtmCommand{}
	if err := stream.RecvMsg(cmd); err != nil {
		return err
	}
	f.received = cmd
	return stream.SendMsg(f.reply)
}

// startFakePanel runs a gRPC server over bufconn and returns a Client wired to it.
func startFakePanel(t *testing.T, reply *CommandResult) (*fakePanelServer, *Client) {
	t.Helper()

	server := &fakePanelServer{reply: reply}
	lis := bufconn.Listen(1024 * 1024)

	srv := grpc.NewServer()
	RegisterPanelServiceServer(srv, server)
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return server, &Client{
		conn:        conn,
		panelClient: NewPanelServiceClient(conn),
		internalKey: "test-key",
	}
}

// TestExecuteCommandTargetsByNumericAgentId asserts the server receives the
// numeric agent id as a string in wire field 1 (UtmCommand.AgentKey) — the
// V11/V12 agent-manager does strconv.Atoi on that field.
func TestExecuteCommandTargetsByNumericAgentId(t *testing.T) {
	server, client := startFakePanel(t, &CommandResult{Result: "OK"})

	res, err := client.ExecuteCommand(context.Background(), 42, "echo hi", "test reason")
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if server.received == nil {
		t.Fatal("server did not receive a UtmCommand")
	}
	if got := server.received.AgentKey; got != "42" {
		t.Errorf("wire field 1 = %q, want %q", got, "42")
	}
	if !res.Success {
		t.Errorf("Success = false, want true for result %q", res.Output)
	}
}

func TestExecuteCommandSuccessTrue(t *testing.T) {
	_, client := startFakePanel(t, &CommandResult{Result: "OK"})

	res, err := client.ExecuteCommand(context.Background(), 7, "true", "r")
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if !res.Success {
		t.Errorf("Success = false, want true")
	}
}

func TestExecuteCommandEmptyResultNotSuccess(t *testing.T) {
	_, client := startFakePanel(t, &CommandResult{Result: ""})

	res, err := client.ExecuteCommand(context.Background(), 7, "true", "r")
	if err != nil {
		t.Fatalf("ExecuteCommand returned error: %v", err)
	}
	if res.Success {
		t.Errorf("Success = true, want false for empty result")
	}
}
