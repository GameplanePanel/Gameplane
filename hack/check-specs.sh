#!/usr/bin/env bash
# check-specs.sh: Verify that all workspace modules have valid, non-empty specs.md files.
#
# Purpose:
#   Ensures compliance with Constitution Principle IV (spec-driven development).
#   Validates that every module listed in go.work plus the web/ subsystem has a
#   non-empty specs.md file at its root.
#
# Note on modules/:
#   Game module directories (modules/minecraft-java, modules/valheim, etc.) are
#   deliberately excluded from this check. Game module specifications are enforced
#   in the separate GameplanePanel/module repository's own CI (ruling D2).
#
# Exit codes:
#   0 = all modules have valid specs.md
#   1 = one or more modules have missing or empty specs.md, or go.work is unreadable

set -euo pipefail

# Navigate to repo root (one level up from script's directory)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
cd "$REPO_ROOT"

# Check that go.work exists and is readable
if [[ ! -f go.work ]] || [[ ! -r go.work ]]; then
    echo "✗ Error: go.work not found or unreadable"
    exit 1
fi

# Parse go.work to extract module directories
# Supports both single-line form "use ./x" and block form "use ( ... )"
# Strips trailing comments (// ...) from each line
modules=()
in_use_block=false
while IFS= read -r line; do
    # Remove trailing comment (// ...)
    line_no_comment=$(printf '%s\n' "$line" | sed 's|//.*||')

    # Detect start of use block
    if echo "$line_no_comment" | grep -q '^use[[:space:]]*($'; then
        in_use_block=true
        continue
    fi

    # Detect end of use block
    if [[ "$in_use_block" == true ]] && echo "$line_no_comment" | grep -q '^ *)$'; then
        in_use_block=false
        continue
    fi

    # Handle single-line form "use ./path" (not followed by "(")
    if echo "$line_no_comment" | grep -q '^use[[:space:]]*\.\/'; then
        module_path=$(printf '%s\n' "$line_no_comment" | sed 's/^use[[:space:]]*\.\///;s/[[:space:]]*$//')
        if [[ -n "$module_path" ]]; then
            modules+=("$module_path")
        fi
        continue
    fi

    # Process lines inside use block
    if [[ "$in_use_block" == true ]]; then
        # Skip blank lines and comments
        line_trimmed=$(printf '%s\n' "$line_no_comment" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
        if [[ -z "$line_trimmed" ]] || echo "$line_trimmed" | grep -q '^#'; then
            continue
        fi

        # Extract path: remove leading ./ and quotes
        module_path=$(printf '%s\n' "$line_trimmed" | sed 's/^\.\///;s/^"//;s/"$//')
        if [[ -n "$module_path" ]]; then
            modules+=("$module_path")
        fi
    fi
done < go.work

# If go.work was readable but no modules extracted, error
if [[ ${#modules[@]} -eq 0 ]]; then
    echo "✗ Error: go.work not found or unreadable"
    exit 1
fi

# Append web (FR-006)
modules+=("web")

# Validate each module's specs.md
failed_count=0
for module in "${modules[@]}"; do
    specs_file="$module/specs.md"

    if [[ ! -f "$specs_file" ]]; then
        echo "✗ $specs_file: missing"
        failed_count=$((failed_count + 1))
    elif [[ ! -s "$specs_file" ]]; then
        # File exists but is 0 bytes
        echo "✗ $specs_file: empty (0 bytes)"
        failed_count=$((failed_count + 1))
    elif ! grep -q '[^[:space:]]' "$specs_file"; then
        # File exists but contains only whitespace
        echo "✗ $specs_file: empty (whitespace only)"
        failed_count=$((failed_count + 1))
    fi
done

# Report results
module_count=${#modules[@]}
if [[ $failed_count -eq 0 ]]; then
    echo "✓ Checked $module_count modules: all have non-empty specs.md"
    exit 0
else
    if [[ $failed_count -eq 1 ]]; then
        echo "✗ 1 module has missing or empty specs.md"
    else
        echo "✗ $failed_count modules have missing or empty specs.md"
    fi
    exit 1
fi
