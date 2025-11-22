#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = [
#   "pyyaml",
# ]
# ///
"""Update .env file with base64-encoded prompts from prompts.yaml.

This script idempotently updates the prompts section in .env files.
Running it multiple times produces the same result.
"""

import base64
import sys
from pathlib import Path

import yaml


START_MARKER = "# --- AUTO-GENERATED PROMPTS START (DO NOT EDIT MANUALLY) ---"
END_MARKER = "# --- AUTO-GENERATED PROMPTS END ---"


def encode_prompts(prompts_file: Path) -> list[str]:
    """Read prompts from YAML and return as base64-encoded env vars."""
    with open(prompts_file) as f:
        prompts = yaml.safe_load(f)

    lines = []
    for key, value in prompts.items():
        # Convert key to env var format: kebab-case -> SCREAMING_SNAKE_CASE
        env_key = key.upper().replace("-", "_")
        # Base64 encode the value
        encoded = base64.b64encode(value.encode()).decode()
        lines.append(f"{env_key}={encoded}")

    return lines


def update_env_file(env_file: Path, prompt_lines: list[str]) -> None:
    """Update the env file with the new prompts section."""
    if not env_file.exists():
        print(f"Error: {env_file} not found", file=sys.stderr)
        sys.exit(1)

    # Read the current file
    with open(env_file) as f:
        content = f.read()

    # Build the new prompts section
    new_section = "\n".join([
        START_MARKER,
        *prompt_lines,
        END_MARKER,
    ])

    # Check if markers exist
    has_start = START_MARKER in content
    has_end = END_MARKER in content

    if has_start and has_end:
        # Replace the section between markers
        lines = content.split("\n")
        new_lines = []
        skip = False

        for line in lines:
            if START_MARKER in line:
                new_lines.append(new_section)
                skip = True
                continue
            if END_MARKER in line:
                skip = False
                continue
            if not skip:
                new_lines.append(line)

        new_content = "\n".join(new_lines)
    else:
        # Append the section
        if not content.endswith("\n"):
            content += "\n"
        new_content = content + "\n" + new_section + "\n"

    # Write back
    with open(env_file, "w") as f:
        f.write(new_content)

    print(f"✅ Prompts updated in {env_file}")


def main():
    if len(sys.argv) > 1:
        env_file = Path(sys.argv[1])
    else:
        env_file = Path(".env.dev")

    prompts_file = Path("prompts.yaml")

    if not prompts_file.exists():
        print(f"Error: {prompts_file} not found", file=sys.stderr)
        sys.exit(1)

    prompt_lines = encode_prompts(prompts_file)
    update_env_file(env_file, prompt_lines)


if __name__ == "__main__":
    main()
