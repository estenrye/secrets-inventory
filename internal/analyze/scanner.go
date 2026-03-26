package analyze

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"secret-inventory/internal/model"
)

type ScannerOptions struct {
	ScriptExtensions []string
	MaxFileBytes     int64
	IncludeUnknown   bool
}

type Scanner struct {
	opts ScannerOptions
}

func NewScanner(opts ScannerOptions) *Scanner {
	return &Scanner{opts: opts}
}

var (
	reSecret = regexp.MustCompile(`(?i)\bsecrets\.([A-Z0-9_]+)\b`)
	reVar    = regexp.MustCompile(`(?i)\bvars\.([A-Z0-9_]+)\b`)
	reEnv    = regexp.MustCompile(`(?i)\benv\.([A-Z0-9_]+)\b`)

	reShellEnv = regexp.MustCompile(`\$(\{)?([A-Za-z_][A-Za-z0-9_]*)\}?`)

	reRunScriptPath = regexp.MustCompile(`(?m)(?:^|\s)(?:bash|sh|python|node)?\s*(\./[^\s'"\\]+)`)
)

func (s *Scanner) ScanWorkflowYAML(owner, repo, workflowPath, yamlText string) ([]model.Finding, []model.FileRef, error) {
	known := map[string]model.OriginHint{}
	seen := map[string]struct{}{}

	findings := []model.Finding{}
	addFiles := []model.FileRef{}

	appendUnique := func(fs ...model.Finding) {
		for _, f := range fs {
			k := findingKey(f)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			findings = append(findings, f)
		}
	}

	// Parse YAML if possible for context; fall back to raw scan.
	var root yaml.Node
	err := yaml.Unmarshal([]byte(yamlText), &root)
	if err != nil {
		// raw scan only
		appendUnique(scanStringForRefs(owner, repo, workflowPath, "workflow_yaml", "", 0, "", "", "raw_scan", "", yamlText, known)...)
		return findings, addFiles, err
	}

	// Always do a raw-text pass across the full YAML, even when parsing succeeds.
	// This catches references in keys/sections that the AST-walk doesn't currently traverse.
	appendUnique(scanStringForRefs(owner, repo, workflowPath, "workflow_yaml", "", 0, "", "", "raw_scan", "", yamlText, known)...)

	// Walk the YAML for env declarations and for common string fields.
	walkWorkflow(&root, func(fieldPath, jobID string, stepIndex int, stepName, contextKind, actionUses string, val string) {
		if strings.HasPrefix(val, "env_key:") {
			name := strings.TrimPrefix(val, "env_key:")
			name = strings.TrimSpace(name)
			if name != "" {
				known[name] = model.OriginHint{Origin: "workflow_env"}
			}
			return
		}
		appendUnique(scanStringForRefs(owner, repo, workflowPath, "workflow_yaml", jobID, stepIndex, stepName, fieldPath, contextKind, actionUses, val, known)...)

		// Discover script entrypoints in run blocks.
		if contextKind == "run_script" {
			for _, m := range reRunScriptPath.FindAllStringSubmatch(val, -1) {
				p := m[1]
				if !strings.HasPrefix(p, "./") {
					continue
				}
				rel := strings.TrimPrefix(p, "./")
				if !s.allowedScript(rel) {
					continue
				}
				addFiles = append(addFiles, model.FileRef{
					RepoOwner:    owner,
					RepoName:     repo,
					Path:         rel,
					Kind:         "script",
					WorkflowPath: workflowPath,
					JobID:        jobID,
					StepIndex:    stepIndex,
					StepName:     stepName,
					FieldPath:    fieldPath,
					ContextKind:  contextKind,
					ActionUses:   actionUses,
					KnownEnv:     copyKnown(known),
				})
			}
		}

		// Local action entrypoint
		if contextKind == "uses_action" && strings.HasPrefix(strings.TrimSpace(val), "./") {
			aDir := strings.TrimPrefix(strings.TrimSpace(val), "./")
			aDir = strings.TrimSuffix(aDir, "/")
			addFiles = append(addFiles, model.FileRef{
				RepoOwner:    owner,
				RepoName:     repo,
				Path:         path.Join(aDir, "action.yml"),
				Kind:         "action_yaml",
				WorkflowPath: workflowPath,
				JobID:        jobID,
				StepIndex:    stepIndex,
				StepName:     stepName,
				FieldPath:    fieldPath,
				ContextKind:  contextKind,
				ActionUses:   val,
				BaseDir:      aDir,
				KnownEnv:     copyKnown(known),
			})
			addFiles = append(addFiles, model.FileRef{
				RepoOwner:    owner,
				RepoName:     repo,
				Path:         path.Join(aDir, "action.yaml"),
				Kind:         "action_yaml",
				WorkflowPath: workflowPath,
				JobID:        jobID,
				StepIndex:    stepIndex,
				StepName:     stepName,
				FieldPath:    fieldPath,
				ContextKind:  contextKind,
				ActionUses:   val,
				BaseDir:      aDir,
				KnownEnv:     copyKnown(known),
			})
		}
	})

	return findings, addFiles, nil
}

func findingKey(f model.Finding) string {
	// Must be stable across runs and include enough fields to avoid accidental collisions.
	return strings.Join([]string{
		f.RepoOwner,
		f.RepoName,
		f.WorkflowPath,
		f.JobID,
		fmt.Sprintf("%d", f.StepIndex),
		f.StepName,
		f.FieldPath,
		f.FileKind,
		f.FilePath,
		f.RefType,
		f.RefName,
		f.ContextKind,
		f.ActionUses,
	}, "|")
}

func (s *Scanner) ScanRepoFile(ref model.FileRef, content string) ([]model.Finding, []model.FileRef, error) {
	findings := []model.Finding{}
	moreFiles := []model.FileRef{}

	switch ref.Kind {
	case "script", "action_entrypoint":
		// shell-style env usage
		for _, m := range reShellEnv.FindAllStringSubmatchIndex(content, -1) {
			name := content[m[4]:m[5]]
			origin := "unknown"
			if ref.KnownEnv != nil {
				if h, ok := ref.KnownEnv[name]; ok {
					origin = h.Origin
				}
			}
			if origin == "unknown" && !s.opts.IncludeUnknown {
				continue
			}
			findings = append(findings, model.Finding{
				RepoOwner:    ref.RepoOwner,
				RepoName:     ref.RepoName,
				WorkflowPath: ref.WorkflowPath,
				JobID:        ref.JobID,
				StepIndex:    ref.StepIndex,
				StepName:     ref.StepName,
				FieldPath:    ref.FieldPath,
				FilePath:     ref.Path,
				FileKind:     ref.Kind,
				RefType:      "runtime_env",
				RefName:      name,
				ContextKind:  ref.ContextKind,
				ActionUses:   ref.ActionUses,
				Origin:       origin,
			})
		}

		// Also scan for expression refs in case scripts contain them.
		findings = append(findings, scanStringForRefs(ref.RepoOwner, ref.RepoName, ref.WorkflowPath, ref.Kind, ref.JobID, ref.StepIndex, ref.StepName, ref.FieldPath, ref.ContextKind, ref.ActionUses, content, ref.KnownEnv)...)

	case "action_yaml":
		var root yaml.Node
		if err := yaml.Unmarshal([]byte(content), &root); err != nil {
			return findings, moreFiles, err
		}

		// Composite action run steps
		// Also discover runs.main for node actions.
		walkActionYAML(&root, func(fieldPath, kind, val string) {
			findings = append(findings, scanStringForRefs(ref.RepoOwner, ref.RepoName, ref.WorkflowPath, "action_yaml", ref.JobID, ref.StepIndex, ref.StepName, fieldPath, kind, ref.ActionUses, val, ref.KnownEnv)...)
			if kind == "run_script" {
				for _, m := range reRunScriptPath.FindAllStringSubmatch(val, -1) {
					p := m[1]
					if !strings.HasPrefix(p, "./") {
						continue
					}
					rel := strings.TrimPrefix(p, "./")
					resolved := rel
					if ref.BaseDir != "" {
						resolved = path.Join(ref.BaseDir, rel)
					}
					if !s.allowedScript(resolved) {
						continue
					}
					moreFiles = append(moreFiles, model.FileRef{
						RepoOwner:    ref.RepoOwner,
						RepoName:     ref.RepoName,
						Path:         resolved,
						Kind:         "script",
						WorkflowPath: ref.WorkflowPath,
						JobID:        ref.JobID,
						StepIndex:    ref.StepIndex,
						StepName:     ref.StepName,
						FieldPath:    fieldPath,
						ContextKind:  kind,
						ActionUses:   ref.ActionUses,
						KnownEnv:     copyKnown(ref.KnownEnv),
						BaseDir:      ref.BaseDir,
					})
				}
			}
			if kind == "action_entrypoint" {
				p := strings.TrimSpace(val)
				if p == "" {
					return
				}
				resolved := p
				if ref.BaseDir != "" {
					resolved = path.Join(ref.BaseDir, p)
				}
				if !s.allowedScript(resolved) {
					return
				}
				moreFiles = append(moreFiles, model.FileRef{
					RepoOwner:    ref.RepoOwner,
					RepoName:     ref.RepoName,
					Path:         resolved,
					Kind:         "action_entrypoint",
					WorkflowPath: ref.WorkflowPath,
					JobID:        ref.JobID,
					StepIndex:    ref.StepIndex,
					StepName:     ref.StepName,
					FieldPath:    fieldPath,
					ContextKind:  "action_entrypoint",
					ActionUses:   ref.ActionUses,
					KnownEnv:     copyKnown(ref.KnownEnv),
					BaseDir:      ref.BaseDir,
				})
			}
		})
	}

	return findings, moreFiles, nil
}

func (s *Scanner) allowedScript(p string) bool {
	ext := strings.ToLower(filepath.Ext(p))
	for _, e := range s.opts.ScriptExtensions {
		if strings.ToLower(e) == ext {
			return true
		}
	}
	return false
}

func scanStringForRefs(owner, repo, workflowPath, fileKind, jobID string, stepIndex int, stepName, fieldPath, contextKind, actionUses, val string, known map[string]model.OriginHint) []model.Finding {
	var out []model.Finding
	add := func(refType, name, expr string, origin string) {
		out = append(out, model.Finding{
			RepoOwner:    owner,
			RepoName:     repo,
			WorkflowPath: workflowPath,
			JobID:        jobID,
			StepIndex:    stepIndex,
			StepName:     stepName,
			FieldPath:    fieldPath,
			FileKind:     fileKind,
			RefType:      refType,
			RefName:      name,
			Expression:   expr,
			ContextKind:  contextKind,
			ActionUses:   actionUses,
			Origin:       origin,
		})
		if known != nil {
			if refType == "secret" {
				known[name] = model.OriginHint{Origin: "expr_secret"}
			}
			if refType == "var" {
				known[name] = model.OriginHint{Origin: "expr_var"}
			}
		}
	}

	for _, m := range reSecret.FindAllStringSubmatch(val, -1) {
		add("secret", m[1], m[0], "expr_secret")
	}
	for _, m := range reVar.FindAllStringSubmatch(val, -1) {
		add("var", m[1], m[0], "expr_var")
	}
	for _, m := range reEnv.FindAllStringSubmatch(val, -1) {
		add("env", m[1], m[0], "workflow_env")
		if known != nil {
			known[m[1]] = model.OriginHint{Origin: "workflow_env"}
		}
	}

	return out
}

func copyKnown(in map[string]model.OriginHint) map[string]model.OriginHint {
	if in == nil {
		return nil
	}
	out := make(map[string]model.OriginHint, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func extractEnvKeysFromFieldPath(_ string, _ string) []string {
	return nil
}

func walkWorkflow(root *yaml.Node, fn func(fieldPath, jobID string, stepIndex int, stepName, contextKind, actionUses string, val string)) {
	// This is a best-effort walker tailored to common Actions workflow layouts.
	// root is typically a DocumentNode with one child.
	if root == nil {
		return
	}
	var doc *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	} else {
		doc = root
	}
	if doc.Kind != yaml.MappingNode {
		return
	}

	// workflow env
	if envNode := mappingGet(doc, "env"); envNode != nil {
		if envNode.Kind == yaml.MappingNode {
			for i := 0; i < len(envNode.Content); i += 2 {
				k := envNode.Content[i]
				if k != nil && k.Kind == yaml.ScalarNode && k.Value != "" {
					fn("env."+k.Value, "", 0, "", "env_block", "", "env_key:"+k.Value)
				}
			}
		}
		yamlWalkStrings(envNode, "env", func(p, v string) {
			fn(p, "", 0, "", "env_block", "", v)
		})
	}

	jobs := mappingGet(doc, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		// raw scan on whole document strings is handled elsewhere
		return
	}
	for i := 0; i < len(jobs.Content); i += 2 {
		jobID := jobs.Content[i].Value
		jobNode := jobs.Content[i+1]
		if jobNode.Kind != yaml.MappingNode {
			continue
		}

		// job env
		envNode := mappingGet(jobNode, "env")
		if envNode != nil {
			// Capture declared env keys by emitting the keys as values to the callback.
			if envNode.Kind == yaml.MappingNode {
				for i := 0; i < len(envNode.Content); i += 2 {
					k := envNode.Content[i]
					if k != nil && k.Kind == yaml.ScalarNode && k.Value != "" {
						fn("jobs."+jobID+".env."+k.Value, jobID, 0, "", "env_block", "", "env_key:"+k.Value)
					}
				}
			}
			yamlWalkStrings(envNode, "jobs."+jobID+".env", func(p, v string) {
				fn(p, jobID, 0, "", "env_block", "", v)
			})
		}

		steps := mappingGet(jobNode, "steps")
		if steps != nil && steps.Kind == yaml.SequenceNode {
			for si, step := range steps.Content {
				if step.Kind != yaml.MappingNode {
					continue
				}
				stepName := ""
				if n := mappingGet(step, "name"); n != nil && n.Kind == yaml.ScalarNode {
					stepName = n.Value
				}

				// step env
				if e := mappingGet(step, "env"); e != nil {
					if e.Kind == yaml.MappingNode {
						for i := 0; i < len(e.Content); i += 2 {
							k := e.Content[i]
							if k != nil && k.Kind == yaml.ScalarNode && k.Value != "" {
								fn(fmtPath("jobs.%s.steps[%d].env.%s", jobID, si, k.Value), jobID, si, stepName, "env_block", "", "env_key:"+k.Value)
							}
						}
					}
					yamlWalkStrings(e, fmtPath("jobs.%s.steps[%d].env", jobID, si), func(p, v string) {
						fn(p, jobID, si, stepName, "env_block", "", v)
					})
				}

				if r := mappingGet(step, "run"); r != nil && r.Kind == yaml.ScalarNode {
					fn(fmtPath("jobs.%s.steps[%d].run", jobID, si), jobID, si, stepName, "run_script", "", r.Value)
				}
				if u := mappingGet(step, "uses"); u != nil && u.Kind == yaml.ScalarNode {
					fn(fmtPath("jobs.%s.steps[%d].uses", jobID, si), jobID, si, stepName, "uses_action", "", u.Value)
				}
				if w := mappingGet(step, "with"); w != nil {
					yamlWalkStrings(w, fmtPath("jobs.%s.steps[%d].with", jobID, si), func(p, v string) {
						fn(p, jobID, si, stepName, "with_input", "", v)
					})
				}
			}
		}
	}
}

func walkActionYAML(root *yaml.Node, fn func(fieldPath, kind, val string)) {
	if root == nil {
		return
	}
	var doc *yaml.Node
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	} else {
		doc = root
	}
	if doc.Kind != yaml.MappingNode {
		return
	}
	runs := mappingGet(doc, "runs")
	if runs == nil || runs.Kind != yaml.MappingNode {
		return
	}
	using := mappingGet(runs, "using")
	usingVal := ""
	if using != nil && using.Kind == yaml.ScalarNode {
		usingVal = using.Value
	}
	if strings.EqualFold(usingVal, "composite") {
		steps := mappingGet(runs, "steps")
		if steps != nil && steps.Kind == yaml.SequenceNode {
			for i, step := range steps.Content {
				if step.Kind != yaml.MappingNode {
					continue
				}
				if r := mappingGet(step, "run"); r != nil && r.Kind == yaml.ScalarNode {
					fn(fmtPath("runs.steps[%d].run", i), "run_script", r.Value)
				}
				if w := mappingGet(step, "with"); w != nil {
					yamlWalkStrings(w, fmtPath("runs.steps[%d].with", i), func(p, v string) {
						fn(p, "with_input", v)
					})
				}
			}
		}
	}

	// node actions
	if m := mappingGet(runs, "main"); m != nil && m.Kind == yaml.ScalarNode {
		fn("runs.main", "action_entrypoint", m.Value)
	}
}

func mappingGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(m.Content); i += 2 {
		k := m.Content[i]
		v := m.Content[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == key {
			return v
		}
	}
	return nil
}

func yamlWalkStrings(n *yaml.Node, prefix string, fn func(path, val string)) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!str" || n.Tag == "" {
			fn(prefix, n.Value)
		}
	case yaml.MappingNode:
		for i := 0; i < len(n.Content); i += 2 {
			k := n.Content[i]
			v := n.Content[i+1]
			kVal := k.Value
			yamlWalkStrings(v, prefix+"."+kVal, fn)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			yamlWalkStrings(c, fmtPath("%s[%d]", prefix, i), fn)
		}
	}
}

func fmtPath(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}
