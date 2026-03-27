package report

import (
	"fmt"
	"html/template"
	"os"
	"sort"
	"strings"

	"secret-inventory/internal/model"
)

type summary struct {
	RepoCount    int
	FindingCount int
	Secrets      int
	Vars         int
	EnvExpr      int
	RuntimeEnv   int
	TopRefs      []refCount
}

type refCount struct {
	Key   string
	Count int
}

func WriteHTML(path string, snap model.Snapshot) error {
	repoRef := map[string]string{}
	for _, r := range snap.Repos {
		key := r.Owner + "/" + r.Name
		ref := strings.TrimSpace(r.ScannedRef)
		if ref == "" {
			ref = strings.TrimSpace(r.DefaultBranch)
		}
		repoRef[key] = ref
	}

	sourceURL := func(f model.Finding) string {
		filePath := ""
		switch f.FileKind {
		case "workflow_yaml":
			filePath = strings.TrimSpace(f.WorkflowPath)
		default:
			filePath = strings.TrimSpace(f.FilePath)
		}
		if filePath == "" {
			return ""
		}
		if strings.Contains(filePath, "__THIS_REPO__") || strings.Contains(filePath, "__BUILDER_CHECKOUT_DIR__") {
			return ""
		}
		ref := repoRef[f.RepoOwner+"/"+f.RepoName]
		if ref == "" {
			return ""
		}
		base := strings.TrimRight(strings.TrimSpace(snap.GitHubWebBase), "/")
		if base == "" {
			base = "https://github.com"
		}
		url := fmt.Sprintf("%s/%s/%s/blob/%s/%s", base, f.RepoOwner, f.RepoName, ref, filePath)
		if f.LineStart > 0 {
			url = fmt.Sprintf("%s#L%d", url, f.LineStart)
		}
		return url
	}

	mergedJob := func(f model.MergedFinding) string {
		if len(f.Contexts) == 0 {
			return ""
		}
		for i := 0; i < len(f.Contexts); i++ {
			v := strings.TrimSpace(f.Contexts[i].JobID)
			if v != "" {
				return v
			}
		}
		return ""
	}

	mergedContexts := func(f model.MergedFinding) string {
		if len(f.Contexts) == 0 {
			return ""
		}
		seen := map[string]struct{}{}
		out := make([]string, 0, len(f.Contexts))
		for _, c := range f.Contexts {
			k := strings.TrimSpace(c.ContextKind)
			if k == "" {
				continue
			}
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, k)
		}
		return strings.Join(out, "\n")
	}

	mergedStep := func(f model.MergedFinding) string {
		if len(f.Contexts) == 0 {
			return ""
		}
		for i := 0; i < len(f.Contexts); i++ {
			v := strings.TrimSpace(f.Contexts[i].StepName)
			if v != "" {
				return v
			}
		}
		return ""
	}

	mergedSourceURL := func(f model.MergedFinding) string {
		filePath := ""
		switch f.FileKind {
		case "workflow_yaml":
			filePath = strings.TrimSpace(f.WorkflowPath)
		default:
			filePath = strings.TrimSpace(f.FilePath)
		}
		if filePath == "" {
			return ""
		}
		if strings.Contains(filePath, "__THIS_REPO__") || strings.Contains(filePath, "__BUILDER_CHECKOUT_DIR__") {
			return ""
		}
		ref := repoRef[f.RepoOwner+"/"+f.RepoName]
		if ref == "" {
			return ""
		}
		base := strings.TrimRight(strings.TrimSpace(snap.GitHubWebBase), "/")
		if base == "" {
			base = "https://github.com"
		}
		url := fmt.Sprintf("%s/%s/%s/blob/%s/%s", base, f.RepoOwner, f.RepoName, ref, filePath)
		if f.LineStart > 0 {
			url = fmt.Sprintf("%s#L%d", url, f.LineStart)
		}
		return url
	}

	secretFindings := make([]model.Finding, 0)
	envFindings := make([]model.Finding, 0)
	varFindings := make([]model.Finding, 0)
	runtimeEnvFindings := make([]model.Finding, 0)

	secretMerged := make([]model.MergedFinding, 0)
	envMerged := make([]model.MergedFinding, 0)
	varMerged := make([]model.MergedFinding, 0)
	runtimeEnvMerged := make([]model.MergedFinding, 0)

	counts := map[string]int{}
	byType := map[string]int{}
	useMerged := len(snap.MergedFindings) > 0
	if useMerged {
		for _, f := range snap.MergedFindings {
			byType[f.RefType] += f.Count
			counts[f.RefType+":"+f.RefName] += f.Count
			switch f.RefType {
			case "secret":
				secretMerged = append(secretMerged, f)
			case "env":
				envMerged = append(envMerged, f)
			case "var":
				varMerged = append(varMerged, f)
			case "runtime_env":
				runtimeEnvMerged = append(runtimeEnvMerged, f)
			}
		}
	} else {
		for _, f := range snap.Findings {
			byType[f.RefType]++
			counts[f.RefType+":"+f.RefName]++
			switch f.RefType {
			case "secret":
				secretFindings = append(secretFindings, f)
			case "env":
				envFindings = append(envFindings, f)
			case "var":
				varFindings = append(varFindings, f)
			case "runtime_env":
				runtimeEnvFindings = append(runtimeEnvFindings, f)
			}
		}
	}

	sortFindings(secretFindings)
	sortFindings(envFindings)
	sortFindings(varFindings)
	sortFindings(runtimeEnvFindings)

	sort.SliceStable(secretMerged, func(i, j int) bool { return secretMerged[i].SourceKey < secretMerged[j].SourceKey })
	sort.SliceStable(envMerged, func(i, j int) bool { return envMerged[i].SourceKey < envMerged[j].SourceKey })
	sort.SliceStable(varMerged, func(i, j int) bool { return varMerged[i].SourceKey < varMerged[j].SourceKey })
	sort.SliceStable(runtimeEnvMerged, func(i, j int) bool { return runtimeEnvMerged[i].SourceKey < runtimeEnvMerged[j].SourceKey })

	var top []refCount
	for k, c := range counts {
		top = append(top, refCount{Key: k, Count: c})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Count == top[j].Count {
			return top[i].Key < top[j].Key
		}
		return top[i].Count > top[j].Count
	})
	if len(top) > 50 {
		top = top[:50]
	}

	s := summary{
		RepoCount: len(snap.Repos),
		FindingCount: func() int {
			if useMerged {
				return len(snap.MergedFindings)
			}
			return len(snap.Findings)
		}(),
		Secrets:    byType["secret"],
		Vars:       byType["var"],
		EnvExpr:    byType["env"],
		RuntimeEnv: byType["runtime_env"],
		TopRefs:    top,
	}

	tmpl := template.Must(template.New("r").Funcs(template.FuncMap{
		"sourceURL":       sourceURL,
		"mergedSourceURL": mergedSourceURL,
		"mergedJob":       mergedJob,
		"mergedStep":      mergedStep,
		"mergedContexts":  mergedContexts,
	}).Parse(htmlTemplate))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, struct {
		Snapshot           model.Snapshot
		Summary            summary
		SecretFindings     []model.Finding
		EnvFindings        []model.Finding
		VarFindings        []model.Finding
		RuntimeEnvFindings []model.Finding
		SecretMerged       []model.MergedFinding
		EnvMerged          []model.MergedFinding
		VarMerged          []model.MergedFinding
		RuntimeEnvMerged   []model.MergedFinding
		UseMerged          bool
	}{
		Snapshot:           snap,
		Summary:            s,
		SecretFindings:     secretFindings,
		EnvFindings:        envFindings,
		VarFindings:        varFindings,
		RuntimeEnvFindings: runtimeEnvFindings,
		SecretMerged:       secretMerged,
		EnvMerged:          envMerged,
		VarMerged:          varMerged,
		RuntimeEnvMerged:   runtimeEnvMerged,
		UseMerged:          useMerged,
	})
}

func sortFindings(findings []model.Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		a := findings[i]
		b := findings[j]

		ar := a.RepoOwner + "/" + a.RepoName
		br := b.RepoOwner + "/" + b.RepoName
		if ar != br {
			return ar < br
		}
		if a.WorkflowPath != b.WorkflowPath {
			return a.WorkflowPath < b.WorkflowPath
		}
		if a.JobID != b.JobID {
			return a.JobID < b.JobID
		}
		if a.StepIndex != b.StepIndex {
			return a.StepIndex < b.StepIndex
		}
		if a.StepName != b.StepName {
			return a.StepName < b.StepName
		}
		ak := a.FileKind
		bk := b.FileKind
		if ak != bk {
			return ak < bk
		}
		ap := strings.TrimSpace(a.FilePath)
		bp := strings.TrimSpace(b.FilePath)
		if ap != bp {
			return ap < bp
		}
		if a.RefName != b.RefName {
			return a.RefName < b.RefName
		}
		return a.FieldPath < b.FieldPath
	})
}

const htmlTemplate = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Secret Inventory Report</title>
  <style>
    body { font-family: -apple-system, BlinkMacSystemFont, Segoe UI, Roboto, sans-serif; margin: 24px; }
    .grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
    .card { border: 1px solid #ddd; border-radius: 8px; padding: 12px; }
    table { border-collapse: collapse; width: 100%; }
    th, td { border: 1px solid #ddd; padding: 8px; text-align: left; }
    th { background: #f6f6f6; }
    code { background: #f3f3f3; padding: 1px 4px; border-radius: 4px; }
  </style>
</head>
<body>
  <h1>Secret Inventory Report</h1>
  <p>Generated at: <code>{{ .Snapshot.GeneratedAt }}</code></p>

  <div class="grid">
    <div class="card"><strong>Repos</strong><div>{{ .Summary.RepoCount }}</div></div>
    <div class="card"><strong>Findings</strong><div>{{ .Summary.FindingCount }}</div></div>
    <div class="card"><strong>Secrets</strong><div>{{ .Summary.Secrets }}</div></div>
    <div class="card"><strong>Vars</strong><div>{{ .Summary.Vars }}</div></div>
    <div class="card"><strong>Env expr</strong><div>{{ .Summary.EnvExpr }}</div></div>
    <div class="card"><strong>Runtime env</strong><div>{{ .Summary.RuntimeEnv }}</div></div>
  </div>

  <h2>Top references</h2>
  <table>
    <thead><tr><th>Reference</th><th>Count</th></tr></thead>
    <tbody>
      {{ range .Summary.TopRefs }}
      <tr><td><code>{{ .Key }}</code></td><td>{{ .Count }}</td></tr>
      {{ end }}

    </tbody>
  </table>

  <h2>Findings</h2>
  <p>
    <a href="#secrets">Secrets</a> |
    <a href="#env">Env</a> |
    <a href="#vars">Vars</a> |
    <a href="#runtime-env">Runtime env</a>
  </p>

  {{ define "merged_table" }}
  <table>
    <thead>
      <tr>
        <th>Repo</th><th>Workflow</th><th>Job</th><th>Step</th><th>File</th><th>Ref</th><th>Count</th><th>Contexts</th><th>Source</th>
      </tr>
    </thead>
    <tbody>
      {{ range . }}
      <tr>
        <td><code>{{ .RepoOwner }}/{{ .RepoName }}</code></td>
        <td><code>{{ .WorkflowPath }}</code></td>
        <td><code>{{ mergedJob . }}</code></td>
        <td><code>{{ mergedStep . }}</code></td>
        <td><code>{{ .FileKind }}{{ if .FilePath }}:{{ .FilePath }}{{ end }}</code></td>
        <td><code>{{ .RefType }}.{{ .RefName }}</code></td>
        <td>{{ .Count }}</td>
        <td><pre style="margin:0; white-space:pre-wrap;"><code>{{ mergedContexts . }}</code></pre></td>
        <td>{{ $u := mergedSourceURL . }}{{ if $u }}<a href="{{ $u }}" target="_blank" rel="noreferrer">view</a>{{ else }}n/a{{ end }}</td>
      </tr>
      {{ end }}
    </tbody>
  </table>
  {{ end }}

  {{ define "findings_table" }}
  <table>
    <thead>
      <tr>
        <th>Repo</th><th>Workflow</th><th>Job</th><th>Step</th><th>File</th><th>Ref</th><th>Context</th><th>Source</th>
      </tr>
    </thead>
    <tbody>
      {{ range . }}
      <tr>
        <td><code>{{ .RepoOwner }}/{{ .RepoName }}</code></td>
        <td><code>{{ .WorkflowPath }}</code></td>
        <td><code>{{ .JobID }}</code></td>
        <td><code>{{ .StepName }}</code></td>
        <td><code>{{ .FileKind }}{{ if .FilePath }}:{{ .FilePath }}{{ end }}</code></td>
        <td><code>{{ .RefType }}.{{ .RefName }}</code></td>
        <td><code>{{ .ContextKind }}</code></td>
        <td>{{ $u := sourceURL . }}{{ if $u }}<a href="{{ $u }}" target="_blank" rel="noreferrer">view</a>{{ else }}n/a{{ end }}</td>
      </tr>
      {{ end }}
    </tbody>
  </table>
  {{ end }}

  <h3 id="secrets">Secrets</h3>
  {{ if .UseMerged }}{{ template "merged_table" .SecretMerged }}{{ else }}{{ template "findings_table" .SecretFindings }}{{ end }}

  <h3 id="env">Env</h3>
  {{ if .UseMerged }}{{ template "merged_table" .EnvMerged }}{{ else }}{{ template "findings_table" .EnvFindings }}{{ end }}

  <h3 id="vars">Vars</h3>
  {{ if .UseMerged }}{{ template "merged_table" .VarMerged }}{{ else }}{{ template "findings_table" .VarFindings }}{{ end }}

  <h3 id="runtime-env">Runtime env</h3>
  {{ if .UseMerged }}{{ template "merged_table" .RuntimeEnvMerged }}{{ else }}{{ template "findings_table" .RuntimeEnvFindings }}{{ end }}
</body>
</html>`
