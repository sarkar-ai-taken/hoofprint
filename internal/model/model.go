// Package model holds the control plane's domain types. They are storage- and
// transport-agnostic; the store and the gRPC layer convert to and from them.
package model

import (
	"time"
)

// Tier is the integration tier detected for a workload change (SPEC.md).
type Tier int

const (
	TierUnspecified Tier = 0
	// TierWorkload: any Deployment/StatefulSet/DaemonSet pod-template change.
	TierWorkload Tier = 1
	// TierProgressive: Argo Rollouts or Flagger canary, baseline and canary
	// observable side by side.
	TierProgressive Tier = 2
	// TierPipeline: Hoofprint-managed deploy stage (v1).
	TierPipeline Tier = 3
)

func (t Tier) String() string {
	switch t {
	case TierWorkload:
		return "workload"
	case TierProgressive:
		return "progressive"
	case TierPipeline:
		return "pipeline"
	default:
		return "unspecified"
	}
}

// MarshalText makes Tier serialise as its name in JSON.
func (t Tier) MarshalText() ([]byte, error) { return []byte(t.String()), nil }

// UnmarshalText accepts the names produced by String.
func (t *Tier) UnmarshalText(b []byte) error {
	switch string(b) {
	case "workload":
		*t = TierWorkload
	case "progressive":
		*t = TierProgressive
	case "pipeline":
		*t = TierPipeline
	default:
		*t = TierUnspecified
	}
	return nil
}

// AgentStatus is the control plane's view of an agent's liveness.
type AgentStatus string

const (
	AgentOnline  AgentStatus = "online"
	AgentOffline AgentStatus = "offline"
)

// Agent is a registered delegate (ADR-0004).
type Agent struct {
	ID            string            `json:"id"`
	Cluster       string            `json:"cluster"`
	Version       string            `json:"version"`
	Capabilities  []string          `json:"capabilities"`
	Tags          map[string]string `json:"tags"`
	Status        AgentStatus       `json:"status"`
	RegisteredAt  time.Time         `json:"registered_at"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
}

// HasCapability reports whether the agent advertised cap at registration.
func (a *Agent) HasCapability(cap string) bool {
	for _, c := range a.Capabilities {
		if c == cap {
			return true
		}
	}
	return false
}

// MatchesTags reports whether every selector key/value is present on the agent.
// An empty selector matches every agent.
func (a *Agent) MatchesTags(selector map[string]string) bool {
	for k, v := range selector {
		if a.Tags[k] != v {
			return false
		}
	}
	return true
}

// ContainerImage is one container's image in a pod template.
type ContainerImage struct {
	Container string `json:"container"`
	Image     string `json:"image"`
}

// Deploy is one observed workload change. It is the unit the verification
// gate produces a verdict for.
type Deploy struct {
	ID                 string            `json:"id"`
	AgentID            string            `json:"agent_id"`
	Cluster            string            `json:"cluster"`
	Namespace          string            `json:"namespace"`
	Kind               string            `json:"kind"`
	Name               string            `json:"name"`
	Tier               Tier              `json:"tier"`
	Controller         string            `json:"controller,omitempty"`
	ImagesBefore       []ContainerImage  `json:"images_before"`
	ImagesAfter        []ContainerImage  `json:"images_after"`
	TemplateHashBefore string            `json:"template_hash_before"`
	TemplateHashAfter  string            `json:"template_hash_after"`
	RevisionBefore     string            `json:"revision_before,omitempty"`
	RevisionAfter      string            `json:"revision_after,omitempty"`
	Labels             map[string]string `json:"labels,omitempty"`
	Annotations        map[string]string `json:"annotations,omitempty"`
	ObservedAt         time.Time         `json:"observed_at"`
	CreatedAt          time.Time         `json:"created_at"`
}

// Outcome is the judge's call on a deploy.
type Outcome string

const (
	OutcomePass   Outcome = "pass"
	OutcomeFail   Outcome = "fail"
	OutcomeUnsure Outcome = "unsure"
)

// Verdict is a judgement about a deploy, with the evidence that supports it.
// Every verdict is persisted for audit (SPEC.md).
type Verdict struct {
	ID         string    `json:"id"`
	DeployID   string    `json:"deploy_id"`
	Judge      string    `json:"judge"` // "stats", "llm:<model>"
	Outcome    Outcome   `json:"outcome"`
	Confidence float64   `json:"confidence"` // 0..1
	Rationale  string    `json:"rationale"`
	CreatedAt  time.Time `json:"created_at"`
}

// Evidence is one artefact a verdict was based on: a metric comparison, a
// log excerpt, a diff hunk, a k8s event. Payload is kind-specific JSON.
type Evidence struct {
	ID        string    `json:"id"`
	VerdictID string    `json:"verdict_id"`
	Kind      string    `json:"kind"` // "metric", "log", "diff", "event", "trace"
	Title     string    `json:"title"`
	Payload   []byte    `json:"payload"`
	CreatedAt time.Time `json:"created_at"`
}

// DeployFilter narrows a Deploy listing.
type DeployFilter struct {
	Cluster   string
	Namespace string
	Limit     int
}
