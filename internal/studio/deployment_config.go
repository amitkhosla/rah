package studio

// Deployment is a named group of gateway instances, topology-agnostic.
// The customer names their own deployments (e.g. "primary", "europe", "dr").
type Deployment struct {
	Name    string   `yaml:"name" json:"name"`
	Targets []string `yaml:"targets" json:"targets"` // gateway management URLs
}

// GateConfig defines the promotion gates that must pass before deploying to an environment.
type GateConfig struct {
	RequireTests          bool     `yaml:"require_tests" json:"require_tests"`
	TestSuites            []string `yaml:"test_suites" json:"test_suites,omitempty"`
	RequireManualApproval bool     `yaml:"require_manual_approval" json:"require_manual_approval"`
	ApprovalTimeoutHours  int      `yaml:"approval_timeout_hours" json:"approval_timeout_hours,omitempty"`
	// RequirePromotionFrom names an environment that must already be deployed before this one accepts a promote.
	RequirePromotionFrom string `yaml:"require_promotion_from" json:"require_promotion_from,omitempty"`
}

// PhaseConfig defines one phase in a phased rollout.
type PhaseConfig struct {
	Name                 string   `yaml:"name" json:"name"`
	Deployments          []string `yaml:"deployments" json:"deployments"`
	RequireApprovalAfter bool     `yaml:"require_approval_after" json:"require_approval_after,omitempty"`
}

// RolloutConfig defines the deployment strategy for an environment.
// Strategy "all" deploys to all listed deployments simultaneously.
// Strategy "phased" executes Phases sequentially, optionally pausing for approval between phases.
type RolloutConfig struct {
	Strategy    string        `yaml:"strategy" json:"strategy"` // "all" | "phased"
	Deployments []string      `yaml:"deployments" json:"deployments,omitempty"`
	Phases      []PhaseConfig `yaml:"phases" json:"phases,omitempty"`
}

// EnvironmentConfig defines a named deployment environment with its gate and rollout plan.
type EnvironmentConfig struct {
	Name    string        `yaml:"name" json:"name"`
	Gate    GateConfig    `yaml:"gate" json:"gate"`
	Rollout RolloutConfig `yaml:"rollout" json:"rollout"`
}

func findEnvironment(envs []EnvironmentConfig, name string) *EnvironmentConfig {
	for i := range envs {
		if envs[i].Name == name {
			return &envs[i]
		}
	}
	return nil
}

func findDeployment(deps []Deployment, name string) *Deployment {
	for i := range deps {
		if deps[i].Name == name {
			return &deps[i]
		}
	}
	return nil
}
