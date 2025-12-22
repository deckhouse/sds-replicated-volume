#!/usr/bin/env python3
"""
Flatten a YAML document into sorted key=value lines.

Usage:
  python hack/flatten_yaml.py INPUT.yaml OUTPUT.txt

Paths are dot-separated; list indices are appended in square brackets.
Only scalar leaves are emitted. Documents with multiple YAML documents
are supported; the document index is prefixed as docN. when needed.
"""

import sys
from pathlib import Path
from typing import Any, Dict, List

import yaml


def _flatten(node: Any, prefix: str, out: Dict[str, str]) -> None:
    if isinstance(node, dict):
        for key in sorted(node.keys()):
            _flatten(node[key], f"{prefix}.{key}" if prefix else key, out)
    elif isinstance(node, list):
        for idx, item in enumerate(node):
            _flatten(item, f"{prefix}[{idx}]" if prefix else f"[{idx}]", out)
    else:
        # scalar leaf
        if node is None:
            value = "null"
        elif isinstance(node, bool):
            value = "true" if node else "false"
        else:
            value = str(node)
        out[prefix] = value


def flatten_yaml(input_path: Path) -> List[str]:
    with input_path.open("r", encoding="utf-8") as f:
        docs = list(yaml.safe_load_all(f))

    lines: Dict[str, str] = {}
    multi = len(docs) > 1
    for idx, doc in enumerate(docs):
        doc_prefix = f"doc{idx}." if multi else ""
        _flatten(doc, doc_prefix, lines)
    return [f"{k}={lines[k]}" for k in sorted(lines.keys())]


def main() -> None:
    if len(sys.argv) != 3:
        print(__doc__)
        sys.exit(1)

    input_file = Path(sys.argv[1])
    output_file = Path(sys.argv[2])

    lines = flatten_yaml(input_file)
    output_file.parent.mkdir(parents=True, exist_ok=True)
    output_file.write_text("\n".join(lines) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()

