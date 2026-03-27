#!/usr/bin/env python3

import argparse
import json
import re
import sys
from pathlib import Path


def main() -> int:
    ap = argparse.ArgumentParser(
        description="Verify secret-inventory snapshot/report include GitHub line hyperlinks metadata"
    )
    ap.add_argument(
        "--out",
        required=True,
        help="Output directory containing snapshot.json and report.html",
    )
    args = ap.parse_args()

    out_dir = Path(args.out).expanduser().resolve()
    snap_path = out_dir / "snapshot.json"
    rep_path = out_dir / "report.html"

    if not snap_path.exists():
        print(f"error: missing {snap_path}", file=sys.stderr)
        return 2
    if not rep_path.exists():
        print(f"error: missing {rep_path}", file=sys.stderr)
        return 2

    with snap_path.open("r", encoding="utf-8") as f:
        s = json.load(f)

    print("github_web_base:", s.get("github_web_base"))

    repos = s.get("repos", []) or []
    print("repos:", len(repos))
    print("repos_with_scanned_ref:", sum(1 for r in repos if r.get("scanned_ref")))

    findings = s.get("findings", []) or []
    print("findings:", len(findings))
    print("findings_with_line:", sum(1 for f in findings if f.get("line_start")))
    print("findings_with_source_key:", sum(1 for f in findings if f.get("source_key")))
    print("sample_finding:", findings[0] if findings else None)

    merged = s.get("merged_findings", []) or []
    print("merged_findings:", len(merged))
    if merged:
        has_contexts = sum(1 for mf in merged if isinstance(mf.get("contexts"), list))
        print("merged_with_contexts_list:", has_contexts)
        print("sample_merged:", merged[0])

    html = rep_path.read_text(encoding="utf-8")
    print("has_source_column:", "Source</th>" in html)
    print("has_count_column:", "Count</th>" in html)
    print("has_blob_links:", "blob/" in html)

    m = re.search(r'href="([^"]+)"[^>]*>view</a>', html)
    print("first_view_link:", m.group(1) if m else None)

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
