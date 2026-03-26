package model

import "time"

type Snapshot struct {
	GeneratedAt   time.Time `json:"generated_at"`
	GitHubWebBase string    `json:"github_web_base,omitempty"`
	Targets       any       `json:"targets"`
	Repos         []Repo    `json:"repos"`
	Findings      []Finding `json:"findings"`
}

type Repo struct {
	Owner         string `json:"owner"`
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch,omitempty"`
	ScannedRef    string `json:"scanned_ref,omitempty"`
	Archived      bool   `json:"archived"`
	Private       bool   `json:"private"`
}

type Finding struct {
	RepoOwner    string `json:"repo_owner"`
	RepoName     string `json:"repo_name"`
	WorkflowPath string `json:"workflow_path"`

	JobID     string `json:"job_id,omitempty"`
	StepIndex int    `json:"step_index,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	FieldPath string `json:"field_path,omitempty"`

	FilePath string `json:"file_path,omitempty"`
	FileKind string `json:"file_kind"` // workflow_yaml|script|action_yaml|action_entrypoint

	RefType    string `json:"ref_type"` // secret|var|env|runtime_env
	RefName    string `json:"ref_name"`
	Expression string `json:"expression,omitempty"`

	ContextKind string `json:"context_kind,omitempty"` // env_block|run_script|with_input|uses_action|reusable_workflow_call
	ActionUses  string `json:"action_uses,omitempty"`

	Origin string `json:"origin,omitempty"` // workflow_env|expr_secret|expr_var|declared_secret|declared_variable|unknown

	LineStart int `json:"line_start,omitempty"`
	LineEnd   int `json:"line_end,omitempty"`
	ColStart  int `json:"col_start,omitempty"`
	ColEnd    int `json:"col_end,omitempty"`
}

type FileRef struct {
	RepoOwner string
	RepoName  string
	Path      string
	Kind      string // script|action_yaml|action_entrypoint

	WorkflowPath string
	JobID        string
	StepIndex    int
	StepName     string
	FieldPath    string
	ContextKind  string
	ActionUses   string

	BaseDir string // used to resolve relative script paths for local actions

	KnownEnv map[string]OriginHint
}

type OriginHint struct {
	Origin string // workflow_env|expr_secret|expr_var|declared_secret|declared_variable
}
