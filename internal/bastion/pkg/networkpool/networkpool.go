package networkpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/google/uuid"
)

const (
	defaultStateFile       = "/var/lib/bastion/network_pool.json"
	defaultTTL             = 1 * time.Hour
	cleanupInterval        = 5 * time.Minute
	stateDirPermissions    = 0700
	stateFilePermissions   = 0600
	defaultSubnetRangeBase = "10.20.0.0"
	defaultSubnetMask      = 16
	highUtilizationWarning = 0.8
)

type NetworkEntry struct {
	NetworkName      string     `json:"network_name"`
	NetworkID        string     `json:"network_id"`
	Subnet           string     `json:"subnet"`
	ConfigHash       string     `json:"config_hash"`
	Driver           string     `json:"driver"`
	CurrentContainer *string    `json:"current_container"`
	CreatedAt        time.Time  `json:"created_at"`
	LastReleasedAt   *time.Time `json:"last_released_at"`
	CleanupAt        *time.Time `json:"cleanup_at"`
	ReuseCount       int        `json:"reuse_count"`
}

type NetworkPoolState struct {
	Networks    map[string]*NetworkEntry `json:"networks"`
	ConfigIndex map[string][]string      `json:"config_index"`
	LastCleanup time.Time                `json:"last_cleanup"`
	mu          sync.RWMutex
}

type SubnetConfig struct {
	BaseIP     string
	SubnetMask int
	MaxSubnets int
}

type Pool struct {
	state          *NetworkPoolState
	stateFile      string
	docker         *client.Client
	cleanupStop    chan struct{}
	cleanupDone    chan struct{}
	cleanupStarted bool
	subnetConfig   SubnetConfig
	logger         *slog.Logger
	mu             sync.Mutex
}

type AcquireResult struct {
	NetworkName string
	NetworkID   string
	Subnet      string
	Reused      bool
}

type ReleaseResult struct {
	CleanedUp bool
}

type Stats struct {
	TotalNetworks     uint32
	ActiveNetworks    uint32
	PooledNetworks    uint32
	PendingCleanup    uint32
	Utilization       float32
	SubnetUtilization float32
	MaxSubnets        uint32
	Healthy           bool
}

func DefaultSubnetConfig() SubnetConfig {
	return SubnetConfig{
		BaseIP:     defaultSubnetRangeBase,
		SubnetMask: defaultSubnetMask,
		MaxSubnets: 65536,
	}
}

func SubnetConfigFromEnv() SubnetConfig {
	config := DefaultSubnetConfig()

	if baseIP := os.Getenv("BASTION_SUBNET_BASE"); baseIP != "" {
		config.BaseIP = baseIP
	}

	if maskStr := os.Getenv("BASTION_SUBNET_MASK"); maskStr != "" {
		var mask int
		if _, err := fmt.Sscanf(maskStr, "%d", &mask); err == nil && mask >= 8 && mask <= 24 {
			config.SubnetMask = mask
			config.MaxSubnets = 1 << uint(24-mask)
		}
	}

	return config
}

func New(ctx context.Context, stateFile string) (*Pool, error) {
	return NewWithConfig(ctx, stateFile, SubnetConfigFromEnv(), nil)
}

func NewWithConfig(ctx context.Context, stateFile string, subnetConfig SubnetConfig, logger *slog.Logger) (*Pool, error) {
	if stateFile == "" {
		stateFile = defaultStateFile
	}

	if logger == nil {
		logger = slog.Default()
	}

	if err := ensureStateDir(stateFile); err != nil {
		return nil, err
	}

	state, err := loadState(stateFile)
	if err != nil {
		return nil, err
	}

	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Docker: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := docker.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("failed to ping Docker daemon: %w", err)
	}

	if err := validateNetworks(ctx, docker, state); err != nil {
		return nil, err
	}

	pool := &Pool{
		state:        state,
		stateFile:    stateFile,
		docker:       docker,
		cleanupStop:  make(chan struct{}),
		cleanupDone:  make(chan struct{}),
		subnetConfig: subnetConfig,
		logger:       logger,
	}

	logger.Info("network pool initialized",
		"subnet_base", subnetConfig.BaseIP,
		"subnet_mask", subnetConfig.SubnetMask,
		"max_subnets", subnetConfig.MaxSubnets,
	)

	return pool, nil
}

func (p *Pool) StartCleanup(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.cleanupStarted {
		p.cleanupStarted = true
		go p.cleanupLoop(ctx)
	}
}

func (p *Pool) Stop() {
	p.mu.Lock()
	started := p.cleanupStarted
	p.mu.Unlock()

	if started {
		close(p.cleanupStop)
		<-p.cleanupDone
	}
}

func (p *Pool) Acquire(ctx context.Context, containerID, configHash string, subnetRange *string, leaseDuration *time.Duration) (*AcquireResult, error) {
	p.state.mu.Lock()

	if networkName := p.findAvailableNetwork(configHash); networkName != "" {
		entry := p.state.Networks[networkName]
		entry.CurrentContainer = &containerID
		entry.CleanupAt = nil
		entry.ReuseCount++

		result := &AcquireResult{
			NetworkName: entry.NetworkName,
			NetworkID:   entry.NetworkID,
			Subnet:      entry.Subnet,
			Reused:      true,
		}

		if networks, ok := p.state.ConfigIndex[configHash]; ok {
			p.state.ConfigIndex[configHash] = removeString(networks, networkName)
		}

		p.state.mu.Unlock()

		if err := p.persist(); err != nil {
			return nil, err
		}

		return result, nil
	}

	p.state.mu.Unlock()

	return p.createNetwork(ctx, containerID, configHash, subnetRange)
}

func (p *Pool) Release(ctx context.Context, containerID, networkName string, forceCleanup bool) (*ReleaseResult, error) {
	p.state.mu.Lock()

	entry, ok := p.state.Networks[networkName]
	if !ok {
		p.state.mu.Unlock()
		return nil, fmt.Errorf("network %s not found in pool", networkName)
	}

	if entry.CurrentContainer == nil || *entry.CurrentContainer != containerID {
		p.state.mu.Unlock()
		return nil, fmt.Errorf("container %s does not own network %s", containerID, networkName)
	}

	entry.CurrentContainer = nil
	now := time.Now()
	entry.LastReleasedAt = &now

	if forceCleanup {
		networkID := entry.NetworkID
		configHash := entry.ConfigHash
		p.state.mu.Unlock()

		if err := p.cleanupNetwork(ctx, networkID); err != nil {
			return nil, err
		}

		p.state.mu.Lock()
		delete(p.state.Networks, networkName)
		if networks, ok := p.state.ConfigIndex[configHash]; ok {
			p.state.ConfigIndex[configHash] = removeString(networks, networkName)
			if len(p.state.ConfigIndex[configHash]) == 0 {
				delete(p.state.ConfigIndex, configHash)
			}
		}
		p.state.mu.Unlock()

		if err := p.persist(); err != nil {
			return nil, err
		}

		return &ReleaseResult{CleanedUp: true}, nil
	}

	cleanupAt := now.Add(defaultTTL)
	entry.CleanupAt = &cleanupAt

	if _, ok := p.state.ConfigIndex[entry.ConfigHash]; !ok {
		p.state.ConfigIndex[entry.ConfigHash] = []string{}
	}
	p.state.ConfigIndex[entry.ConfigHash] = append(p.state.ConfigIndex[entry.ConfigHash], networkName)

	p.state.mu.Unlock()

	if err := p.persist(); err != nil {
		return nil, err
	}

	return &ReleaseResult{CleanedUp: false}, nil
}

func (p *Pool) Stats() *Stats {
	p.state.mu.RLock()
	defer p.state.mu.RUnlock()

	total := len(p.state.Networks)
	active := 0
	pendingCleanup := 0

	for _, entry := range p.state.Networks {
		if entry.CurrentContainer != nil {
			active++
		}
		if entry.CleanupAt != nil {
			pendingCleanup++
		}
	}

	pooled := total - active
	utilization := float32(0)
	if total > 0 {
		utilization = float32(active) / float32(total)
	}

	subnetUtilization := float32(0)
	if p.subnetConfig.MaxSubnets > 0 {
		subnetUtilization = float32(total) / float32(p.subnetConfig.MaxSubnets)
	}

	healthy := utilization < 0.9 && subnetUtilization < highUtilizationWarning

	return &Stats{
		TotalNetworks:     uint32(total),
		ActiveNetworks:    uint32(active),
		PooledNetworks:    uint32(pooled),
		PendingCleanup:    uint32(pendingCleanup),
		Utilization:       utilization,
		SubnetUtilization: subnetUtilization,
		MaxSubnets:        uint32(p.subnetConfig.MaxSubnets),
		Healthy:           healthy,
	}
}

func (p *Pool) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	defer close(p.cleanupDone)

	for {
		select {
		case <-ticker.C:
			_ = p.runCleanup(ctx)
		case <-p.cleanupStop:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (p *Pool) runCleanup(ctx context.Context) error {
	now := time.Now()
	p.state.mu.Lock()

	var toCleanup []struct {
		name       string
		id         string
		configHash string
	}

	for name, entry := range p.state.Networks {
		if entry.CleanupAt != nil && entry.CleanupAt.Before(now) && entry.CurrentContainer == nil {
			toCleanup = append(toCleanup, struct {
				name       string
				id         string
				configHash string
			}{name, entry.NetworkID, entry.ConfigHash})
		}
	}

	p.state.mu.Unlock()

	for _, item := range toCleanup {
		if err := p.cleanupNetwork(ctx, item.id); err != nil {
			continue
		}

		p.state.mu.Lock()
		delete(p.state.Networks, item.name)
		if networks, ok := p.state.ConfigIndex[item.configHash]; ok {
			p.state.ConfigIndex[item.configHash] = removeString(networks, item.name)
			if len(p.state.ConfigIndex[item.configHash]) == 0 {
				delete(p.state.ConfigIndex, item.configHash)
			}
		}
		p.state.mu.Unlock()
	}

	p.state.mu.Lock()
	p.state.LastCleanup = now
	p.state.mu.Unlock()

	return p.persist()
}

func (p *Pool) createNetwork(ctx context.Context, containerID, configHash string, subnetRange *string) (*AcquireResult, error) {
	networkName := fmt.Sprintf("iso-net-%s", uuid.New().String()[:8])

	// Retry logic with exponential backoff for handling transient failures and race conditions
	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Allocate subnet
		subnet := ""
		if subnetRange != nil && *subnetRange != "" {
			subnet = *subnetRange
		} else {
			var err error
			subnet, err = p.allocateSubnet(ctx)
			if err != nil {
				return nil, err
			}
		}

		// Attempt to create network
		resp, err := p.docker.NetworkCreate(ctx, networkName, network.CreateOptions{
			Driver: "bridge",
			IPAM: &network.IPAM{
				Config: []network.IPAMConfig{
					{Subnet: subnet},
				},
			},
		})

		if err == nil {
			// Success - create entry and return
			p.state.mu.Lock()
			entry := &NetworkEntry{
				NetworkName:      networkName,
				NetworkID:        resp.ID,
				Subnet:           subnet,
				ConfigHash:       configHash,
				Driver:           "bridge",
				CurrentContainer: &containerID,
				CreatedAt:        time.Now(),
				ReuseCount:       0,
			}
			p.state.Networks[networkName] = entry
			p.state.mu.Unlock()

			if err := p.persist(); err != nil {
				return nil, err
			}

			return &AcquireResult{
				NetworkName: networkName,
				NetworkID:   resp.ID,
				Subnet:      subnet,
				Reused:      false,
			}, nil
		}

		lastErr = err

		// Check if error is due to subnet overlap or address already in use (retryable errors)
		errMsg := err.Error()
		isRetryable := false
		if subnetRange == nil || *subnetRange == "" {
			// Only retry if we're auto-allocating subnets
			isRetryable =
				(containsAny(errMsg, "Pool overlaps", "overlaps with other") ||
					containsAny(errMsg, "already in use", "address already"))
		}

		if isRetryable && attempt < maxRetries-1 {
			// Exponential backoff: 100ms, 200ms, 400ms
			backoff := time.Duration(100*(1<<uint(attempt))) * time.Millisecond
			time.Sleep(backoff)
			continue
		}

		// Non-retryable error or max retries exceeded
		break
	}

	return nil, fmt.Errorf("failed to create network after %d attempts: %w", maxRetries, lastErr)
}

func (p *Pool) cleanupNetwork(ctx context.Context, networkID string) error {
	inspect, err := p.docker.NetworkInspect(ctx, networkID, network.InspectOptions{})
	if err == nil {
		for containerID := range inspect.Containers {
			_ = p.docker.NetworkDisconnect(ctx, networkID, containerID, true)
		}
	}

	return p.docker.NetworkRemove(ctx, networkID)
}

func (p *Pool) findAvailableNetwork(configHash string) string {
	if networks, ok := p.state.ConfigIndex[configHash]; ok {
		for _, networkName := range networks {
			if entry, ok := p.state.Networks[networkName]; ok && entry.CurrentContainer == nil {
				return networkName
			}
		}
	}
	return ""
}

func (p *Pool) allocateSubnet(ctx context.Context) (string, error) {
	dockerNetworks, err := p.docker.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list Docker networks: %w", err)
	}

	usedSubnets := make(map[string]bool)

	p.state.mu.RLock()
	for _, entry := range p.state.Networks {
		usedSubnets[entry.Subnet] = true
	}
	pooledCount := len(p.state.Networks)
	p.state.mu.RUnlock()

	for _, net := range dockerNetworks {
		if net.IPAM.Config != nil {
			for _, config := range net.IPAM.Config {
				usedSubnets[config.Subnet] = true
			}
		}
	}

	utilization := float32(pooledCount) / float32(p.subnetConfig.MaxSubnets)
	if utilization > highUtilizationWarning {
		p.logger.Warn("high subnet utilization",
			"utilization", fmt.Sprintf("%.1f%%", utilization*100),
			"used", pooledCount,
			"max", p.subnetConfig.MaxSubnets,
		)
	}

	baseIP := net.ParseIP(p.subnetConfig.BaseIP)
	if baseIP == nil {
		return "", fmt.Errorf("invalid base IP: %s", p.subnetConfig.BaseIP)
	}
	baseIP = baseIP.To4()
	if baseIP == nil {
		return "", fmt.Errorf("base IP must be IPv4: %s", p.subnetConfig.BaseIP)
	}

	for i := 0; i < p.subnetConfig.MaxSubnets; i++ {
		subnet := p.generateSubnet(baseIP, i)
		if !usedSubnets[subnet] {
			return subnet, nil
		}
	}

	return "", fmt.Errorf("no available subnets (all %d checked in %s/%d range)",
		p.subnetConfig.MaxSubnets, p.subnetConfig.BaseIP, p.subnetConfig.SubnetMask)
}

func (p *Pool) generateSubnet(baseIP net.IP, index int) string {
	octet1 := int(baseIP[1]) + (index / 256)
	octet2 := index % 256

	return fmt.Sprintf("%d.%d.%d.0/24", baseIP[0], octet1, octet2)
}

func (p *Pool) persist() error {
	p.state.mu.RLock()
	data, err := json.MarshalIndent(p.state, "", "  ")
	p.state.mu.RUnlock()

	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	tmpFile := p.stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, stateFilePermissions); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	if err := os.Rename(tmpFile, p.stateFile); err != nil {
		_ = os.Remove(tmpFile)
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}

func loadState(stateFile string) (*NetworkPoolState, error) {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return &NetworkPoolState{
				Networks:    make(map[string]*NetworkEntry),
				ConfigIndex: make(map[string][]string),
				LastCleanup: time.Now(),
			}, nil
		}
		return nil, fmt.Errorf("failed to read state file: %w", err)
	}

	var state NetworkPoolState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal state: %w", err)
	}

	if state.Networks == nil {
		state.Networks = make(map[string]*NetworkEntry)
	}
	if state.ConfigIndex == nil {
		state.ConfigIndex = make(map[string][]string)
	}

	return &state, nil
}

func validateNetworks(ctx context.Context, docker *client.Client, state *NetworkPoolState) error {
	networks, err := docker.NetworkList(ctx, network.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list Docker networks: %w", err)
	}

	dockerNetworkIDs := make(map[string]bool)
	for _, n := range networks {
		dockerNetworkIDs[n.ID] = true
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	for name, entry := range state.Networks {
		if !dockerNetworkIDs[entry.NetworkID] {
			delete(state.Networks, name)
		}
	}

	state.ConfigIndex = make(map[string][]string)
	for name, entry := range state.Networks {
		if entry.CurrentContainer == nil {
			state.ConfigIndex[entry.ConfigHash] = append(state.ConfigIndex[entry.ConfigHash], name)
		}
	}

	return nil
}

func ensureStateDir(stateFile string) error {
	dir := filepath.Dir(stateFile)
	if err := os.MkdirAll(dir, stateDirPermissions); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}
	return nil
}

func removeString(slice []string, s string) []string {
	result := make([]string, 0, len(slice))
	for _, item := range slice {
		if item != s {
			result = append(result, item)
		}
	}
	return result
}

func containsAny(s string, substrs ...string) bool {
	for _, substr := range substrs {
		if len(substr) > 0 && len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}
