#!/bin/sh
# Strip Co-Authored-By trailers from LLM / AI coding agents.
# Used by commit-msg and prepare-commit-msg.
#
# Usage: strip-llm-coauthors.sh <commit-message-file>

set -e

msgfile=$1
if [ -z "$msgfile" ] || [ ! -f "$msgfile" ]; then
	exit 0
fi

python3 - "$msgfile" <<'PY'
import re
import sys
from pathlib import Path

path = Path(sys.argv[1])
lines = path.read_text(encoding="utf-8").splitlines()

# Names / emails commonly used by LLM coding agents in Co-Authored-By trailers.
llm = re.compile(
    r"(?i)("
    r"claude|anthropic|"
    r"cursor|cursoragent|"
    r"copilot|github\s*copilot|"
    r"chatgpt|openai|"
    r"gemini|\bbard\b|"
    r"codeium|windsurf|tabnine|"
    r"devin|\baider\b|grok|\bxai\b|"
    r"noreply@anthropic\.com|"
    r"cursoragent@cursor\.com|"
    r"[\w.+-]+@cursor\.com|"
    r"copilot@github\.com"
    r")"
)
coauthor = re.compile(r"(?i)^\s*co-authored-by:\s*")

kept = []
for line in lines:
    if coauthor.match(line) and llm.search(line):
        continue
    kept.append(line)

while kept and kept[-1].strip() == "":
    kept.pop()

path.write_text(("\n".join(kept) + "\n") if kept else "", encoding="utf-8")
PY
