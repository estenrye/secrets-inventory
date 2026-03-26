package report

import (
	"html/template"
	"os"
	"sort"

	"secret-inventory/internal/model"
)

type summary struct {
	RepoCount     int
	FindingCount  int
	Secrets       int
	Vars          int
	EnvExpr       int
	RuntimeEnv    int
	TopRefs       []refCount
}

type refCount struct {
	Key   string
	Count int
}

func WriteHTML(path string, snap model.Snapshot) error {
	counts := map[string]int{}
	byType := map[string]int{}
	for _, f := range snap.Findings {
		byType[f.RefType]++
		counts[f.RefType+":"+f.RefName]++
	}
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
		RepoCount:    len(snap.Repos),
		FindingCount: len(snap.Findings),
		Secrets:      byType["secret"],
		Vars:         byType["var"],
		EnvExpr:      byType["env"],
		RuntimeEnv:   byType["runtime_env"],
		TopRefs:      top,
	}

	tmpl := template.Must(template.New("r").Parse(htmlTemplate))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, struct {
		Snapshot model.Snapshot
		Summary  summary
	}{Snapshot: snap, Summary: s})
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
  <table>
    <thead>
      <tr>
        <th>Repo</th><th>Workflow</th><th>Job</th><th>Step</th><th>File</th><th>Ref</th><th>Context</th>
      </tr>
    </thead>
    <tbody>
      {{ range .Snapshot.Findings }}
      <tr>
        <td><code>{{ .RepoOwner }}/{{ .RepoName }}</code></td>
        <td><code>{{ .WorkflowPath }}</code></td>
        <td><code>{{ .JobID }}</code></td>
        <td><code>{{ .StepName }}</code></td>
        <td><code>{{ .FileKind }}{{ if .FilePath }}:{{ .FilePath }}{{ end }}</code></td>
        <td><code>{{ .RefType }}.{{ .RefName }}</code></td>
        <td><code>{{ .ContextKind }}</code></td>
      </tr>
      {{ end }}
    </tbody>
  </table>
</body>
</html>`
