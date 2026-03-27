package model

import "time"

type Snapshot struct {
	GeneratedAt   time.Time `json:"generated_at"`
	GitHubWebBase string    `json:"github_web_base,omitempty"`
	Targets       any       `json:"targets"`
	Repos         []Repo    `json:"repos"`
	Findings      []Finding `json:"findings"`

	MergedFindings []MergedFinding `json:"merged_findings,omitempty"`

	DeclaredSecrets   []DeclaredItem `json:"declared_secrets,omitempty"`
	DeclaredVariables []DeclaredItem `json:"declared_variables,omitempty"`

	DeepInspectWarnings []string `json:"deep_inspect_warnings,omitempty"`
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
	Environment  string `json:"environment,omitempty"`

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

	SourceKey string `json:"source_key,omitempty"`

	LineStart int `json:"line_start,omitempty"`
	LineEnd   int `json:"line_end,omitempty"`
	ColStart  int `json:"col_start,omitempty"`
	ColEnd    int `json:"col_end,omitempty"`
}

type FindingContext struct {
	WorkflowPath string `json:"workflow_path"`
	Environment  string `json:"environment,omitempty"`

	JobID     string `json:"job_id,omitempty"`
	StepIndex int    `json:"step_index,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	FieldPath string `json:"field_path,omitempty"`

	ContextKind string `json:"context_kind,omitempty"`
	ActionUses  string `json:"action_uses,omitempty"`
	Origin      string `json:"origin,omitempty"`
}

type MergedFinding struct {
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`

	RefType    string `json:"ref_type"` // secret|var|env|runtime_env
	RefName    string `json:"ref_name"`
	Expression string `json:"expression,omitempty"`

	FilePath string `json:"file_path,omitempty"`
	FileKind string `json:"file_kind"` // workflow_yaml|script|action_yaml|action_entrypoint

	WorkflowPath string `json:"workflow_path,omitempty"`

	LineStart int `json:"line_start,omitempty"`
	LineEnd   int `json:"line_end,omitempty"`
	ColStart  int `json:"col_start,omitempty"`
	ColEnd    int `json:"col_end,omitempty"`

	SourceKey string `json:"source_key,omitempty"`
	Count     int    `json:"count"`

	Contexts []FindingContext `json:"contexts"`
}

type FileRef struct {
	RepoOwner string
	RepoName  string
	Path      string
	Kind      string // script|action_yaml|action_entrypoint

	WorkflowPath string
	Environment  string
	JobID        string
	StepIndex    int
	StepName     string
	FieldPath    string
	ContextKind  string
	ActionUses   string

	BaseDir string // used to resolve relative script paths for local actions

	KnownEnv map[string]OriginHint
}

type DeclaredItem struct {
	Name        string `json:"name"`
	ScopeKind   string `json:"scope_kind"` // org|repo|environment
	Org         string `json:"org,omitempty"`
	RepoOwner   string `json:"repo_owner,omitempty"`
	RepoName    string `json:"repo_name,omitempty"`
	Environment string `json:"environment,omitempty"`
	ManageURL   string `json:"manage_url,omitempty"`
	Used        bool   `json:"used"`
	UsedCount   int    `json:"used_count,omitempty"`
}

type OriginHint struct {
	Origin string // workflow_env|expr_secret|expr_var|declared_secret|declared_variable
}
