#!/usr/bin/env bash
# Remind to rebuild/restart containers when Docker-related files are modified.
# Called by Claude Code PostToolUse hook.
#
# Input: JSON on stdin with tool_input containing file_path.
# Exit 0 always (non-blocking hook).

FILE=$(python3 -c "
import sys, json
try:
    data = json.load(sys.stdin)
    ti = data.get('tool_input', {})
    print(ti.get('file_path', '') or ti.get('filePath', ''))
except Exception:
    print('')
" 2>/dev/null || echo "")

if [[ "$FILE" == *"Dockerfile"* ]] || \
   [[ "$FILE" == *"docker-compose"*".yml" ]] || \
   [[ "$FILE" == *"docker-compose"*".yaml" ]]; then
    echo "Container rebuild needed: modified '$FILE'."
    echo "  Run: docker compose up --build"
    echo "  Then verify with: make smoke"
fi

exit 0
