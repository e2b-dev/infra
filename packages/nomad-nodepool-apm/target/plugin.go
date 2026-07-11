package target

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad-autoscaler/plugins/base"
	targetsdk "github.com/hashicorp/nomad-autoscaler/plugins/target"
	"github.com/hashicorp/nomad-autoscaler/sdk"
	"github.com/hashicorp/nomad/api"
)

const (
	pluginName  = "nomad-deployment-aware-target"
	maxAttempts = 5
	retryDelay  = 10 * time.Millisecond

	// deploymentStatusInitializing is reported by Nomad for a deployment that
	// has been created but not yet processed; the api package has no constant
	// for it.
	deploymentStatusInitializing = "initializing"
)

var pluginInfo = &base.PluginInfo{Name: pluginName, PluginType: sdk.PluginTypeTarget}

// errAutoRevertEnabled is a configuration error retrying cannot fix: the
// plugin fails deployments to unblock scaling, and with auto_revert=true
// Nomad would restore the previous job version on every such failure.
var errAutoRevertEnabled = errors.New("auto_revert is enabled; " + pluginName + " requires auto_revert=false")

var _ targetsdk.Target = (*Plugin)(nil)

type jobKey struct {
	namespace string
	job       string
}

type Plugin struct {
	client           *api.Client
	logger           hclog.Logger
	defaultNamespace string
	locksMu          sync.Mutex
	locks            map[jobKey]*sync.Mutex
	retryDelay       time.Duration
}

func New(log hclog.Logger) *Plugin {
	return &Plugin{logger: log, locks: make(map[jobKey]*sync.Mutex), retryDelay: retryDelay}
}

func (p *Plugin) SetConfig(config map[string]string) error {
	cfg := api.DefaultConfig()
	if value := config["nomad_address"]; value != "" {
		cfg.Address = value
	}
	if value := config["nomad_token"]; value != "" {
		cfg.SecretID = value
	}
	if value := config["nomad_region"]; value != "" {
		cfg.Region = value
	}
	if value := config["nomad_namespace"]; value != "" {
		cfg.Namespace = value
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return fmt.Errorf("failed to create Nomad client: %w", err)
	}
	p.client = client
	p.defaultNamespace = cfg.Namespace

	return nil
}

func (p *Plugin) PluginInfo() (*base.PluginInfo, error) {
	return pluginInfo, nil
}

func (p *Plugin) Scale(action sdk.ScalingAction, config map[string]string) error {
	if action.Count == sdk.StrategyActionMetaValueDryRunCount {
		return nil
	}
	if p.client == nil {
		return errors.New("target plugin is not configured")
	}

	jobID, group, namespace, err := p.target(config)
	if err != nil {
		return err
	}
	lock := p.jobLock(jobKey{namespace: namespace, job: jobID})
	lock.Lock()
	defer lock.Unlock()

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = p.attemptScale(action, jobID, group, namespace)
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, errAutoRevertEnabled) {
			return lastErr
		}
		p.logger.Warn("scale reconciliation attempt failed",
			"namespace", namespace, "job", jobID, "group", group, "attempt", attempt, "error", lastErr)
		if attempt < maxAttempts && p.retryDelay > 0 {
			time.Sleep(p.retryDelay)
		}
	}

	return fmt.Errorf("scale reconciliation exhausted for %s/%s group %q after %d attempts: %w",
		namespace, jobID, group, maxAttempts, lastErr)
}

// attemptScale performs one reconciliation pass from fresh state. It is a
// no-op when the durable count already matches; otherwise it fails a
// conflicting active deployment (Nomad rejects scaling while one is in
// progress) or applies and verifies the new count. A nil return means the
// durable count matches the action; any error is retryable from fresh state.
func (p *Plugin) attemptScale(action sdk.ScalingAction, jobID, group, namespace string) error {
	query := &api.QueryOptions{Namespace: namespace}
	write := &api.WriteOptions{Namespace: namespace}

	job, err := p.readJob(jobID, query)
	if err != nil {
		return err
	}
	count, err := taskGroupCount(job, group)
	if err != nil {
		return err
	}
	if count == action.Count {
		// The durable count already matches. Any active deployment is a
		// rollout converging to the desired count; leave it alone.
		return nil
	}

	if autoRevertEnabled(job, group) {
		return fmt.Errorf("%w: job %s/%s group %q", errAutoRevertEnabled, namespace, jobID, group)
	}

	deployment, err := p.readDeployment(jobID, query)
	if err != nil {
		return err
	}

	// The count must change, and Nomad rejects scaling while a deployment is
	// active, so fail the conflicting deployment and restart from fresh state.
	if activeForJob(job, deployment) {
		p.logger.Info("failing active deployment before scaling",
			"namespace", namespace, "job", jobID, "deployment", deployment.ID)
		if _, _, err := p.client.Deployments().Fail(deployment.ID, write); err != nil {
			return fmt.Errorf("failed to fail deployment %s: %w", deployment.ID, err)
		}

		return fmt.Errorf("marked deployment %s failed; rechecking from fresh state", deployment.ID)
	}

	if job.JobModifyIndex == nil {
		return fmt.Errorf("job %s/%s has no JobModifyIndex", namespace, jobID)
	}
	desired := action.Count
	req := &api.ScalingRequest{
		Count:          &desired,
		Target:         map[string]string{"Job": jobID, "Group": group},
		Message:        action.Reason,
		Error:          action.Error,
		Meta:           action.Meta,
		JobModifyIndex: *job.JobModifyIndex,
	}
	if _, _, err := p.client.Jobs().ScaleWithRequest(jobID, req, write); err != nil {
		return fmt.Errorf("scale write raced: %w", err)
	}

	// Verify only the durable count. The scale itself spawns a deployment for
	// its own rollout; that is expected and left to run.
	job, err = p.readJob(jobID, query)
	if err != nil {
		return err
	}
	count, err = taskGroupCount(job, group)
	if err != nil {
		return err
	}
	if count != action.Count {
		return fmt.Errorf("count after scale is %d, want %d", count, action.Count)
	}

	return nil
}

func (p *Plugin) Status(config map[string]string) (*sdk.TargetStatus, error) {
	if p.client == nil {
		return nil, errors.New("target plugin is not configured")
	}
	jobID, group, namespace, err := p.target(config)
	if err != nil {
		return nil, err
	}
	status, _, err := p.client.Jobs().ScaleStatus(jobID, &api.QueryOptions{Namespace: namespace})
	if err != nil {
		return nil, fmt.Errorf("failed to read scale status for %s/%s: %w", namespace, jobID, err)
	}
	groupStatus, ok := status.TaskGroups[group]
	if !ok {
		return nil, fmt.Errorf("task group %q not found", group)
	}
	meta := map[string]string{
		"nomad_autoscaler.target.nomad." + jobID + ".stopped": strconv.FormatBool(status.JobStopped),
	}
	if len(groupStatus.Events) > 0 {
		meta[sdk.TargetStatusMetaKeyLastEvent] = strconv.FormatUint(groupStatus.Events[0].Time, 10)
	}

	return &sdk.TargetStatus{Ready: !status.JobStopped, Count: int64(groupStatus.Running), Meta: meta}, nil
}

func (p *Plugin) target(config map[string]string) (string, string, string, error) {
	jobID := config[sdk.TargetConfigKeyJob]
	if jobID == "" {
		return "", "", "", fmt.Errorf("required config key %q not found", sdk.TargetConfigKeyJob)
	}
	group := config[sdk.TargetConfigKeyTaskGroup]
	if group == "" {
		return "", "", "", fmt.Errorf("required config key %q not found", sdk.TargetConfigKeyTaskGroup)
	}
	namespace := config[sdk.TargetConfigKeyNamespace]
	if namespace == "" {
		namespace = p.defaultNamespace
	}
	if namespace == "" {
		namespace = api.DefaultNamespace
	}

	return jobID, group, namespace, nil
}

func (p *Plugin) jobLock(key jobKey) *sync.Mutex {
	p.locksMu.Lock()
	defer p.locksMu.Unlock()
	if p.locks[key] == nil {
		p.locks[key] = &sync.Mutex{}
	}

	return p.locks[key]
}

func (p *Plugin) readJob(jobID string, query *api.QueryOptions) (*api.Job, error) {
	job, _, err := p.client.Jobs().Info(jobID, query)
	if err != nil {
		return nil, fmt.Errorf("failed to read job %s/%s: %w", query.Namespace, jobID, err)
	}

	return job, nil
}

func (p *Plugin) readDeployment(jobID string, query *api.QueryOptions) (*api.Deployment, error) {
	deployment, _, err := p.client.Jobs().LatestDeployment(jobID, query)
	if err != nil {
		return nil, fmt.Errorf("failed to read latest deployment for %s/%s: %w", query.Namespace, jobID, err)
	}

	return deployment, nil
}

func taskGroupCount(job *api.Job, group string) (int64, error) {
	for _, taskGroup := range job.TaskGroups {
		if taskGroup != nil && taskGroup.Name != nil && *taskGroup.Name == group {
			if taskGroup.Count == nil {
				return 0, fmt.Errorf("task group %q has no durable count", group)
			}

			return int64(*taskGroup.Count), nil
		}
	}

	return 0, fmt.Errorf("task group %q not found", group)
}

// autoRevertEnabled reports whether the target task group has
// auto_revert=true in its update strategy. Nomad canonicalizes the job-level
// update block into each group, but the job-level strategy is still consulted
// as a fallback.
func autoRevertEnabled(job *api.Job, group string) bool {
	var update *api.UpdateStrategy
	for _, taskGroup := range job.TaskGroups {
		if taskGroup != nil && taskGroup.Name != nil && *taskGroup.Name == group {
			update = taskGroup.Update

			break
		}
	}
	if update == nil {
		update = job.Update
	}

	return update != nil && update.AutoRevert != nil && *update.AutoRevert
}

func activeForJob(job *api.Job, deployment *api.Deployment) bool {
	if job == nil || job.CreateIndex == nil || deployment == nil || deployment.JobCreateIndex != *job.CreateIndex {
		return false
	}
	switch deployment.Status {
	case api.DeploymentStatusRunning, api.DeploymentStatusPaused, api.DeploymentStatusBlocked,
		api.DeploymentStatusUnblocking, api.DeploymentStatusPending, deploymentStatusInitializing:
		return true
	default:
		return false
	}
}
