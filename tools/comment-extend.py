#!/usr/bin/env python3
"""Comment out root-level `extend:` blocks in Kestra flow YAML files.

Usage:
    python3 tools/comment-extend.py [input-dir]

Defaults to ./input-flows if no directory is given.
"""

import os
import re
import sys


def comment_extend_blocks(input_dir: str) -> None:
    updated = []

    for filename in sorted(os.listdir(input_dir)):
        if not (filename.endswith(".yaml") or filename.endswith(".yml")):
            continue

        filepath = os.path.join(input_dir, filename)

        with open(filepath, "r") as f:
            lines = f.readlines()

        # Find root-level `extend:` (no leading whitespace)
        extend_line = None
        for i, line in enumerate(lines):
            if re.match(r"^extend:", line):
                extend_line = i
                break

        if extend_line is None:
            continue

        new_lines = lines[:extend_line]
        for line in lines[extend_line:]:
            new_lines.append("# " + line)

        with open(filepath, "w") as f:
            f.writelines(new_lines)

        updated.append(filename)

    print(f"Updated {len(updated)} files:")
    for f in updated:
        print(f"  {f}")


if __name__ == "__main__":
    directory = sys.argv[1] if len(sys.argv) > 1 else "input-flows"
    if not os.path.isdir(directory):
        print(f"Error: '{directory}' is not a directory", file=sys.stderr)
        sys.exit(1)
    comment_extend_blocks(directory)
