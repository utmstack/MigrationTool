package agentmanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

const (
	DeployTimeout = 180 * time.Second // 3 minutes per agent
)

type DeployResult struct {
	Agent        *Agent
	Success      bool
	Architecture string // Detected architecture (amd64, arm64)
	DownloadOut  string
	InstallOut   string
	Error        error
	Duration     time.Duration
}

type DeployStats struct {
	Total    int
	Success  int
	Failed   int
	Skipped  int
	Duration time.Duration
}

type Deployer struct {
	client      *Client
	concurrency int

	// Bridge credentials: the destination V12 panel host and the internal key
	// shared by source and destination. The bridge reads these from
	// credentials.yml and uses them to dial GetConnectionKey over 443.
	destServer  string
	internalKey string
}

func NewDeployer(client *Client) *Deployer {
	return &Deployer{
		client:      client,
		concurrency: 5, // Deploy to 5 agents in parallel
	}
}

// SetCredentials binds the destination server and internal key that every
// bridge will receive via credentials.yml. Must be called before
// DeployToAgent when writing credentials.
func (d *Deployer) SetCredentials(destServer, internalKey string) {
	d.destServer = destServer
	d.internalKey = internalKey
}

func (d *Deployer) DeployToAgent(ctx context.Context, agent *Agent) *DeployResult {
	startTime := time.Now()
	result := &DeployResult{
		Agent: agent,
	}

	deployCtx, cancel := context.WithTimeout(ctx, DeployTimeout)
	defer cancel()

	platformInfo := ParsePlatformBasic(agent)

	// Step 1: Detect architecture
	archCmd := GetArchDetectCommand(platformInfo)
	archResult, err := d.client.ExecuteCommand(
		deployCtx,
		agent.Id,
		archCmd,
		"Detect architecture for migrate_agent download",
	)
	if err != nil {
		if deployCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
		} else {
			result.Error = fmt.Errorf("architecture detection failed: %w", err)
		}
		result.Duration = time.Since(startTime)
		return result
	}

	if !archResult.Success {
		result.Error = fmt.Errorf("architecture detection failed: %s", archResult.Output)
		result.Duration = time.Since(startTime)
		return result
	}

	// Parse the architecture from output
	platformInfo.Architecture = ParseArchitectureFromOutput(archResult.Output)
	result.Architecture = platformInfo.Architecture

	// Step 2: Download with correct architecture
	downloadCmd := GetDownloadCommand(platformInfo)
	downloadResult, err := d.client.ExecuteCommand(
		deployCtx,
		agent.Id,
		downloadCmd,
		fmt.Sprintf("Download migrate_agent (%s/%s) for V11->V12 migration", platformInfo.OS, platformInfo.Architecture),
	)
	if err != nil {
		if deployCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
		} else {
			result.Error = fmt.Errorf("download command failed: %w", err)
		}
		result.Duration = time.Since(startTime)
		return result
	}
	result.DownloadOut = downloadResult.Output

	// Check for download errors in output
	if !downloadResult.Success {
		result.Error = fmt.Errorf("download failed: %s", downloadResult.Output)
		result.Duration = time.Since(startTime)
		return result
	}

	// Step 3: Verify download succeeded (file exists)
	verifyDownloadCmd := GetVerifyDownloadCommand(platformInfo)
	verifyDownloadResult, err := d.client.ExecuteCommand(
		deployCtx,
		agent.Id,
		verifyDownloadCmd,
		"Verify migrate_agent download",
	)
	if err != nil {
		if deployCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
		} else {
			result.Error = fmt.Errorf("download verification failed: %w", err)
		}
		result.Duration = time.Since(startTime)
		return result
	}

	if !strings.Contains(verifyDownloadResult.Output, "OK") {
		result.Error = fmt.Errorf("download verification failed: file not found after download")
		result.Duration = time.Since(startTime)
		return result
	}

	// Step 4: Install
	installCmd := GetInstallCommand(platformInfo)
	installResult, err := d.client.ExecuteCommand(
		deployCtx,
		agent.Id,
		installCmd,
		"Install migrate_agent service for V11->V12 migration",
	)
	if err != nil {
		if deployCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
		} else {
			result.Error = fmt.Errorf("install command failed: %w", err)
		}
		result.Duration = time.Since(startTime)
		return result
	}
	result.InstallOut = installResult.Output

	if !installResult.Success {
		result.Error = fmt.Errorf("install failed: %s", installResult.Output)
		result.Duration = time.Since(startTime)
		return result
	}

	// Step 5: Verify install succeeded (service exists)
	verifyInstallCmd := GetVerifyInstallCommand(platformInfo)
	verifyInstallResult, err := d.client.ExecuteCommand(
		deployCtx,
		agent.Id,
		verifyInstallCmd,
		"Verify migrate_agent installation",
	)
	if err != nil {
		if deployCtx.Err() == context.DeadlineExceeded {
			result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
		} else {
			result.Error = fmt.Errorf("install verification failed: %w", err)
		}
		result.Duration = time.Since(startTime)
		return result
	}

	if !strings.Contains(verifyInstallResult.Output, "OK") {
		result.Error = fmt.Errorf("install verification failed: service not found after install")
		result.Duration = time.Since(startTime)
		return result
	}

	// Step 6: write credentials.yml for the bridge. Skipped when no
	// destination server was bound via SetCredentials.
	if d.destServer != "" {
		credsYAML := fmt.Sprintf(
			"server: '%s'\ninternal_key: '%s'\n",
			d.destServer, d.internalKey,
		)
		credsB64 := base64.StdEncoding.EncodeToString([]byte(credsYAML))

		writeCredsResult, err := d.client.ExecuteCommand(
			deployCtx,
			agent.Id,
			GetWriteCredentialsCommand(platformInfo, credsB64),
			"Write bridge credentials for V11->V12 connection-key fetch",
		)
		if err != nil {
			if deployCtx.Err() == context.DeadlineExceeded {
				result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
			} else {
				result.Error = fmt.Errorf("credentials write failed: %w", err)
			}
			result.Duration = time.Since(startTime)
			return result
		}
		if !writeCredsResult.Success {
			result.Error = fmt.Errorf("credentials write failed: %s", writeCredsResult.Output)
			result.Duration = time.Since(startTime)
			return result
		}

		verifyCredsResult, err := d.client.ExecuteCommand(
			deployCtx,
			agent.Id,
			GetVerifyCredentialsCommand(platformInfo),
			"Verify bridge credentials file",
		)
		if err != nil {
			if deployCtx.Err() == context.DeadlineExceeded {
				result.Error = fmt.Errorf("deployment timed out after %v", DeployTimeout)
			} else {
				result.Error = fmt.Errorf("credentials verification failed: %w", err)
			}
			result.Duration = time.Since(startTime)
			return result
		}
		if !strings.Contains(verifyCredsResult.Output, "OK") {
			result.Error = fmt.Errorf("credentials verification failed: file not found")
			result.Duration = time.Since(startTime)
			return result
		}
	}

	result.Success = true
	result.Duration = time.Since(startTime)
	return result
}

func (d *Deployer) DeployToAllOnline(ctx context.Context) ([]DeployResult, *DeployStats, error) {
	startTime := time.Now()

	// Get all online agents
	agents, err := d.client.GetOnlineAgents(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get online agents: %w", err)
	}

	if len(agents) == 0 {
		return nil, &DeployStats{
			Total:    0,
			Duration: time.Since(startTime),
		}, nil
	}

	results := make([]DeployResult, len(agents))
	var wg sync.WaitGroup
	sem := make(chan struct{}, d.concurrency)

	// Initialize progress bar
	progress := ui.NewProgress(len(agents), "Deploying migrate_agent")

	for i, agent := range agents {
		wg.Add(1)
		go func(idx int, ag *Agent) {
			defer wg.Done()

			sem <- struct{}{}        // Acquire semaphore
			defer func() { <-sem }() // Release semaphore

			result := d.DeployToAgent(ctx, ag)
			results[idx] = *result

			// Update progress
			label := fmt.Sprintf("%s (%s)", ag.Hostname, ag.Ip)
			if result.Success {
				progress.IncrementWithLabel(label)
			} else {
				progress.IncrementWithLabel(label + " [FAILED]")
			}
		}(i, agent)
	}

	wg.Wait()
	progress.FinishWithMessage("Deployment completed")

	// Calculate stats
	stats := &DeployStats{
		Total:    len(agents),
		Duration: time.Since(startTime),
	}
	for _, r := range results {
		if r.Success {
			stats.Success++
		} else {
			stats.Failed++
		}
	}

	return results, stats, nil
}

func (d *Deployer) CheckOfflineAgents(ctx context.Context) ([]*Agent, error) {
	return d.client.GetOfflineAgents(ctx)
}

func PrintDeploymentSummary(results []DeployResult, stats *DeployStats) {
	ui.Blank()
	ui.SubHeader("Deployment Summary")

	ui.SummaryOrdered("Results",
		[]string{"Total Agents", "Successful", "Failed", "Duration"},
		map[string]string{
			"Total Agents": fmt.Sprintf("%d", stats.Total),
			"Successful":   fmt.Sprintf("%d", stats.Success),
			"Failed":       fmt.Sprintf("%d", stats.Failed),
			"Duration":     stats.Duration.Round(time.Second).String(),
		})

	// Show successful agents with their architectures
	var successful []DeployResult
	for _, r := range results {
		if r.Success {
			successful = append(successful, r)
		}
	}

	if len(successful) > 0 {
		ui.Blank()
		ui.Info("Deployed to:")
		for _, r := range successful {
			ui.Detail(
				fmt.Sprintf("  %s (%s)", r.Agent.Hostname, r.Agent.Ip),
				fmt.Sprintf("%s/%s", r.Agent.Os, r.Architecture),
			)
		}
	}

	// Show failed agents if any
	var failed []DeployResult
	for _, r := range results {
		if !r.Success {
			failed = append(failed, r)
		}
	}

	if len(failed) > 0 {
		ui.Blank()
		ui.Warning(fmt.Sprintf("%d agent(s) failed to receive migrate_agent:", len(failed)))
		for _, r := range failed {
			ui.Detail(
				fmt.Sprintf("  %s (%s)", r.Agent.Hostname, r.Agent.Ip),
				r.Error.Error(),
			)
		}
	}
}

func PrintOfflineWarning(offline []*Agent) {
	if len(offline) == 0 {
		return
	}

	ui.Blank()
	ui.Warning(fmt.Sprintf("%d agent(s) are OFFLINE and will NOT receive the migrate_agent:", len(offline)))
	for _, agent := range offline {
		ui.Detail(
			fmt.Sprintf("  %s", agent.Hostname),
			fmt.Sprintf("IP: %s, Last seen: %s", agent.Ip, agent.LastSeen),
		)
	}
	ui.Blank()
	ui.Info("These agents will remain on V11 until they come online and the migration completes.")
}
